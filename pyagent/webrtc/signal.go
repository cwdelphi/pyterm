package webrtc

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"io"
	"log"
	"math"
	"net"
	"net/http"
	"os"
	"runtime"
	"strings"
	"sync"
	"time"

	"github.com/pion/webrtc/v4"
	"github.com/ppy-tools/pyagent/config"
	"github.com/ppy-tools/pyagent/icecfg"
	"github.com/ppy-tools/pyagent/icefilter"
	"github.com/ppy-tools/pyagent/logger"
	"github.com/ppy-tools/pyagent/netinfo"
	"github.com/ppy-tools/pyagent/pathcache"
	"github.com/ppy-tools/pyagent/plugin"
	socks5plugin "github.com/ppy-tools/pyagent/plugins/socks5"
	speedtestplugin "github.com/ppy-tools/pyagent/plugins/speedtest"
	tunnelplugin "github.com/ppy-tools/pyagent/plugins/tunnel"
	ws "github.com/ppy-tools/pyagent/websocket"
)

// SignalMessage 信令消息
type SignalMessage struct {
	Type      string                     `json:"type"`
	RoomID    string                     `json:"room_id,omitempty"`
	AgentID   string                     `json:"agent_id,omitempty"`
	SDP       *webrtc.SessionDescription `json:"sdp,omitempty"`
	Candidate *webrtc.ICECandidateInit   `json:"candidate,omitempty"`
	Data      json.RawMessage            `json:"data,omitempty"`
}

// AgentVersion is set by main.go at startup
var AgentVersion = "dev"

// 自升级单飞：同一时刻只允许一次下载+替换，防止连点升级并发写坏运行中的二进制
var (
	upgradeMu   sync.Mutex
	upgradeBusy bool
)

// shortHash 短哈希用于连接池 key（防日志泄露明文密码）
func shortHash(s string) string {
	h := sha256.Sum256([]byte(s))
	return hex.EncodeToString(h[:8])
}

func init() {
	go cleanupIdleSSH()
}

// SignalHandler 信令处理器
type SignalHandler struct {
	client            *ws.Client
	p                 map[string]*Peer
	mu                sync.RWMutex
	pendingCandidates map[string][]webrtc.ICECandidateInit
	onReady           func()
	onClose           func()
	onAuthFailed      func()
	onRegister        func()
	onConnectSuccess  func(roomID string)
	pluginMgr         *plugin.Manager
	speedtestPlugin   *speedtestplugin.Plugin
	speedtestCancels  map[string]chan struct{} // room -> 取消信号(speedtest_stop)
	lastIceLevel      int                      // P2: 最近一次 offer 的等级（阶梯自统计用）
}

// getPluginManager 懒初始化插件管理器（配置消息在读循环串行处理，无竞态）
func (h *SignalHandler) getPluginManager() *plugin.Manager {
	if h.pluginMgr == nil {
		m := plugin.NewManager(plugin.Deps{
			Bridge: func(conn net.Conn, addr string, onResult func(ok bool)) {
				bridgeTCPConn(conn, addr, h, onResult)
			},
			SendConnectTunnel: func(agentID string) error {
				return h.client.SendConnectTunnel(agentID, h.client.Token())
			},
			SendSignal: h.sendSpeedtestSignal,
		})
		// 注册插件
		if err := m.Register(tunnelplugin.New(tunnelplugin.Deps{
			Reconcile: func(ts []config.TunnelConfig) {
				if tunnelManager != nil {
					tunnelManager.ReconcileTunnels(ts)
				} else {
					log.Printf("[TUNNEL] tunnelManager not initialized, skipping tunnel update")
				}
			},
			Stop: func() {
				if tunnelManager != nil {
					tunnelManager.StopAll()
				}
			},
			Status: func() (bool, string) {
				if tunnelManager == nil {
					return false, "not initialized"
				}
				n, detail := tunnelManager.Stats()
				return n > 0, detail
			},
			SendConnectTunnel: func(agentID string) error {
				return h.client.SendConnectTunnel(agentID, h.client.Token())
			},
		})); err != nil {
			log.Printf("[PLUGIN] 注册 tunnel 失败: %v", err)
		}
		if err := m.Register(socks5plugin.New(m.Deps())); err != nil {
			log.Printf("[PLUGIN] 注册 socks5 失败: %v", err)
		}
		sp := speedtestplugin.New(speedtestplugin.Deps{
			SendSignal: h.sendSpeedtestSignal,
			Logf:       func(format string, args ...any) { log.Printf(format, args...) },
		})
		if err := m.Register(sp); err != nil {
			log.Printf("[PLUGIN] 注册 speedtest 失败: %v", err)
		}
		h.speedtestPlugin = sp
		h.pluginMgr = m
	}
	return h.pluginMgr
}

