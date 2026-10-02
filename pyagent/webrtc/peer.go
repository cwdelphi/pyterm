package webrtc

import (
	"fmt"
	"io"
	"log"
	"net"
	"strings"
	"sync"
	"time"

	"github.com/pion/webrtc/v4"

	"github.com/ppy-tools/pyagent/icefilter"
	"github.com/ppy-tools/pyagent/netinfo"
	"github.com/ppy-tools/pyagent/pathcache"
)

// Peer WebRTC对等连接
type Peer struct {
	ID            string
	conn          *webrtc.PeerConnection
	dataChannels  map[string]*webrtc.DataChannel
	dataChannelMu sync.RWMutex
	onDataChannel func(name string, dc *webrtc.DataChannel)
	onClose       func()
	closed        bool
	closedMu      sync.Mutex
}

// NewPeer 创建新的对等连接（按进程级规则过滤接口）
func NewPeer(id string, config webrtc.Configuration) (*Peer, error) {
	return NewPeerLevel(id, config, 0)
}

// NewPeerLevel 按 ICE 等级创建对等连接（P2 §4.5 三级回退阶梯）：
//
//	level 1 = L1 只放行路径缓存胜出的本地接口（无缓存/开关关闭 → 自动退 L2）
//	level 2 = L2 按进程级规则过滤（缺省）
//	level 3 = L3 不设过滤（兜底）
//	level 0 = 未宣告（隧道/测速等非网关通道）→ 同 L2
func NewPeerLevel(id string, config webrtc.Configuration, level int) (*Peer, error) {
	// D4：ICE 失败后的重试节流（ice_cooldown，默认 2s）
	waitICECooldown()

	// ICE 接口过滤（P0, 方案 §4.1）+ P2 等级（§4.5）
	// predicate 在建连瞬间取快照
	se := webrtc.SettingEngine{}
	if pred := levelPredicate(level); pred != nil {
		se.SetInterfaceFilter(pred)
	}
	api := webrtc.NewAPI(webrtc.WithSettingEngine(se))

	pc, err := api.NewPeerConnection(config)
	if err != nil {
		return nil, fmt.Errorf("创建PeerConnection失败: %w", err)
	}

	pc.OnICEGatheringStateChange(func(state webrtc.ICEGatheringState) {
		if state != webrtc.ICEGatheringStateComplete {
			return
		}
		log.Printf("[ICE-FILTER] gathering complete: %s local_candidates=%d level=%d",
			icefilter.Summary(), countLocalCandidates(pc), level)
	})

	peer := &Peer{
		ID:           id,
		conn:         pc,
		dataChannels: make(map[string]*webrtc.DataChannel),
	}

	// 设置ICE连接状态回调
	pc.OnICEConnectionStateChange(func(state webrtc.ICEConnectionState) {
		log.Printf("[GA-DC] ICE connection state: %s", state.String())
		switch state {
		case webrtc.ICEConnectionStateConnected, webrtc.ICEConnectionStateCompleted,
			webrtc.ICEConnectionStateDisconnected, webrtc.ICEConnectionStateFailed:
			logSelectedCandidatePair(pc)
		}
		if state == webrtc.ICEConnectionStateConnected ||
			state == webrtc.ICEConnectionStateCompleted {
			// P2 §4.5: 记录胜出本地接口 → 路径缓存(L1 依据) + 看板 last_pair
			go recordPathCache(pc, level)
		}
		if state == webrtc.ICEConnectionStateFailed {
			markICEFailure()
			// Close 会等 gather 收尾（STUN/TURN 不可达时实测可阻塞 ~105s），
			// 在 pion 回调里同步关会卡死该 PC 的事件分发 → 异步关。
			go peer.Close()
		}
	})

	// 设置连接状态回调
	pc.OnConnectionStateChange(func(state webrtc.PeerConnectionState) {
		log.Printf("[GA-DC] connection state: %s", state.String())
		if state == webrtc.PeerConnectionStateFailed {
			markICEFailure()
			go peer.Close()
		}
	})

	// 当远程端创建DataChannel时触发回调
	pc.OnDataChannel(func(dc *webrtc.DataChannel) {
		log.Printf("[GA-DC] remote DC created: %s", dc.Label())
		peer.dataChannelMu.Lock()
		peer.dataChannels[dc.Label()] = dc
		peer.dataChannelMu.Unlock()

		peer.setupDataChannel(dc.Label(), dc)

		if peer.onDataChannel != nil {
			peer.onDataChannel(dc.Label(), dc)
		}
	})

	return peer, nil
}

