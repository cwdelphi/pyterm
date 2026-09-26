package webrtc

import (
	"fmt"
	"io"
	"log"
	"sync"

	"github.com/pion/webrtc/v4"
)

// Peer WebRTC对等连接
type Peer struct {
	ID             string
	conn           *webrtc.PeerConnection
	dataChannels   map[string]*webrtc.DataChannel
	dataChannelMu  sync.RWMutex
	onDataChannel  func(name string, dc *webrtc.DataChannel)
	onClose        func()
	closed         bool
	closedMu       sync.Mutex
}

// NewPeer 创建新的对等连接
func NewPeer(id string, config webrtc.Configuration) (*Peer, error) {
pc, err := webrtc.NewPeerConnection(config)
	if err != nil {
		return nil, fmt.Errorf("创建PeerConnection失败: %w", err)
	}

	peer := &Peer{
		ID:            id,
		conn:          pc,
		dataChannels:  make(map[string]*webrtc.DataChannel),
	}

	// 设置ICE连接状态回调
	pc.OnICEConnectionStateChange(func(state webrtc.ICEConnectionState) {
		log.Printf("[GA-DC] ICE connection state: %s", state.String())
		if state == webrtc.ICEConnectionStateFailed {
			peer.Close()
		}
	})

	// 设置连接状态回调
	pc.OnConnectionStateChange(func(state webrtc.PeerConnectionState) {
		log.Printf("[GA-DC] connection state: %s", state.String())
		if state == webrtc.PeerConnectionStateFailed {
			peer.Close()
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