// NewSignalHandler 创建信令处理器
func NewSignalHandler(client *ws.Client) *SignalHandler {
	return &SignalHandler{
		client:            client,
		p:                 make(map[string]*Peer),
		pendingCandidates: make(map[string][]webrtc.ICECandidateInit),
		speedtestCancels:  make(map[string]chan struct{}),
	}
}

func (h *SignalHandler) OnReady(handler func()) { h.onReady = handler }

func (h *SignalHandler) OnClose(handler func()) { h.onClose = handler }

func (h *SignalHandler) OnAuthFailed(handler func()) { h.onAuthFailed = handler }

func (h *SignalHandler) OnRegister(handler func()) { h.onRegister = handler }

func (h *SignalHandler) OnConnectSuccess(handler func(roomID string)) { h.onConnectSuccess = handler }

func (h *SignalHandler) Start() error {
	h.client.OnMessage(func(msg *ws.Message) {
		h.handleMessage(msg)
	})
	h.client.OnConnect(func() {
		log.Println("WebSocket连接成功，发送注册消息")
		h.sendRegister()
	})
	h.client.OnDisconnect(func() {
		log.Println("WebSocket断开连接")
		if h.onClose != nil {
			h.onClose()
		}
	})
	return h.client.Connect()
}

func (h *SignalHandler) handleMessage(msg *ws.Message) {
	switch msg.Type {
	case "register_success":
		log.Println("注册成功")
		if len(msg.ICEServers) > 2 {
			var iceServers []ws.ICEServerConfig
			if err := json.Unmarshal(msg.ICEServers, &iceServers); err != nil {
				log.Printf("解析ICE配置失败: %v", err)
			} else if len(iceServers) > 0 {
				h.client.SetICEServers(iceServers)
				log.Printf("已接收服务端ICE配置, %d个服务器", len(iceServers))
			}
		}
		// 处理注册时服务端下发的配置（含隧道）
		if len(msg.Config) > 2 {
			h.handleConfigUpdate(msg.Config)
		}
		// 注册后立即上报一次网络接口清单（方案 §4.2 Agent→Server network_info）
		go h.SendNetworkInfo(netinfo.LastReport())
		if h.onRegister != nil {
			go h.onRegister()
		}
	case "config_update":
		// 服务端推送配置更新
		log.Println("收到服务端配置更新")
		h.handleConfigUpdate(msg.Data)

	case "ice_scan_req":
		// 后台「立即扫描」请求（方案 §4.2 Admin→Agent）：本地扫一遍并回 network_info
		log.Println("收到后台 ICE 扫描请求")
		go h.handleIceScanReq()

	case "upgrade":
		// 服务端推送自升级指令
		log.Printf("收到升级指令: %s", string(msg.Data))
		go h.handleUpgrade(msg.Data)

	case "register_failed":
		detail := string(msg.Data)
		log.Printf("注册失败: %s", detail)
		if h.onAuthFailed != nil {
			go h.onAuthFailed()
		}
	case "browser_connect":
		log.Printf("浏览器连接请求, room: %s", msg.RoomID)
		if h.onReady != nil {
			go h.onReady()
		}
	case "connect_success":
		log.Printf("隧道连接成功, room: %s", msg.RoomID)
		if h.onConnectSuccess != nil {
			go h.onConnectSuccess(msg.RoomID)
		}
	case "speedtest_connect":
		log.Printf("[SPEEDTEST] 收到测速指令, room: %s", msg.RoomID)
		go h.startSpeedtest(msg)
	case "speedtest_stop":
		log.Printf("[SPEEDTEST] 收到测速停止, room: %s", msg.RoomID)
		h.stopSpeedtest(msg.RoomID)
	case "offer":
		h.handleOffer(msg)
	case "answer":
		h.handleAnswer(msg)
	case "candidate":
		h.handleCandidate(msg)
	case "heartbeat_ack":
	case "error":
		detail := msg.Detail
		if detail == "" {
			detail = string(msg.Data)
		}
		log.Printf("[MD-ERROR] 服务端返回错误: %s", detail)
	default:
		log.Printf("未知消息类型: %s", msg.Type)
	}
}