// countLocalCandidates 统计本端候选数量（连通性检查候选对的分子）
func countLocalCandidates(pc *webrtc.PeerConnection) int {
	n := 0
	for _, s := range pc.GetStats() {
		if st, ok := s.(webrtc.ICECandidateStats); ok && st.Type == webrtc.StatsTypeLocalCandidate {
			n++
		}
	}
	if n > 0 {
		return n
	}
	// 兜底：stats 尚未就绪时从本地 SDP 统计
	if d := pc.CurrentLocalDescription(); d != nil {
		return strings.Count(d.SDP, "\na=candidate:")
	}
	return 0
}

// logSelectedCandidatePair 打印 ICE 选中候选对（P2P/relay 判定依据）
func logSelectedCandidatePair(pc *webrtc.PeerConnection) {
	sctp := pc.SCTP()
	if sctp == nil || sctp.Transport() == nil {
		log.Printf("[ICE-DIAG] selected pair: (no SCTP/DTLS transport)")
		return
	}
	iceTransport := sctp.Transport().ICETransport()
	if iceTransport == nil {
		log.Printf("[ICE-DIAG] selected pair: (no ICE transport)")
		return
	}
	pair, err := iceTransport.GetSelectedCandidatePair()
	if err != nil {
		log.Printf("[ICE-DIAG] selected pair: get failed: %v", err)
		return
	}
	if pair == nil || pair.Local == nil || pair.Remote == nil {
		log.Printf("[ICE-DIAG] selected pair: (nil)")
		return
	}
	log.Printf("[ICE-DIAG] selected pair: local=%s:%d(%s,%s) <-> remote=%s:%d(%s,%s)",
		pair.Local.Address, pair.Local.Port, pair.Local.Typ, pair.Local.Protocol,
		pair.Remote.Address, pair.Remote.Port, pair.Remote.Typ, pair.Remote.Protocol)
}

// levelPredicate 按网关宣告的等级生成本端接口过滤器（P2 §4.5）；返回 nil = 不过滤。
func levelPredicate(level int) func(string) bool {
	switch level {
	case 1:
		if !pathcache.Enabled() {
			return icefilter.Predicate()
		}
		e, ok := pathcache.Default().Lookup(pathcache.CacheKey, currentIfHash())
		if !ok {
			return icefilter.Predicate() // 无缓存 → 退 L2 语义
		}
		if !icefilter.Current().Match(e.LocalIface) {
			log.Printf("[ICE-LADDER] 缓存接口 %s 已被规则丢弃, 回落 L2", e.LocalIface)
			return icefilter.Predicate()
		}
		return func(name string) bool { return name == e.LocalIface }
	case 3:
		return nil
	default:
		return icefilter.Predicate()
	}
}

// currentIfHash 当前接口集合指纹（路径缓存的失效依据）
func currentIfHash() string {
	if rep := netinfo.LastReport(); rep != nil {
		return rep.IfHash
	}
	return ""
}

// recordPathCache 等待选中候选对出现后记录胜出本地接口（路径缓存 + 看板 last_pair）
func recordPathCache(pc *webrtc.PeerConnection, level int) {
	if !pathcache.Enabled() {
		return
	}
	deadline := time.Now().Add(3 * time.Second)
	var pair *webrtc.ICECandidatePair
	for time.Now().Before(deadline) {
		if pc.ConnectionState() == webrtc.PeerConnectionStateClosed {
			return
		}
		if p := selectedPair(pc); p != nil {
			pair = p
			break
		}
		time.Sleep(100 * time.Millisecond)
	}
	if pair == nil || pair.Local == nil {
		return
	}
	iface := ifaceForIP(pair.Local.Address)
	if iface == "" {
		log.Printf("[ICE-LADDER] 无法映射胜出接口 %s, 跳过缓存", pair.Local.Address)
		return
	}
	connType := classifyConnType(pair.Local.Typ, pair.Remote.Typ)
	hash := currentIfHash()
	pathcache.Default().Record(pathcache.CacheKey, iface, hash, connType, 0)
	log.Printf("[ICE-LADDER] 命中本地接口 %s (conn_type=%s level=%d)", iface, connType, level)
	// 看板: 立即补报 network_info（否则要等 5min 周期重扫）
	netinfo.SetLastPair(&netinfo.LastPair{
		Iface:    iface,
		ConnType: connType,
		IfHash:   hash,
		Level:    level,
		At:       time.Now().Unix(),
	})
	netinfo.PushStats()
}