// runPingPhase 源端 ping 阶段: 发 N 个 ping 帧, 收集 pong RTT
func (h *SignalHandler) runPingPhase(dc *webrtc.DataChannel, cancel chan struct{}, ping *speedtestPing) (pingMs, jitterMs, lossPct float64) {
	const n = 8
	// P2 #13: 步进定时器复用，不再每帧 time.After 分配（go≥1.23 Reset 无陈旧值语义）
	pace := time.NewTimer(55 * time.Millisecond)
	pace.Stop()
	defer pace.Stop()
	for i := int64(0); i < n; i++ {
		ts := time.Now().UnixNano()
		raw, _ := json.Marshal(map[string]any{"t": "ping", "seq": i, "ts": ts})
		if err := dc.Send(append([]byte{0x60}, raw...)); err != nil {
			break
		}
		pace.Reset(55 * time.Millisecond)
		select {
		case <-cancel:
			return 0, 0, 100
		case <-pace.C:
		}
	}
	deadline := time.Now().Add(500 * time.Millisecond)
	for time.Now().Before(deadline) {
		ping.mu.Lock()
		got := len(ping.rtts)
		ping.mu.Unlock()
		if got >= n {
			break
		}
		time.Sleep(20 * time.Millisecond)
	}
	ping.mu.Lock()
	rtts := append([]float64(nil), ping.rtts...)
	ping.mu.Unlock()
	if len(rtts) == 0 {
		return 0, 0, 100
	}
	var sum float64
	for _, r := range rtts {
		sum += r
	}
	avg := sum / float64(len(rtts))
	var jsum float64
	for i := 1; i < len(rtts); i++ {
		d := rtts[i] - rtts[i-1]
		if d < 0 {
			d = -d
		}
		jsum += d
	}
	return math.Round(avg*100) / 100,
		math.Round(jsum/float64(len(rtts)-1)*100) / 100,
		math.Round((1-float64(len(rtts))/float64(n))*10000) / 100
}

func (h *SignalHandler) sendRegister() {
	data, _ := json.Marshal(map[string]string{
		"type":          "register",
		"agent_id":      h.client.AgentID(),
		"agent_name":    h.client.AgentID(),
		"agent_version": AgentVersion,
		"deploy_mode":   DeployMode,
		"arch":          runtime.GOARCH,
		"token":         h.client.Token(),
	})
	msg := &ws.Message{
		Type:    "register",
		AgentID: h.client.AgentID(),
		Data:    data,
	}
	if err := h.client.Send(msg); err != nil {
		log.Printf("发送注册消息失败: %v", err)
	}
}

// hexDump 日志用短十六进制转储（P2 #21：替代 += fmt.Sprintf 的 O(n²) 拼接；
// 输出与旧 "%02x " 逐字节一致，含尾随空格）
func hexDump(b []byte) string {
	const digits = "0123456789abcdef"
	var sb strings.Builder
	sb.Grow(len(b) * 3)
	for _, c := range b {
		sb.WriteByte(digits[c>>4])
		sb.WriteByte(digits[c&0x0f])
		sb.WriteByte(' ')
	}
	return sb.String()
}

// forgetPeer Peer 关闭时回收房间注册与候选缓冲（P2 #8）。
// 常规房间此前从不删除 → 每次断连泄漏一个 Peer(PC/goroutine/ICE)；候选随 Peer 关闭一并清。
// 身份校验：只清理仍指向该 Peer 的记录，旧 Peer 异步 Close 收尾时不会误删换代后的新 Peer。
func (h *SignalHandler) forgetPeer(roomID string, peer *Peer) {
	h.mu.Lock()
	if h.p[roomID] == peer {
		delete(h.p, roomID)
		delete(h.pendingCandidates, roomID)
	}
	h.mu.Unlock()
}

func (h *SignalHandler) handleOffer(msg *ws.Message) {
	roomID := msg.RoomID
	if roomID == "" {
		log.Printf("收到Offer但无room_id")
		return
	}
	var sdp webrtc.SessionDescription
	if err := json.Unmarshal(msg.Data, &sdp); err != nil {
		log.Printf("解析Offer失败: %v", err)
		return
	}

	log.Printf("收到Offer, room=%s, 创建PeerConnection", roomID)
	h.noteOfferLevel(msg.ICELevel)

	var iceServers []webrtc.ICEServer
	if serverICEServers := h.client.GetICEServers(); len(serverICEServers) > 0 {
		for _, s := range serverICEServers {
			iceServers = append(iceServers, webrtc.ICEServer{
				URLs:       s.URLs,
				Username:   s.Username,
				Credential: s.Credential,
			})
		}
		log.Printf("使用服务端ICE配置: %v", iceServers)
	} else {
		iceServers = []webrtc.ICEServer{
			{URLs: []string{"stun:stun.l.google.com:19302"}},
		}
		log.Printf("未收到服务端ICE配置, 回退到Google STUN")
	}

	config := webrtc.Configuration{ICEServers: iceServers}
	if strings.HasPrefix(roomID, "speedtest_") {
		// 测速: 双端 relay-only(强制走 TURN, md 侧已校验 coturn 可用)
		config.ICETransportPolicy = webrtc.ICETransportPolicyRelay
		log.Printf("[SPEEDTEST] relay-only peer (room=%s)", roomID)
	}

	peer, err := NewPeerLevel(h.client.AgentID(), config, msg.ICELevel)
	if err != nil {
		log.Printf("创建Peer失败: %v", err)
		return
	}

	// P2 #8: 先挂关闭回调再入表（此时指针未发布，不存在关闭先于注册的竞态）
	peer.OnClose(func() { h.forgetPeer(roomID, peer) })
	h.mu.Lock()
	oldPeer := h.p[roomID]
	h.p[roomID] = peer
	// 换代即丢弃上一轮协商的候选缓冲（旧 ufrag 对新 Peer 无效，answer 补发会报错刷屏）
	delete(h.pendingCandidates, roomID)
	h.mu.Unlock()
	if oldPeer != nil {
		// 旧 Peer 的 gather 可能仍卡在 STUN/TURN 拨号上（Close 会等其收尾，实测最长 ~105s），
		// 持锁/同步关闭会堵死信令读循环 → 阶梯重建的 answer 迟到/丢失 → 先换后异步关。
		go oldPeer.Close()
	}

	peer.OnICECandidate(func(candidate *webrtc.ICECandidate) {
		if candidate == nil {
			return
		}
		candidateJSON := candidate.ToJSON()
		log.Printf("[ICE-DIAG] local candidate room=%s: %s", roomID, candidateJSON.Candidate)
		h.sendCandidate(&candidateJSON, roomID)
	})

	peer.OnDataChannel(func(name string, dc *webrtc.DataChannel) {
		log.Printf("收到数据通道: %s, room: %s", name, roomID)
		if name == "ssh-terminal" || name == "data" {
			// 同步挂 OnMessage：若 go 异步，浏览器 onopen 立刻发的
			// vnc_connect/ssh_connect 可能在 handler 注册前到达并被丢弃。
			h.bridgeSSHToDataChannel(dc, roomID)
			return
		}
		if name == "tcp-tunnel" {
			// bridgeTCPTunnel 内部通过 registerTunnelDC 一次性挂好
			// OnMessage/OnClose（pion 单槽，重复注册会互相覆盖）
			h.bridgeTCPTunnel(dc, roomID)
			return
		}
		if name == "speedtest" {
			// 测速接收端: 同步挂 OnMessage(防首帧丢失), 分发给 speedtest 插件
			h.attachSpeedtestDC(dc, roomID)
			return
		}
		if h.onReady != nil {
			go h.onReady()
		}
	})

	if err := peer.SetRemoteDescription(sdp); err != nil {
		log.Printf("设置远程描述失败: %v", err)
		go peer.Close()
		return
	}

	answer, err := peer.CreateAnswer()
	if err != nil {
		log.Printf("创建Answer失败: %v", err)
		go peer.Close()
		return
	}

	h.sendAnswer(answer, roomID)
}

func (h *SignalHandler) handleAnswer(msg *ws.Message) {
	roomID := msg.RoomID
	h.mu.RLock()
	peer, ok := h.p[roomID]
	h.mu.RUnlock()
	if !ok || peer == nil {
		log.Printf("收到Answer但Peer不存在 room=%s", roomID)
		return
	}
	var sdp webrtc.SessionDescription
	sdpData := msg.Data
	if len(sdpData) == 0 {
		sdpData = msg.SDP
	}
	if err := json.Unmarshal(sdpData, &sdp); err != nil {
		log.Printf("解析Answer失败: %v", err)
		return
	}
	if err := peer.SetRemoteDescription(sdp); err != nil {
		log.Printf("设置远程描述失败: %v", err)
		return
	}
	// 刷新缓冲的ICE候选
	h.mu.Lock()
	pending := h.pendingCandidates[roomID]
	delete(h.pendingCandidates, roomID)
	h.mu.Unlock()
	for _, c := range pending {
		if err := peer.AddICECandidate(c); err != nil {
			log.Printf("添加缓冲ICE候选失败: %v", err)
		}
	}
	if len(pending) > 0 {
		log.Printf("已刷新 %d 个缓冲ICE候选 (room=%s)", len(pending), roomID)
	}
}