// selectedPair 取 ICE 选中候选对；未就绪/取失败返回 nil
func selectedPair(pc *webrtc.PeerConnection) *webrtc.ICECandidatePair {
	sctp := pc.SCTP()
	if sctp == nil || sctp.Transport() == nil {
		return nil
	}
	ice := sctp.Transport().ICETransport()
	if ice == nil {
		return nil
	}
	pair, err := ice.GetSelectedCandidatePair()
	if err != nil || pair == nil || pair.Local == nil || pair.Remote == nil {
		return nil
	}
	return pair
}

// ifaceForIP 用本机接口地址反查接口名（胜出接口的缓存键）
func ifaceForIP(ip string) string {
	if ip == "" {
		return ""
	}
	list, err := net.Interfaces()
	if err != nil {
		return ""
	}
	for _, ni := range list {
		addrs, _ := ni.Addrs()
		for _, a := range addrs {
			if strings.Split(a.String(), "/")[0] == ip {
				return ni.Name
			}
		}
	}
	return ""
}

// classifyConnType 与浏览器 parseConnType 规则对齐：任一端 relay → relay，
// 两端均为 host/srflx/prflx → P2P，其余 → BUG
func classifyConnType(local, remote webrtc.ICECandidateType) string {
	if local == webrtc.ICECandidateTypeRelay || remote == webrtc.ICECandidateTypeRelay {
		return "relay"
	}
	direct := func(t webrtc.ICECandidateType) bool {
		return t == webrtc.ICECandidateTypeHost || t == webrtc.ICECandidateTypeSrflx ||
			t == webrtc.ICECandidateTypePrflx
	}
	if direct(local) && direct(remote) {
		return "P2P"
	}
	return "BUG"
}

// OnDataChannel 设置数据通道回调
func (p *Peer) OnDataChannel(handler func(name string, dc *webrtc.DataChannel)) {
	p.onDataChannel = handler
}

// OnClose 设置关闭回调
func (p *Peer) OnClose(handler func()) {
	p.onClose = handler
}

// CreateDataChannel 创建数据通道
func (p *Peer) CreateDataChannel(name string, ordered bool) (*webrtc.DataChannel, error) {
	p.dataChannelMu.Lock()
	defer p.dataChannelMu.Unlock()

	if _, exists := p.dataChannels[name]; exists {
		return nil, fmt.Errorf("数据通道 %s 已存在", name)
	}

	dc, err := p.conn.CreateDataChannel(name, &webrtc.DataChannelInit{
		Ordered: &ordered,
	})
	if err != nil {
		return nil, fmt.Errorf("创建数据通道失败: %w", err)
	}

	p.setupDataChannel(name, dc)
	return dc, nil
}

// GetOrCreateDataChannel 获取或创建数据通道
func (p *Peer) GetOrCreateDataChannel(name string, ordered bool) (*webrtc.DataChannel, error) {
	p.dataChannelMu.RLock()
	if dc, exists := p.dataChannels[name]; exists {
		p.dataChannelMu.RUnlock()
		return dc, nil
	}
	p.dataChannelMu.RUnlock()

	return p.CreateDataChannel(name, ordered)
}

// setupDataChannel 设置数据通道回调
func (p *Peer) setupDataChannel(name string, dc *webrtc.DataChannel) {
	p.dataChannels[name] = dc

	dc.OnOpen(func() {
		log.Printf("[GA-DC] DC %s opened", name)
	})

	dc.OnClose(func() {
		log.Printf("[GA-DC] DC %s closed", name)
		p.dataChannelMu.Lock()
		delete(p.dataChannels, name)
		p.dataChannelMu.Unlock()
	})

}