func (h *SignalHandler) handleCandidate(msg *ws.Message) {
	roomID := msg.RoomID
	h.mu.RLock()
	peer, ok := h.p[roomID]
	h.mu.RUnlock()
	if !ok || peer == nil {
		candRaw := msg.Data
		if len(candRaw) == 0 {
			candRaw = msg.Candidate
		}
		log.Printf("收到ICE候选但Peer不存在 room=%s (dropped): %s", roomID, string(candRaw))
		return
	}
	var candidate webrtc.ICECandidateInit
	candidateData := msg.Data
	if len(candidateData) == 0 {
		candidateData = msg.Candidate
	}
	if err := json.Unmarshal(candidateData, &candidate); err != nil {
		log.Printf("解析ICE候选失败: %v", err)
		return
	}
	log.Printf("[ICE-DIAG] remote candidate room=%s: %s", roomID, candidate.Candidate)
	if err := peer.AddICECandidate(candidate); err != nil {
		// remote description 未设置时缓冲候选
		log.Printf("[ICE-DIAG] remote candidate buffered room=%s (remote desc not set)", roomID)
		h.mu.Lock()
		h.pendingCandidates[roomID] = append(h.pendingCandidates[roomID], candidate)
		h.mu.Unlock()
	}
}

func (h *SignalHandler) sendAnswer(answer *webrtc.SessionDescription, roomID string) error {
	data, _ := json.Marshal(answer)
	msg := &ws.Message{
		Type:    "answer",
		AgentID: h.client.AgentID(),
		RoomID:  roomID,
		From:    "agent",
		Data:    data,
	}
	if err := h.client.Send(msg); err != nil {
		log.Printf("发送Answer失败 room=%s: %v", roomID, err)
		return err
	}
	return nil
}

func (h *SignalHandler) sendCandidate(candidate *webrtc.ICECandidateInit, roomID string) error {
	data, _ := json.Marshal(candidate)
	msg := &ws.Message{
		Type:    "candidate",
		AgentID: h.client.AgentID(),
		RoomID:  roomID,
		From:    "agent",
		Data:    data,
	}
	return h.client.Send(msg)
}

func (h *SignalHandler) sendOffer(offer *webrtc.SessionDescription, roomID string) error {
	data, _ := json.Marshal(offer)
	msg := &ws.Message{
		Type:    "offer",
		AgentID: h.client.AgentID(),
		RoomID:  roomID,
		From:    "agent",
		Data:    data,
	}
	return h.client.Send(msg)
}

func (h *SignalHandler) GetPeer() *Peer {
	h.mu.RLock()
	defer h.mu.RUnlock()
	for _, p := range h.p {
		return p
	}
	return nil
}

// handleConfigUpdate 处理服务端配置更新（热部署）
func (h *SignalHandler) handleConfigUpdate(data json.RawMessage) {
	var serverCfg config.ServerConfig
	if err := json.Unmarshal(data, &serverCfg); err != nil {
		log.Printf("解析服务端配置失败: %v", err)
		return
	}

	// 更新 WS 参数
	if serverCfg.WSReconnectInterval > 0 {
		h.client.SetReconnectInterval(serverCfg.WSReconnectInterval)
	}
	if serverCfg.WSHeartbeatInterval > 0 {
		h.client.SetHeartbeatInterval(serverCfg.WSHeartbeatInterval)
	}

	// 热切换日志级别
	if serverCfg.LogLevel != "" {
		logger.SetLevel(serverCfg.LogLevel)
	}

	// D4: ICE 失败重试节流窗口（缺省不改，服务端始终下发 ice_cooldown）
	if serverCfg.ICECooldown != nil {
		SetICECooldown(*serverCfg.ICECooldown)
	}

	// P1: 后台 config_json.ice 规则（优先级高于本地自扫描）
	if serverCfg.Ice != nil {
		if src, err := icecfg.Apply(serverCfg.Ice); err != nil {
			log.Printf("[ICE-CFG] mode=%s src=%s: %v", icecfg.NormalizeMode(serverCfg.Ice.Mode), src, err)
		} else {
			log.Printf("[ICE-CFG] applied src=%s %s fallback=%v cache=%v",
				src, icefilter.Summary(), serverCfg.Ice.FallbackOn(), pathcache.Enabled())
		}
	}

	// P2: 后台「清空路径缓存」（仅随本次推送, 不落库）
	if serverCfg.IceCacheClear {
		pathcache.Default().Clear()
		netinfo.ClearStats()
		log.Printf("[ICE-CFG] 路径缓存与看板数据已清空")
		go h.SendNetworkInfo(netinfo.LastReportWithStats())
	}

	// 插件分发(阶段D): normalize 双通路 → Manager 串行 Reconcile 为唯一入口;
	// tunnel/socks5 插件各自接管监听, 顶层 tunnels 镜像仅旧Agent读取, 本地不再直驱。
	h.getPluginManager().Reconcile(serverCfg.NormalizePlugins())

	// 发送确认
	h.sendConfigUpdateAck()
	log.Printf("配置热更新完成: reconnect=%ds heartbeat=%ds log_level=%s tunnels=%d plugins=%d",
		serverCfg.WSReconnectInterval, serverCfg.WSHeartbeatInterval, logger.GetLevel(), len(serverCfg.Tunnels), len(serverCfg.Plugins))
}