// SetRemoteDescription 设置远程描述
func (p *Peer) SetRemoteDescription(desc webrtc.SessionDescription) error {
	return p.conn.SetRemoteDescription(desc)
}

// CreateAnswer 创建应答
func (p *Peer) CreateAnswer() (*webrtc.SessionDescription, error) {
	answer, err := p.conn.CreateAnswer(nil)
	if err != nil {
		return nil, err
	}

	err = p.conn.SetLocalDescription(answer)
	if err != nil {
		return nil, err
	}

	return p.conn.LocalDescription(), nil
}

// CreateOffer 创建提议
func (p *Peer) CreateOffer() (*webrtc.SessionDescription, error) {
	offer, err := p.conn.CreateOffer(nil)
	if err != nil {
		return nil, err
	}

	err = p.conn.SetLocalDescription(offer)
	if err != nil {
		return nil, err
	}

	return &offer, nil
}

// AddICECandidate 添加ICE候选
func (p *Peer) AddICECandidate(candidate webrtc.ICECandidateInit) error {
	return p.conn.AddICECandidate(candidate)
}

// GetICECandidates 获取ICE候选
func (p *Peer) GetICECandidates() []webrtc.ICECandidateInit {
	return nil // 通过回调获取
}

// OnICECandidate 设置ICE候选回调
func (p *Peer) OnICECandidate(handler func(candidate *webrtc.ICECandidate)) {
	p.conn.OnICECandidate(handler)
}

// GetLocalDescription 获取本地描述
func (p *Peer) GetLocalDescription() *webrtc.SessionDescription {
	return p.conn.LocalDescription()
}

// GetStats 获取连接统计
func (p *Peer) GetStats() webrtc.StatsReport {
	return p.conn.GetStats()
}

// Close 关闭连接
func (p *Peer) Close() {
	p.closedMu.Lock()
	defer p.closedMu.Unlock()

	if p.closed {
		return
	}
	p.closed = true

	log.Printf("[GA-DC] close peer: %s", p.ID)

	// 关闭所有数据通道
	p.dataChannelMu.Lock()
	for name, dc := range p.dataChannels {
		dc.Close()
		delete(p.dataChannels, name)
	}
	p.dataChannelMu.Unlock()

	// 关闭PeerConnection
	p.conn.Close()

	// 通知关闭
	if p.onClose != nil {
		p.onClose()
	}
}

// PeerManager 对等连接管理器
type PeerManager struct {
	peers map[string]*Peer
	mu    sync.RWMutex
}

// NewPeerManager 创建对等连接管理器
func NewPeerManager() *PeerManager {
	return &PeerManager{
		peers: make(map[string]*Peer),
	}
}

// Add 添加对等连接
func (m *PeerManager) Add(peer *Peer) {
	m.mu.Lock()
	defer m.mu.Unlock()

	m.peers[peer.ID] = peer
	peer.OnClose(func() {
		m.Remove(peer.ID)
	})
}

// Get 获取对等连接
func (m *PeerManager) Get(id string) (*Peer, bool) {
	m.mu.RLock()
	defer m.mu.RUnlock()

	peer, exists := m.peers[id]
	return peer, exists
}

// Remove 移除对等连接
func (m *PeerManager) Remove(id string) {
	m.mu.Lock()
	defer m.mu.Unlock()

	if peer, exists := m.peers[id]; exists {
		peer.Close()
		delete(m.peers, id)
	}
}

// CloseAll 关闭所有连接
func (m *PeerManager) CloseAll() {
	m.mu.Lock()
	defer m.mu.Unlock()

	for id, peer := range m.peers {
		peer.Close()
		delete(m.peers, id)
	}
}

// GetByID 根据ID获取对等连接
func (m *PeerManager) GetByID(id string) *Peer {
	m.mu.RLock()
	defer m.mu.RUnlock()

	return m.peers[id]
}

// DetachDataChannel 分离数据通道获取io.ReadWriteCloser
func DetachDataChannel(dc *webrtc.DataChannel) (io.ReadWriteCloser, error) {
	return dc.Detach()
}