// SendNetworkInfo 上报网络接口清单（方案 §4.2 Agent→Server network_info）
// rep 为 nil 时跳过；服务端存入 agents.config_json.net_info 供后台展示。
func (h *SignalHandler) SendNetworkInfo(rep *netinfo.Report) {
	if rep == nil {
		return
	}
	data, err := json.Marshal(rep)
	if err != nil {
		log.Printf("序列化 network_info 失败: %v", err)
		return
	}
	msg := &ws.Message{
		Type:    "network_info",
		AgentID: h.client.AgentID(),
		Data:    data,
	}
	if err := h.client.Send(msg); err != nil {
		log.Printf("发送 network_info 失败: %v", err)
	}
}

// noteOfferLevel 记录网关宣告的 ICE 等级（P2 §4.5）：
//   - 等级上调 = 网关按阶梯回退重建，跳过 D4 失败节流（否则每级白等 2s）；
//   - 同步维护看板的 ladder 自统计并立即补报。
func (h *SignalHandler) noteOfferLevel(level int) {
	if level <= 0 {
		return
	}
	h.mu.Lock()
	prev := h.lastIceLevel
	h.lastIceLevel = level
	h.mu.Unlock()
	if prev > 0 && level > prev {
		skipICECooldown()
		log.Printf("[ICE-LADDER] 等级上调 L%d→L%d (阶梯回退重建, 跳过失败节流)", prev, level)
	}
	h.recordLadder(level)
}

// recordLadder 阶梯自统计（Agent 从 offer 观察，见方案 §7.2 2.4）
func (h *SignalHandler) recordLadder(level int) {
	l := netinfo.GetLadder()
	if l == nil {
		l = &netinfo.Ladder{}
	}
	before := *l
	if l.Level >= 1 && level > l.Level {
		l.Fallbacks++
		log.Printf("[ICE-LADDER] 回退触发 %d→%d (累计 %d 次)", l.Level, level, l.Fallbacks)
	}
	if level == 3 {
		l.L3++
		log.Printf("[ICE-LADDER] L3兜底 (累计 %d 次)", l.L3)
	}
	if level == 1 {
		l.CacheHits++
	}
	l.Level = level
	l.At = time.Now().Unix()
	if *l == before {
		return // 计数没变(仅 Level 未变) → 不打扰上报
	}
	netinfo.SetLadder(l)
	netinfo.PushStats()
}

// handleIceScanReq 后台「立即扫描」：重扫一次接口并回 network_info（扫描请求不改规则）
func (h *SignalHandler) handleIceScanReq() {
	rep, err := netinfo.Refresh(netinfo.CurrentOptions(netinfo.Options{}))
	if err != nil {
		log.Printf("[ICE-SCAN] 扫描失败: %v", err)
		return
	}
	log.Printf("[ICE-SCAN] done %s host=%d->%d pairs=%d->%d %s",
		rep.IfHash, rep.HostBefore, rep.HostAfter, rep.EstPairsBef, rep.EstPairsAft, rep.Rule.Summary())
	h.SendNetworkInfo(rep)
}

// sendConfigUpdateAck 发送配置更新确认
func (h *SignalHandler) sendConfigUpdateAck() {
	data, _ := json.Marshal(map[string]interface{}{
		"ok": true,
	})
	msg := &ws.Message{
		Type:    "config_update_ack",
		AgentID: h.client.AgentID(),
		Data:    data,
	}
	if err := h.client.Send(msg); err != nil {
		log.Printf("发送config_update_ack失败: %v", err)
	}
}

// handleUpgrade 处理服务端推送的自升级指令（单飞）
func (h *SignalHandler) handleUpgrade(data json.RawMessage) {
	upgradeMu.Lock()
	if upgradeBusy {
		upgradeMu.Unlock()
		log.Println("自升级已在执行，忽略重复指令")
		return
	}
	upgradeBusy = true
	upgradeMu.Unlock()
	defer func() {
		upgradeMu.Lock()
		upgradeBusy = false
		upgradeMu.Unlock()
	}()

	var req struct {
		Version     string `json:"version"`
		DownloadURL string `json:"download_url"`
	}
	if err := json.Unmarshal(data, &req); err != nil {
		log.Printf("解析升级指令失败: %v", err)
		return
	}
	log.Printf("开始升级: 目标版本=%s 下载地址=%s", req.Version, req.DownloadURL)

	// 1. 下载新二进制到临时文件
	tmpPath := os.Args[0] + ".new"
	resp, err := http.Get(req.DownloadURL)
	if err != nil {
		log.Printf("下载新版本失败: %v", err)
		return
	}
	defer resp.Body.Close()
	if resp.StatusCode != 200 {
		log.Printf("下载新版本失败: HTTP %d", resp.StatusCode)
		return
	}
	out, err := os.Create(tmpPath)
	if err != nil {
		log.Printf("创建临时文件失败: %v", err)
		return
	}
	written, err := io.Copy(out, resp.Body)
	out.Close()
	if err != nil {
		log.Printf("写入临时文件失败: %v", err)
		os.Remove(tmpPath)
		return
	}
	log.Printf("下载完成: %d bytes", written)

	// 2.5 完整性校验：必须是 ELF 且不小于 1MB，防止坏文件直接覆盖运行中的二进制
	if written < (1<<20) || !isELF(tmpPath) {
		log.Printf("下载文件非法(非 ELF 或过小)，放弃替换")
		os.Remove(tmpPath)
		return
	}

	// 2. 设置可执行权限
	if err := os.Chmod(tmpPath, 0755); err != nil {
		log.Printf("设置权限失败: %v", err)
		os.Remove(tmpPath)
		return
	}

	// 3. 备份当前二进制
	backupPath := os.Args[0] + ".bak"
	if err := os.Rename(os.Args[0], backupPath); err != nil {
		log.Printf("备份当前版本失败: %v", err)
		os.Remove(tmpPath)
		return
	}

	// 4. 替换为新版本
	if err := os.Rename(tmpPath, os.Args[0]); err != nil {
		log.Printf("替换二进制失败: %v", err)
		os.Rename(backupPath, os.Args[0])
		return
	}

	// 4.5 持久化到宿主路径(PERSIST_BIN): 容器重启/重建后不回退版本
	if dst := os.Getenv("PERSIST_BIN"); dst != "" && dst != os.Args[0] {
		if err := persistBinary(os.Args[0], dst); err != nil {
			log.Printf("[UPGRADE] 持久化二进制失败(不影响本次升级): %v", err)
		} else {
			log.Printf("[UPGRADE] 已持久化二进制到 %s", dst)
		}
	}

	log.Printf("升级完成，正在重启...")

	// 5. 重启自身（平台相关实现）
	restartSelf()
}

// persistBinary 将新二进制持久化到目标路径: 先同目录 tmp+rename(原子), 失败(文件挂载点 EBUSY/EROFS)回退流式覆盖
func persistBinary(src, dst string) error {
	in, err := os.Open(src)
	if err != nil {
		return err
	}
	defer in.Close()
	tmp := dst + ".new"
	out, err := os.Create(tmp)
	if err != nil {
		return err
	}
	if _, err := io.Copy(out, in); err != nil {
		out.Close()
		os.Remove(tmp)
		return err
	}
	if err := out.Chmod(0755); err != nil {
		out.Close()
		os.Remove(tmp)
		return err
	}
	out.Close()
	if err := os.Rename(tmp, dst); err != nil {
		bin, err := os.Open(src)
		if err != nil {
			os.Remove(tmp)
			return err
		}
		defer bin.Close()
		dstf, err := os.OpenFile(dst, os.O_WRONLY|os.O_TRUNC, 0755)
		if err != nil {
			os.Remove(tmp)
			return err
		}
		if _, err := io.Copy(dstf, bin); err != nil {
			dstf.Close()
			os.Remove(tmp)
			return err
		}
		dstf.Close()
		os.Remove(tmp)
	}
	return nil
}

// StopPlugins 进程退出前优雅停止全部插件监听(tunnel/socks5, 阶段D 接 main)
func (h *SignalHandler) StopPlugins() {
	if h.pluginMgr != nil {
		h.pluginMgr.StopAll()
	}
}

func (h *SignalHandler) Close() {
	h.mu.Lock()
	for id, p := range h.p {
		// 异步关：gather 卡住时 Close 会等 STUN/TURN 收尾，同步会挂死退出流程
		go p.Close()
		delete(h.p, id)
	}
	h.mu.Unlock()
	h.client.Close()
}

// isELF 判断文件是否为 ELF 可执行文件（校验魔数）
func isELF(path string) bool {
	f, err := os.Open(path)
	if err != nil {
		return false
	}
	defer f.Close()
	magic := make([]byte, 4)
	if _, err := io.ReadFull(f, magic); err != nil {
		return false
	}
	return magic[0] == 0x7f && magic[1] == 'E' && magic[2] == 'L' && magic[3] == 'F'
}

// Message type prefixes
const (
	MsgTerminal         byte = 0x00
	MsgSSHConnect       byte = 0x01
	MsgResize           byte = 0x02
	MsgAck              byte = 0x03 // P3: browser→agent 终端输出累计确认
	MsgSFTPRequest      byte = 0x10
	MsgSFTPResponse     byte = 0x11
	MsgVNCConnect       byte = 0x20
	MsgVNCData          byte = 0x21
	MsgVNCDisconnect    byte = 0x22
	MsgVNCInput         byte = 0x23
	MsgVNCError         byte = 0x2F
	MsgTunnelConnect    byte = 0x30
	MsgTunnelData       byte = 0x31
	MsgTunnelDisconnect byte = 0x32
	MsgTunnelConnectOK  byte = 0x33
	MsgTunnelUDPData    byte = 0x34
	MsgTunnelUDPConnect byte = 0x35
	MsgError            byte = 0xFF
	MsgDiagnostics      byte = 0xFE
)

type ResizeMsg struct {
	Cols int `json:"cols"`
	Rows int `json:"rows"`
}

// reportAgentError sends error diagnostics to browser
func (h *SignalHandler) reportAgentError(dc *webrtc.DataChannel, stage string, errMsg string, tcpMs int64, sshMs int64, shellMs int64, host string, port int) {
	diagMsg, _ := json.Marshal(map[string]interface{}{
		"_type":          "diagnostics_error",
		"error_stage":    stage,
		"error_msg":      errMsg,
		"agent_tcp_ms":   tcpMs,
		"agent_ssh_ms":   sshMs,
		"agent_shell_ms": shellMs,
		"agent_ssh_host": host,
		"agent_ssh_port": port,
	})
	if err := h.sendDCMsg(dc, MsgDiagnostics, diagMsg); err != nil {
		log.Printf("DC Send 0xFE error failed, fallback to WS: err=%v", err)
	}
}

// sendWSDiagnostics 通过 WebSocket 信令通道回传诊断数据（DC 发送失败时的备用路径）
func (h *SignalHandler) sendWSDiagnostics(roomID string, diagData json.RawMessage) {
	diagFields := map[string]interface{}{}
	_ = json.Unmarshal(diagData, &diagFields)
	diagFields["type"] = "agent_diagnostics"
	diagFields["room_id"] = roomID
	diagFields["agent_id"] = h.client.AgentID()
	flatData, _ := json.Marshal(diagFields)
	_ = h.client.Send(&ws.Message{
		Type:    "agent_diagnostics",
		RoomID:  roomID,
		AgentID: h.client.AgentID(),
		Data:    flatData,
	})
}

// waitVCNSendCapacity 背压时暂停读 VNC TCP（true=可继续读；false=ctx 结束/DC 关闭）
func (h *SignalHandler) waitVCNSendCapacity(ctx context.Context, dc *webrtc.DataChannel) bool {
	// P2 #13: 轮询定时器复用（go≥1.23 Reset 无陈旧值语义），不再每次 time.After 分配
	poll := time.NewTimer(10 * time.Millisecond)
	poll.Stop()
	defer poll.Stop()
	for {
		if dc.ReadyState() != webrtc.DataChannelStateOpen {
			return false
		}
		st, ok := dcWriteStateOf(dc)
		if !ok {
			return false
		}
		st.mu.Lock()
		pending := st.pendingBytes
		st.mu.Unlock()
		// 低水位才继续读，避免持续堆到丢帧阈值
		if pending <= dcPendingMaxBytes/4 && dc.BufferedAmount() <= dcBackpressureThreshold/2 {
			return true
		}
		poll.Reset(10 * time.Millisecond)
		select {
		case <-ctx.Done():
			return false
		case <-poll.C:
		}
	}
}
