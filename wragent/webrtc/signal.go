package webrtc

import (
	"context"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net"
	"net/http"
	"os"
	"sync"
	"time"

	"github.com/pion/webrtc/v4"
	"github.com/pkg/sftp"
	"github.com/ppy-tools/wragent/config"
	"github.com/ppy-tools/wragent/logger"
	ws "github.com/ppy-tools/wragent/websocket"
	gossh "golang.org/x/crypto/ssh"
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

// SSH connection pool
type sshPoolEntry struct {
	client   *gossh.Client
	lastUsed time.Time
	mu       sync.Mutex
}

var sshPool = struct {
	sync.RWMutex
	entries map[string]*sshPoolEntry
}{entries: make(map[string]*sshPoolEntry)}

// AgentVersion is set by main.go at startup
var AgentVersion = "dev"

// 自升级单飞：同一时刻只允许一次下载+替换，防止连点升级并发写坏运行中的二进制
var (
	upgradeMu   sync.Mutex
	upgradeBusy bool
)

func getSSHClient(user, addr, password string) (*gossh.Client, error) {
	// P5: key 含密码哈希，避免换密码后复用旧连接
	key := user + "@" + addr + "#" + shortHash(password)

	// Try existing connection
	sshPool.RLock()
	if entry, ok := sshPool.entries[key]; ok {
		sshPool.RUnlock()
		entry.mu.Lock()
		if entry.client.Conn != nil {
			entry.lastUsed = time.Now()
			client := entry.client
			entry.mu.Unlock()
			return client, nil
		}
		entry.mu.Unlock()
		// Connection dead, remove and reconnect
		sshPool.Lock()
		delete(sshPool.entries, key)
		sshPool.Unlock()
	} else {
		sshPool.RUnlock()
	}

	// Create new connection — P5: TCP keepalive 探测半开连接
	config := &gossh.ClientConfig{
		User: user,
		Auth: []gossh.AuthMethod{
			gossh.Password(password),
		},
		HostKeyCallback: HostKeyCallback(),
		Timeout:         10 * time.Second,
	}
	dialer := &net.Dialer{Timeout: 10 * time.Second, KeepAlive: 30 * time.Second}
	rawConn, err := dialer.Dial("tcp", addr)
	if err != nil {
		return nil, err
	}
	ncc, chans, reqs, err := gossh.NewClientConn(rawConn, addr, config)
	if err != nil {
		rawConn.Close()
		return nil, err
	}
	client := gossh.NewClient(ncc, chans, reqs)

	sshPool.Lock()
	sshPool.entries[key] = &sshPoolEntry{
		client:   client,
		lastUsed: time.Now(),
	}
	sshPool.Unlock()
	return client, nil
}

// shortHash 短哈希用于连接池 key（防日志泄露明文密码）
func shortHash(s string) string {
	h := sha256.Sum256([]byte(s))
	return hex.EncodeToString(h[:8])
}

// cleanupIdleSSH cleans up SSH connections idle for > 5 minutes
func cleanupIdleSSH() {
	for {
		time.Sleep(60 * time.Second)
		sshPool.Lock()
		for key, entry := range sshPool.entries {
			entry.mu.Lock()
			if time.Since(entry.lastUsed) > 5*time.Minute {
				entry.client.Close()
				delete(sshPool.entries, key)
				log.Printf("[SSH-POOL] closed idle connection: %s", key)
			}
			entry.mu.Unlock()
		}
		sshPool.Unlock()
	}
}

func init() {
	go cleanupIdleSSH()
}

// SignalHandler 信令处理器
type SignalHandler struct {
	client            *ws.Client
	p                 map[string]*Peer
	mu                sync.RWMutex
	dcSendMu          sync.Mutex
	pendingCandidates map[string][]webrtc.ICECandidateInit
	onReady           func()
	onClose           func()
	onAuthFailed      func()
	onRegister        func()
	onConnectSuccess  func(roomID string)
}

// NewSignalHandler 创建信令处理器
func NewSignalHandler(client *ws.Client) *SignalHandler {
	return &SignalHandler{
		client:            client,
		p:                 make(map[string]*Peer),
		pendingCandidates: make(map[string][]webrtc.ICECandidateInit),
	}
}

func (h *SignalHandler) OnReady(handler func())                       { h.onReady = handler }
func (h *SignalHandler) OnClose(handler func())                       { h.onClose = handler }
func (h *SignalHandler) OnAuthFailed(handler func())                  { h.onAuthFailed = handler }
func (h *SignalHandler) OnRegister(handler func())                    { h.onRegister = handler }
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
		if h.onRegister != nil {
			go h.onRegister()
		}
	case "config_update":
		// 服务端推送配置更新
		log.Println("收到服务端配置更新")
		h.handleConfigUpdate(msg.Data)

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
	case "offer":
		h.handleOffer(msg)
	case "answer":
		h.handleAnswer(msg)
	case "candidate":
		h.handleCandidate(msg)
	case "heartbeat_ack":
	default:
		log.Printf("未知消息类型: %s", msg.Type)
	}
}

// CreateTunnelPeer 隧道模式：主动创建 Peer + DataChannel + Offer
func (h *SignalHandler) CreateTunnelPeer(roomID string) {
	log.Printf("[TUNNEL] 创建隧道Peer, room=%s", roomID)

	var iceServers []webrtc.ICEServer
	if serverICEServers := h.client.GetICEServers(); len(serverICEServers) > 0 {
		for _, s := range serverICEServers {
			iceServers = append(iceServers, webrtc.ICEServer{
				URLs:       s.URLs,
				Username:   s.Username,
				Credential: s.Credential,
			})
		}
	} else {
		iceServers = []webrtc.ICEServer{
			{URLs: []string{"stun:stun.l.google.com:19302"}},
		}
	}

	config := webrtc.Configuration{ICEServers: iceServers}
	peer, err := NewPeer(h.client.AgentID(), config)
	if err != nil {
		log.Printf("[TUNNEL] 创建Peer失败: %v", err)
		return
	}

	h.mu.Lock()
	h.p[roomID] = peer
	h.mu.Unlock()

	peer.OnICECandidate(func(candidate *webrtc.ICECandidate) {
		if candidate == nil {
			return
		}
		candidateJSON := candidate.ToJSON()
		h.sendCandidate(&candidateJSON, roomID)
	})

	// 创建 DataChannel "tcp-tunnel"
	dc, err := peer.CreateDataChannel("tcp-tunnel", true)
	if err != nil {
		log.Printf("[TUNNEL] 创建DataChannel失败: %v", err)
		peer.Close()
		return
	}

	dc.OnOpen(func() {
		log.Printf("[TUNNEL] DataChannel 'tcp-tunnel' 已打开 (room=%s)", roomID)
		RegisterTunnelDC(dc)
		if h.onReady != nil {
			go h.onReady()
		}
	})

	// CreateOffer 内部已调用 SetLocalDescription
	offer, err := peer.CreateOffer()
	if err != nil {
		log.Printf("[TUNNEL] 创建Offer失败: %v", err)
		peer.Close()
		return
	}

	h.sendOffer(offer, roomID)
	log.Printf("[TUNNEL] Offer已发送, room=%s", roomID)
}

func (h *SignalHandler) sendRegister() {
	data, _ := json.Marshal(map[string]string{
		"type":          "register",
		"agent_id":      h.client.AgentID(),
		"agent_name":    h.client.AgentID(),
		"agent_version": AgentVersion,
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

	peer, err := NewPeer(h.client.AgentID(), config)
	if err != nil {
		log.Printf("创建Peer失败: %v", err)
		return
	}

	h.mu.Lock()
	if oldPeer, exists := h.p[roomID]; exists {
		oldPeer.Close()
		delete(h.p, roomID)
	}
	h.p[roomID] = peer
	h.mu.Unlock()

	peer.OnICECandidate(func(candidate *webrtc.ICECandidate) {
		if candidate == nil {
			return
		}
		candidateJSON := candidate.ToJSON()
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
			RegisterTunnelDC(dc)
			h.bridgeTCPTunnel(dc, roomID)
			return
		}
		if h.onReady != nil {
			go h.onReady()
		}
	})

	if err := peer.SetRemoteDescription(sdp); err != nil {
		log.Printf("设置远程描述失败: %v", err)
		peer.Close()
		return
	}

	answer, err := peer.CreateAnswer()
	if err != nil {
		log.Printf("创建Answer失败: %v", err)
		peer.Close()
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
		log.Printf("收到ICE候选但Peer不存在 room=%s", roomID)
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
	if err := peer.AddICECandidate(candidate); err != nil {
		// remote description 未设置时缓冲候选
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
	return h.client.Send(msg)
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

	// 热更新隧道
	if tunnelManager != nil {
		tunnelManager.ReconcileTunnels(serverCfg.Tunnels)
		// 为每个有目标Agent的隧道发送connect_tunnel，建立WebRTC DataChannel
		for _, t := range serverCfg.Tunnels {
			if t.Enabled && t.TargetAgentID != "" {
				if err := h.client.SendConnectTunnel(t.TargetAgentID, h.client.Token()); err != nil {
					log.Printf("[TUNNEL] connect_tunnel to %s failed: %v", t.TargetAgentID, err)
				} else {
					log.Printf("[TUNNEL] connect_tunnel sent to %s for tunnel %s", t.TargetAgentID, t.ID)
				}
			}
		}
	} else {
		log.Printf("tunnelManager not initialized, skipping tunnel update")
	}

	// 发送确认
	h.sendConfigUpdateAck()
	log.Printf("配置热更新完成: reconnect=%ds heartbeat=%ds log_level=%s tunnels=%d",
		serverCfg.WSReconnectInterval, serverCfg.WSHeartbeatInterval, logger.GetLevel(), len(serverCfg.Tunnels))
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

	log.Printf("升级完成，正在重启...")

	// 5. 重启自身（平台相关实现）
	restartSelf()
}

func (h *SignalHandler) Close() {
	h.mu.Lock()
	for id, p := range h.p {
		p.Close()
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

type SSHConnectMsg struct {
	Type     string `json:"type"`
	Host     string `json:"host"`
	Port     int    `json:"port"`
	Username string `json:"username"`
	AuthType string `json:"auth_type"`
	Password string `json:"password"`
	Cols     int    `json:"cols"`
	Rows     int    `json:"rows"`
}

type ResizeMsg struct {
	Cols int `json:"cols"`
	Rows int `json:"rows"`
}

// sshSession 保存 SSH 会话状态
type sshSession struct {
	dc      *webrtc.DataChannel
	sshConn *gossh.Client
	session *gossh.Session
	stdin   interface{ Write([]byte) (int, error) }
}

// vncSession 保存 VNC 桥接状态
type vncSession struct {
	cancel  context.CancelFunc
	inputCh chan []byte
}

const dcBackpressureThreshold = 64 * 1024 // 64KB
const dcPendingMaxBytes = 256 * 1024      // P1: 背压待发队列上限 256KB

// dcWriteState P1: 背压时暂存高频帧，低水位冲刷；队列满则丢最旧并限频日志
// P3: 终端输出帧带4B BE seq（创建时分配，丢帧留下空洞供浏览器检测）；lastAck 供诊断
type dcWriteState struct {
	mu           sync.Mutex
	pending      [][]byte
	pendingBytes int
	dropped      uint64
	lastDropLog  time.Time
	flushing     bool
	txSeq        uint32
	lastAckSeq   uint32
	hasAck       bool
}

var dcWriteStates sync.Map // *webrtc.DataChannel -> *dcWriteState

func getDCWriteState(dc *webrtc.DataChannel) *dcWriteState {
	if v, ok := dcWriteStates.Load(dc); ok {
		return v.(*dcWriteState)
	}
	st, _ := dcWriteStates.LoadOrStore(dc, &dcWriteState{})
	return st.(*dcWriteState)
}

// sendDCMsg 通过 DataChannel 发送带前缀的消息 (API 模式)
// 终端输出 (0x00) 帧格式: [0x00][4B BE seq][payload]
// VNC (0x21) 是连续 RFB 字节流：任何丢帧/乱序都会导致 noVNC zlib 解码失败，
// 因此 VNC 只入队保序、绝不丢弃；背压时由 bridgeVNC 暂停读 TCP 做反压。
func (h *SignalHandler) sendDCMsg(dc *webrtc.DataChannel, prefix byte, data []byte) error {
	var msg []byte
	if prefix == MsgTerminal {
		st := getDCWriteState(dc)
		st.mu.Lock()
		seq := st.txSeq
		st.txSeq++
		st.mu.Unlock()
		msg = make([]byte, 5+len(data))
		msg[0] = prefix
		msg[1] = byte(seq >> 24)
		msg[2] = byte(seq >> 16)
		msg[3] = byte(seq >> 8)
		msg[4] = byte(seq)
		copy(msg[5:], data)
	} else {
		msg = make([]byte, 1+len(data))
		msg[0] = prefix
		copy(msg[1:], data)
	}

	if prefix == MsgTerminal || prefix == MsgVNCData {
		st := getDCWriteState(dc)
		st.mu.Lock()
		// 队列非空/flush 中/缓冲高：必须入队，禁止直发越过 pending（防乱序）
		needQueue := len(st.pending) > 0 || st.flushing || dc.BufferedAmount() > dcBackpressureThreshold
		if needQueue {
			if prefix == MsgVNCData {
				// VNC 绝不丢：等空间（配合 bridgeVNC 停读 TCP，由 SCTP/TCP 窗口反压）
				for st.pendingBytes+len(msg) > dcPendingMaxBytes {
					st.mu.Unlock()
					if dc.ReadyState() != webrtc.DataChannelStateOpen {
						return fmt.Errorf("datachannel closed")
					}
					time.Sleep(5 * time.Millisecond)
					st.mu.Lock()
				}
				st.pending = append(st.pending, msg)
				st.pendingBytes += len(msg)
				st.mu.Unlock()
				h.scheduleDCFlush(dc, st)
				return nil
			}
			// 终端可丢（有 seq 空洞检测）
			for st.pendingBytes+len(msg) > dcPendingMaxBytes && len(st.pending) > 0 {
				old := st.pending[0]
				st.pending = st.pending[1:]
				st.pendingBytes -= len(old)
				st.dropped++
				if time.Since(st.lastDropLog) > 5*time.Second {
					st.lastDropLog = time.Now()
					log.Printf("[A-BRIDGE] backpressure drop prefix=0x%02x total=%d pendingBytes=%d buffered=%d",
						prefix, st.dropped, st.pendingBytes, dc.BufferedAmount())
				}
			}
			if len(msg) > dcPendingMaxBytes {
				st.dropped++
				st.mu.Unlock()
				return nil
			}
			st.pending = append(st.pending, msg)
			st.pendingBytes += len(msg)
			st.mu.Unlock()
			h.scheduleDCFlush(dc, st)
			return nil
		}
		// 空闲直发：持 st.mu，避免与 flush 取批竞态导致乱序
		h.dcSendMu.Lock()
		err := dc.Send(msg)
		h.dcSendMu.Unlock()
		st.mu.Unlock()
		if err != nil {
			log.Printf("[A-BRIDGE] DC send error prefix=0x%02x len=%d: %v", prefix, len(data), err)
		}
		return err
	}

	h.dcSendMu.Lock()
	err := dc.Send(msg)
	h.dcSendMu.Unlock()
	if err != nil {
		log.Printf("[A-BRIDGE] DC send error prefix=0x%02x len=%d: %v", prefix, len(data), err)
	}
	return err
}

// enqueueDCFrame 保留给非 sendDCMsg 路径；VNC 不走丢帧逻辑
func (h *SignalHandler) enqueueDCFrame(dc *webrtc.DataChannel, prefix byte, msg []byte) error {
	if prefix == MsgVNCData {
		st := getDCWriteState(dc)
		st.mu.Lock()
		for st.pendingBytes+len(msg) > dcPendingMaxBytes {
			st.mu.Unlock()
			if dc.ReadyState() != webrtc.DataChannelStateOpen {
				return fmt.Errorf("datachannel closed")
			}
			time.Sleep(5 * time.Millisecond)
			st.mu.Lock()
		}
		st.pending = append(st.pending, msg)
		st.pendingBytes += len(msg)
		st.mu.Unlock()
		h.scheduleDCFlush(dc, st)
		return nil
	}
	st := getDCWriteState(dc)
	st.mu.Lock()
	defer st.mu.Unlock()
	for st.pendingBytes+len(msg) > dcPendingMaxBytes && len(st.pending) > 0 {
		old := st.pending[0]
		st.pending = st.pending[1:]
		st.pendingBytes -= len(old)
		st.dropped++
		if time.Since(st.lastDropLog) > 5*time.Second {
			st.lastDropLog = time.Now()
			log.Printf("[A-BRIDGE] backpressure drop prefix=0x%02x total=%d pendingBytes=%d buffered=%d",
				prefix, st.dropped, st.pendingBytes, dc.BufferedAmount())
		}
	}
	if len(msg) > dcPendingMaxBytes {
		st.dropped++
		return nil
	}
	st.pending = append(st.pending, msg)
	st.pendingBytes += len(msg)
	h.scheduleDCFlush(dc, st)
	return nil
}

// scheduleDCFlush 低水位/短延迟后冲刷待发队列；持续到清空或 DC 关闭（防 40 次后停刷死锁）
func (h *SignalHandler) scheduleDCFlush(dc *webrtc.DataChannel, st *dcWriteState) {
	if st.flushing {
		return
	}
	st.flushing = true
	go func() {
		for {
			time.Sleep(25 * time.Millisecond)
			st.mu.Lock()
			if len(st.pending) == 0 {
				st.flushing = false
				st.mu.Unlock()
				return
			}
			if dc.ReadyState() != webrtc.DataChannelStateOpen {
				st.pending = nil
				st.pendingBytes = 0
				st.flushing = false
				st.mu.Unlock()
				return
			}
			// 缓冲仍高：继续等
			if dc.BufferedAmount() > dcBackpressureThreshold/2 {
				st.mu.Unlock()
				continue
			}
			// 冲刷一批（flushing=true 期间新帧只入队，保序）
			batch := st.pending
			st.pending = nil
			st.pendingBytes = 0
			st.mu.Unlock()
			for _, m := range batch {
				if dc.BufferedAmount() > dcBackpressureThreshold {
					// 又满了：剩余重新入队头部；VNC 帧绝不丢弃
					st.mu.Lock()
					rest := append([][]byte{m}, st.pending...)
					var kept [][]byte
					var bytes int
					for _, x := range rest {
						isVNC := len(x) > 0 && x[0] == MsgVNCData
						if isVNC {
							kept = append(kept, x)
							bytes += len(x)
							continue
						}
						if bytes+len(x) > dcPendingMaxBytes {
							st.dropped++
							continue
						}
						kept = append(kept, x)
						bytes += len(x)
					}
					st.pending = kept
					st.pendingBytes = bytes
					st.mu.Unlock()
					break
				}
				h.dcSendMu.Lock()
				_ = dc.Send(m)
				h.dcSendMu.Unlock()
			}
		}
	}()
}

func (h *SignalHandler) sendDCErrorMsg(dc *webrtc.DataChannel, detail string) {
	data, _ := json.Marshal(map[string]interface{}{"type": "error", "detail": detail})
	h.sendDCMsg(dc, MsgError, data)
}

// handleTerminalAck P3: 浏览器回传已收到的最大终端 seq（4B BE）
func (h *SignalHandler) handleTerminalAck(dc *webrtc.DataChannel, payload []byte) {
	if len(payload) < 4 {
		return
	}
	seq := uint32(payload[0])<<24 | uint32(payload[1])<<16 | uint32(payload[2])<<8 | uint32(payload[3])
	st := getDCWriteState(dc)
	st.mu.Lock()
	if !st.hasAck || seq > st.lastAckSeq {
		st.lastAckSeq = seq
		st.hasAck = true
	}
	dropped := st.dropped
	tx := st.txSeq
	st.mu.Unlock()
	// 诊断: ack 落后于 tx 说明有丢帧未被浏览器见过
	if tx > seq+1 && dropped > 0 {
		log.Printf("[A-BRIDGE] terminal seq ack=%d tx=%d dropped=%d (gaps expected if backpressure)", seq, tx, dropped)
	}
}

func (h *SignalHandler) sendDCTerminalData(dc *webrtc.DataChannel, data []byte) {
	h.sendDCMsg(dc, MsgTerminal, data)
}

// bridgeSSHToDataChannel 桥接 WebRTC DataChannel 到目标 SSH 服务器
// 使用 API 模式: dc.OnMessage 读取, dc.Send 写入

type VNCConnectMsg struct {
	Type        string      `json:"type"`
	Host        string      `json:"host"`
	Port        int         `json:"port"`
	Password    string      `json:"password"`
	PixelFormat string      `json:"pixel_format"`
	ColorDepth  interface{} `json:"color_depth"`
	ReadOnly    bool        `json:"read_only"`
}

func (h *SignalHandler) bridgeSSHToDataChannel(dc *webrtc.DataChannel, roomID string) {
	var sess *sshSession
	var vnc *vncSession
	// sess/vnc 由 OnMessage/OnClose 并发访问，需互斥
	var stateMu sync.Mutex

	if dc.ReadyState() == webrtc.DataChannelStateOpen {
		log.Printf("DataChannel %s 已打开 (API模式)", dc.Label())
	}

	dc.OnMessage(func(msg webrtc.DataChannelMessage) {
		data := msg.Data
		if len(data) < 2 {
			return
		}
		prefix := data[0]
		payload := data[1:]

		stateMu.Lock()
		defer stateMu.Unlock()

		// ── VNC 消息路由 ──
		if prefix == MsgVNCConnect || prefix == MsgVNCInput || prefix == MsgVNCDisconnect {
			// 断开: 浏览器关闭 VNC 时通知 agent
			if prefix == MsgVNCDisconnect {
				if vnc != nil {
					log.Printf("[A-BRIDGE] VNC disconnect requested (room=%s)", roomID)
					vnc.cancel()
					vnc = nil
				}
				return
			}
			// 新连接: 若已有活跃会话先关掉旧的
			if prefix == MsgVNCConnect {
				if vnc != nil {
					log.Printf("[A-BRIDGE] VNC reconnect: closing old session (room=%s)", roomID)
					vnc.cancel()
					vnc = nil
				}
				ctx, cancel := context.WithCancel(context.Background())
				vncInputCh := make(chan []byte, 64)
				vnc = &vncSession{cancel: cancel, inputCh: vncInputCh}
				go func() {
					h.bridgeVNC(ctx, dc, roomID, payload, vncInputCh)
					log.Printf("[A-BRIDGE] VNC bridge ended (room=%s)", roomID)
				}()
				return
			}
			// 输入: 转发到当前活跃桥接
			if prefix == MsgVNCInput && vnc != nil && vnc.inputCh != nil {
				if len(data) <= 80 {
					hexStr := ""
					for _, b := range data[:len(data)] {
						hexStr += fmt.Sprintf("%02x ", b)
					}
					log.Printf("[A-BRIDGE] VNC input %d bytes: %s (room=%s)", len(data), hexStr, roomID)
				} else {
					log.Printf("[A-BRIDGE] VNC input %d bytes (room=%s)", len(data), roomID)
				}
				select {
				case vnc.inputCh <- data:
				default:
				}
				return
			}
			return
		}

		// ── SSH 消息路由 ──
		if sess == nil {
			// 首帧必须是 SSH 连接；失败后允许浏览器重发 0x01 恢复 (R6)
			if prefix != MsgSSHConnect && prefix != MsgAck {
				h.sendDCErrorMsg(dc, "首条消息必须是SSH连接指令 (0x01)")
				return
			}
			if prefix == MsgAck {
				h.handleTerminalAck(dc, payload)
				return
			}
			var connectMsg SSHConnectMsg
			if err := json.Unmarshal(payload, &connectMsg); err != nil {
				h.sendDCErrorMsg(dc, "SSH连接指令解析失败: "+err.Error())
				return
			}
			sess = h.connectSSH(dc, &connectMsg, roomID)
			// 失败时 sess 仍为 nil，下一条 0x01 可重试
			return
		}

		// 后续消息: 分发处理
		switch prefix {
		case MsgAck:
			h.handleTerminalAck(dc, payload)
		case MsgSSHConnect:
			// 已有会话时收到新连接指令 → 关闭旧会话后重建 (R6 幂等重连)
			if sess.session != nil {
				sess.session.Close()
			}
			var connectMsg SSHConnectMsg
			if err := json.Unmarshal(payload, &connectMsg); err != nil {
				h.sendDCErrorMsg(dc, "SSH连接指令解析失败: "+err.Error())
				return
			}
			sess = h.connectSSH(dc, &connectMsg, roomID)
		case MsgTerminal:
			if sess.stdin != nil {
				sess.stdin.Write(payload)
			}
		case MsgResize:
			// P9: window-change 必须发到带 PTY 的原会话，不能新开 Session
			var resize ResizeMsg
			if json.Unmarshal(payload, &resize) == nil && sess.session != nil {
				if err := sess.session.WindowChange(resize.Rows, resize.Cols); err != nil {
					log.Printf("WindowChange failed room=%s: %v", roomID, err)
				}
			}
		case MsgSFTPRequest:
			if sess.sshConn != nil {
				sshConnRef := sess.sshConn
				go h.handleSFTPData(dc, payload, sshConnRef)
			}
		default:
			// 未知前缀，当作终端数据
			if sess.stdin != nil {
				sess.stdin.Write(payload)
			}
		}
	})

	// R8: DC 关闭时回收 SSH 会话，避免目标机残留孤儿 shell
	dc.OnClose(func() {
		log.Printf("DataChannel closed, cleaning SSH session (room=%s)", roomID)
		dcWriteStates.Delete(dc)
		stateMu.Lock()
		defer stateMu.Unlock()
		if vnc != nil {
			vnc.cancel()
			vnc = nil
		}
		if sess != nil {
			if sess.session != nil {
				sess.session.Close()
			}
			sess = nil
		}
	})
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

// reportVNCError sends VNC error diagnostics to browser
func (h *SignalHandler) reportVNCError(dc *webrtc.DataChannel, stage string, errMsg string, tcpMs int64, host string, port int, roomID string) {
	diagMsg, _ := json.Marshal(map[string]interface{}{
		"_type":          "diagnostics_error",
		"error_stage":    stage,
		"error_msg":      errMsg,
		"agent_tcp_ms":   tcpMs,
		"agent_ssh_host": host,
		"agent_ssh_port": port,
	})
	if err := h.sendDCMsg(dc, MsgDiagnostics, diagMsg); err != nil {
		log.Printf("DC Send 0xFE VNC error failed: err=%v", err)
	}
	// 同时通过WS发送，确保gateway模式下浏览器也能收到
	h.sendWSDiagnostics(roomID, diagMsg)
}

// connectSSH 建立 SSH 连接并启动桥接
func (h *SignalHandler) connectSSH(dc *webrtc.DataChannel, connectMsg *SSHConnectMsg, roomID string) *sshSession {
	sSHStart := time.Now()
	addr := fmt.Sprintf("%s:%d", connectMsg.Host, connectMsg.Port)
	log.Printf("收到SSH连接指令: %s@%s (room=%s)", connectMsg.Username, addr, roomID)

	sshConn, err := getSSHClient(connectMsg.Username, addr, connectMsg.Password)
	tCPSSHMs := time.Since(sSHStart).Milliseconds()
	if err != nil {
		h.reportAgentError(dc, "agent_ssh", err.Error(), tCPSSHMs, 0, 0, connectMsg.Host, connectMsg.Port)
		h.sendDCErrorMsg(dc, "SSH连接失败: "+err.Error())
		return nil
	}

	session, err := sshConn.NewSession()
	if err != nil {
		h.sendDCErrorMsg(dc, "创建SSH会话失败: "+err.Error())
		return nil
	}

	modes := gossh.TerminalModes{
		gossh.ECHO:          1,
		gossh.TTY_OP_ISPEED: 14400,
		gossh.TTY_OP_OSPEED: 14400,
	}
	cols := connectMsg.Cols
	if cols <= 0 {
		cols = 120
	}
	rows := connectMsg.Rows
	if rows <= 0 {
		rows = 40
	}
	if err := session.RequestPty("xterm", rows, cols, modes); err != nil {
		tSessionMs := time.Since(sSHStart).Milliseconds()
		h.reportAgentError(dc, "agent_shell", err.Error(), tCPSSHMs, 0, tSessionMs-tCPSSHMs, connectMsg.Host, connectMsg.Port)
		h.sendDCErrorMsg(dc, "请求PTY失败: "+err.Error())
		session.Close()
		return nil
	}

	stdin, err := session.StdinPipe()
	if err != nil {
		h.sendDCErrorMsg(dc, "获取stdin失败: "+err.Error())
		session.Close()
		return nil
	}

	stdout, err := session.StdoutPipe()
	if err != nil {
		h.sendDCErrorMsg(dc, "获取stdout失败: "+err.Error())
		session.Close()
		return nil
	}

	stderr, err := session.StderrPipe()
	if err != nil {
		h.sendDCErrorMsg(dc, "获取stderr失败: "+err.Error())
		session.Close()
		return nil
	}

	if err := session.Shell(); err != nil {
		tShellMs := time.Since(sSHStart).Milliseconds()
		h.reportAgentError(dc, "agent_shell", err.Error(), tCPSSHMs, 0, tShellMs-tCPSSHMs, connectMsg.Host, connectMsg.Port)
		h.sendDCErrorMsg(dc, "启动shell失败: "+err.Error())
		session.Close()
		return nil
	}

	log.Printf("SSH会话建立成功，开始桥接: %s@%s", connectMsg.Username, addr)

	// 发送诊断消息(细粒度时间) — 准备好数据，但延迟到首帧终端数据之后再发送
	tShellDone := time.Since(sSHStart).Milliseconds()
	tSessionSetupMs := tShellDone - tCPSSHMs
	diagMsg, _ := json.Marshal(map[string]interface{}{
		"_type":            "diagnostics",
		"agent_connect_ms": tShellDone,
		"agent_tcp_ms":     tCPSSHMs,
		"agent_ssh_ms":     tSessionSetupMs,
		"agent_shell_ms":   0,
		"agent_ssh_host":   connectMsg.Host,
		"agent_ssh_port":   connectMsg.Port,
	})
	var diagSent sync.Once
	sendDiagOnce := func() {
		diagSent.Do(func() {
			if err := h.sendDCMsg(dc, MsgDiagnostics, diagMsg); err != nil {
				log.Printf("DC Send 0xFE failed, fallback to WS: room=%s err=%v", roomID, err)
				h.sendWSDiagnostics(roomID, diagMsg)
			}
		})
	}

	// SSH stdout → DataChannel (通过 dc.Send)
	go func() {
		buf := make([]byte, 32*1024)
		for {
			n, err := stdout.Read(buf)
			if n > 0 {
				h.sendDCTerminalData(dc, buf[:n])
				// 首帧终端数据发送后，再发送0xFE诊断，确保浏览器先收到终端数据
				sendDiagOnce()
			}
			if err != nil {
				break
			}
		}
		// stdout结束时兜底发送0xFE（如果没有任何终端数据）
		sendDiagOnce()
	}()

	// SSH stderr → DataChannel (通过 dc.Send)
	go func() {
		buf := make([]byte, 32*1024)
		for {
			n, err := stderr.Read(buf)
			if n > 0 {
				h.sendDCTerminalData(dc, buf[:n])
				sendDiagOnce()
			}
			if err != nil {
				break
			}
		}
		sendDiagOnce()
	}()

	// 等待 SSH 会话结束
	go func() {
		session.Wait()
		log.Printf("SSH会话结束: %s@%s", connectMsg.Username, addr)
		session.Close()
		// 会话结束时兜底发送0xFE（如果stdout/stderr都无数据）
		sendDiagOnce()
	}()

	log.Printf("SSH连接诊断: %s (room=%s, connect=%dms)", addr, roomID, tShellDone)

	return &sshSession{dc: dc, sshConn: sshConn, session: session, stdin: stdin}
}

type SFTPRequestMsg struct {
	Type    string `json:"type"`
	Op      string `json:"op"`
	ReqID   string `json:"req_id"`
	Path    string `json:"path"`
	Content string `json:"content,omitempty"`
	OldPath string `json:"old_path,omitempty"`
	NewName string `json:"new_name,omitempty"`
}

type SftpItem struct {
	Name  string `json:"name"`
	Size  int64  `json:"size"`
	IsDir bool   `json:"is_dir"`
	Mode  string `json:"mode"`
	Mtime string `json:"mtime"`
}

func (h *SignalHandler) sendDCSftpResponse(dc *webrtc.DataChannel, reqID, op, path string, items []SftpItem, content interface{}) {
	resp := map[string]interface{}{
		"type":   "sftp",
		"op":     op,
		"req_id": reqID,
		"path":   path,
		"ok":     true,
	}
	if items != nil {
		resp["items"] = items
	}
	if content != nil {
		resp["content"] = content
	}
	data, _ := json.Marshal(resp)
	h.sendDCMsg(dc, MsgSFTPResponse, data)
}

func (h *SignalHandler) sendDCSftpOk(dc *webrtc.DataChannel, reqID, op string) {
	resp := map[string]interface{}{
		"type":   "sftp",
		"op":     op,
		"req_id": reqID,
		"ok":     true,
	}
	data, _ := json.Marshal(resp)
	h.sendDCMsg(dc, MsgSFTPResponse, data)
}

func (h *SignalHandler) sendDCSftpError(dc *webrtc.DataChannel, reqID, detail string) {
	resp := map[string]interface{}{
		"type":   "sftp",
		"req_id": reqID,
		"ok":     false,
		"detail": detail,
	}
	data, _ := json.Marshal(resp)
	h.sendDCMsg(dc, MsgSFTPResponse, data)
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

func (h *SignalHandler) handleSFTPData(dc *webrtc.DataChannel, data []byte, sshConn *gossh.Client) {
	var req SFTPRequestMsg
	if err := json.Unmarshal(data, &req); err != nil {
		h.sendDCSftpError(dc, "", "SFTP请求解析失败: "+err.Error())
		return
	}

	client, err := sftp.NewClient(sshConn)
	if err != nil {
		h.sendDCSftpError(dc, req.ReqID, "SFTP连接失败: "+err.Error())
		return
	}
	defer client.Close()

	switch req.Op {
	case "list":
		entries, err := client.ReadDir(req.Path)
		if err != nil {
			h.sendDCSftpError(dc, req.ReqID, "读取目录失败: "+err.Error())
			return
		}
		items := make([]SftpItem, 0, len(entries))
		for _, e := range entries {
			items = append(items, SftpItem{
				Name:  e.Name(),
				Size:  e.Size(),
				IsDir: e.IsDir(),
				Mode:  e.Mode().String(),
				Mtime: e.ModTime().Format("2006-01-02 15:04:05"),
			})
		}
		h.sendDCSftpResponse(dc, req.ReqID, "list", req.Path, items, nil)

	case "read":
		f, err := client.Open(req.Path)
		if err != nil {
			h.sendDCSftpError(dc, req.ReqID, "读取文件失败: "+err.Error())
			return
		}
		data, err := io.ReadAll(f)
		f.Close()
		if err != nil {
			h.sendDCSftpError(dc, req.ReqID, "读取内容失败: "+err.Error())
			return
		}
		b64 := base64.StdEncoding.EncodeToString(data)
		h.sendDCSftpResponse(dc, req.ReqID, "read", req.Path, nil, b64)

	case "write":
		data, err := base64.StdEncoding.DecodeString(req.Content)
		if err != nil {
			h.sendDCSftpError(dc, req.ReqID, "解码内容失败: "+err.Error())
			return
		}
		f, err := client.Create(req.Path)
		if err != nil {
			h.sendDCSftpError(dc, req.ReqID, "创建文件失败: "+err.Error())
			return
		}
		_, err = f.Write(data)
		f.Close()
		if err != nil {
			h.sendDCSftpError(dc, req.ReqID, "写入文件失败: "+err.Error())
			return
		}
		h.sendDCSftpOk(dc, req.ReqID, "write")

	case "mkdir":
		if err := client.MkdirAll(req.Path); err != nil {
			h.sendDCSftpError(dc, req.ReqID, "创建目录失败: "+err.Error())
			return
		}
		h.sendDCSftpOk(dc, req.ReqID, "mkdir")

	case "delete":
		if err := client.Remove(req.Path); err != nil {
			h.sendDCSftpError(dc, req.ReqID, "删除失败: "+err.Error())
			return
		}
		h.sendDCSftpOk(dc, req.ReqID, "delete")

	case "rename":
		if err := client.Rename(req.OldPath, req.NewName); err != nil {
			h.sendDCSftpError(dc, req.ReqID, "重命名失败: "+err.Error())
			return
		}
		h.sendDCSftpOk(dc, req.ReqID, "rename")

	case "stat":
		_, err := client.Stat(req.Path)
		if err != nil {
			h.sendDCSftpError(dc, req.ReqID, "获取状态失败: "+err.Error())
			return
		}
		h.sendDCSftpOk(dc, req.ReqID, "stat")

	default:
		h.sendDCSftpError(dc, req.ReqID, "未知操作: "+req.Op)
	}
}

// bridgeVNC 桥接 WebRTC DataChannel 到目标 VNC 服务器 (透明 TCP 代理)
// Agent 不做 RFB 解析，让 noVNC 处理完整 RFB 协议
func (h *SignalHandler) bridgeVNC(ctx context.Context, dc *webrtc.DataChannel, roomID string, connectPayload []byte, inputCh chan []byte) {
	defer close(inputCh)

	var connMsg VNCConnectMsg
	if err := json.Unmarshal(connectPayload, &connMsg); err != nil {
		h.sendDCErrorMsg(dc, "VNC连接指令解析失败: "+err.Error())
		return
	}

	addr := fmt.Sprintf("%s:%d", connMsg.Host, connMsg.Port)
	log.Printf("[A-BRIDGE] VNC connect: %s (room=%s)", addr, roomID)

	// TCP connect to VNC server
	vncStart := time.Now()
	tcpConn, err := net.DialTimeout("tcp", addr, 10*time.Second)
	tcpMs := time.Since(vncStart).Milliseconds()
	if err != nil {
		h.reportVNCError(dc, "agent_tcp", "VNC连接失败: "+err.Error(), tcpMs, connMsg.Host, connMsg.Port, roomID)
		h.sendDCErrorMsg(dc, "VNC连接失败: "+err.Error())
		return
	}
	defer tcpConn.Close()
	log.Printf("[A-BRIDGE] VNC TCP connected: %s (room=%s)", addr, roomID)

	// Notify browser that TCP connection is ready
	resultData, _ := json.Marshal(map[string]interface{}{
		"type": "vnc_connect_result",
		"ok":   true,
	})
	h.sendDCMsg(dc, MsgVNCConnect, resultData)

	// 发送VNC诊断消息(0xFE) — 同时通过DC和WS双路发送，确保浏览器收到
	diagMsg, _ := json.Marshal(map[string]interface{}{
		"_type":            "diagnostics",
		"agent_connect_ms": tcpMs,
		"agent_tcp_ms":     tcpMs,
		"agent_ssh_ms":     0,
		"agent_shell_ms":   0,
		"agent_ssh_host":   connMsg.Host,
		"agent_ssh_port":   connMsg.Port,
	})
	if err := h.sendDCMsg(dc, MsgDiagnostics, diagMsg); err != nil {
		log.Printf("VNC DC Send 0xFE failed: room=%s err=%v", roomID, err)
	}
	// 同时通过WS发送，确保gateway模式下浏览器也能收到诊断数据
	h.sendWSDiagnostics(roomID, diagMsg)
	log.Printf("VNC连接诊断: %s (room=%s, tcp=%dms)", addr, roomID, tcpMs)

	// Bidirectional bridge with context cancellation
	done := make(chan struct{})

	// TCP -> DataChannel (VNC server responses to browser)
	go func() {
		defer close(done)
		buf := make([]byte, 64*1024)
		for {
			// 背压：DC 缓冲/待发队列高时暂停读 TCP，让 VNC 服务端降速（防丢帧）
			if !h.waitVCNSendCapacity(ctx, dc) {
				return
			}
			n, err := tcpConn.Read(buf)
			if n > 0 {
				if n <= 80 {
					preview := make([]byte, n)
					copy(preview, buf[:n])
					hexStr := ""
					for _, b := range preview {
						hexStr += fmt.Sprintf("%02x ", b)
					}
					log.Printf("[A-BRIDGE] VNC TCP recv %d bytes: %s (room=%s)", n, hexStr, roomID)
				} else {
					log.Printf("[A-BRIDGE] VNC TCP recv %d bytes (room=%s)", n, roomID)
				}
				if sendErr := h.sendDCMsg(dc, MsgVNCData, buf[:n]); sendErr != nil {
					log.Printf("[A-BRIDGE] VNC DC send failed (room=%s): %v", roomID, sendErr)
					return
				}
			}
			if err != nil {
				if err != io.EOF {
					log.Printf("[A-BRIDGE] VNC TCP read error (room=%s): %v", roomID, err)
				}
				return
			}
		}
	}()

	// DataChannel input -> TCP (browser/noVNC requests to VNC server)
	go func() {
		for {
			select {
			case msg, ok := <-inputCh:
				if !ok {
					return
				}
				if len(msg) > 1 {
					if _, err := tcpConn.Write(msg[1:]); err != nil {
						log.Printf("[A-BRIDGE] VNC TCP write error (room=%s): %v", roomID, err)
						return
					}
				}
			case <-ctx.Done():
				return
			case <-done:
				return
			}
		}
	}()

	// Wait: either TCP read finishes or context is cancelled
	select {
	case <-done:
		// TCP side ended (server closed or error)
	case <-ctx.Done():
		// Browser requested disconnect — close TCP to stop everything
		log.Printf("[A-BRIDGE] VNC context cancelled, closing TCP (room=%s)", roomID)
		tcpConn.Close()
		<-done // wait for goroutines to finish
	}
	log.Printf("[A-BRIDGE] VNC bridge closed (room=%s)", roomID)
}

// waitVCNSendCapacity 背压时暂停读 VNC TCP（true=可继续读；false=ctx 结束/DC 关闭）
func (h *SignalHandler) waitVCNSendCapacity(ctx context.Context, dc *webrtc.DataChannel) bool {
	for {
		if dc.ReadyState() != webrtc.DataChannelStateOpen {
			return false
		}
		st := getDCWriteState(dc)
		st.mu.Lock()
		pending := st.pendingBytes
		st.mu.Unlock()
		// 低水位才继续读，避免持续堆到丢帧阈值
		if pending <= dcPendingMaxBytes/4 && dc.BufferedAmount() <= dcBackpressureThreshold/2 {
			return true
		}
		select {
		case <-ctx.Done():
			return false
		case <-time.After(10 * time.Millisecond):
		}
	}
}

// bridgeTCPTunnel 处理来自隧道Listener的DataChannel，桥接到本地TCP连接
func (h *SignalHandler) bridgeTCPTunnel(dc *webrtc.DataChannel, roomID string) {
	log.Printf("[TUNNEL-BRIDGE] tcp-tunnel DataChannel opened (room=%s)", roomID)

	conns := make(map[uint16]net.Conn)
	var mu sync.Mutex

	dc.OnMessage(func(msg webrtc.DataChannelMessage) {
		data := msg.Data
		if len(data) < 3 {
			return
		}
		prefix := data[0]
		connID := uint16(data[1])<<8 | uint16(data[2])
		payload := data[3:]

		switch prefix {
		case MsgTunnelConnect:
			// Listener请求建立TCP连接
			var req struct {
				Host string `json:"host"`
				Port int    `json:"port"`
			}
			if err := json.Unmarshal(payload, &req); err != nil {
				log.Printf("[TUNNEL-BRIDGE] 解析连接请求失败: %v", err)
				h.sendTunnelConnectOK(dc, connID, false, "解析失败")
				return
			}
			addr := fmt.Sprintf("%s:%d", req.Host, req.Port)
			log.Printf("[TUNNEL-BRIDGE] 建立TCP连接: %s (connID=%d, room=%s)", addr, connID, roomID)

			conn, err := net.DialTimeout("tcp", addr, 10*time.Second)
			if err != nil {
				log.Printf("[TUNNEL-BRIDGE] TCP连接失败: %v (connID=%d)", err, connID)
				h.sendTunnelConnectOK(dc, connID, false, err.Error())
				return
			}

			mu.Lock()
			conns[connID] = conn
			mu.Unlock()

			h.sendTunnelConnectOK(dc, connID, true, "")
			log.Printf("[TUNNEL-BRIDGE] TCP连接成功: %s (connID=%d)", addr, connID)

			// TCP → DataChannel
			go func() {
				defer func() {
					mu.Lock()
					delete(conns, connID)
					mu.Unlock()
					conn.Close()
					h.sendTunnelMsg(dc, MsgTunnelDisconnect, connID, nil)
					log.Printf("[TUNNEL-BRIDGE] TCP连接关闭: %s (connID=%d)", addr, connID)
				}()
				buf := make([]byte, 65536)
				for {
					n, err := conn.Read(buf)
					if n > 0 {
						h.sendTunnelMsg(dc, MsgTunnelData, connID, buf[:n])
					}
					if err != nil {
						return
					}
				}
			}()

		case MsgTunnelData:
			// DataChannel → TCP
			mu.Lock()
			conn, ok := conns[connID]
			mu.Unlock()
			if ok && len(payload) > 0 {
				if _, err := conn.Write(payload); err != nil {
					log.Printf("[TUNNEL-BRIDGE] TCP写入失败: %v (connID=%d)", err, connID)
				}
			}

		case MsgTunnelDisconnect:
			// Listener关闭TCP连接
			mu.Lock()
			conn, ok := conns[connID]
			if ok {
				delete(conns, connID)
			}
			mu.Unlock()
			if ok {
				conn.Close()
				log.Printf("[TUNNEL-BRIDGE] 收到断开通知，关闭TCP (connID=%d)", connID)
			}
		}
	})

	dc.OnClose(func() {
		log.Printf("[TUNNEL-BRIDGE] DataChannel关闭 (room=%s)", roomID)
		mu.Lock()
		for connID, conn := range conns {
			conn.Close()
			delete(conns, connID)
		}
		mu.Unlock()
	})
}

func (h *SignalHandler) sendTunnelConnectOK(dc *webrtc.DataChannel, connID uint16, ok bool, detail string) {
	payload, _ := json.Marshal(map[string]interface{}{"ok": ok, "detail": detail})
	h.sendTunnelMsg(dc, MsgTunnelConnectOK, connID, payload)
}

func (h *SignalHandler) sendTunnelMsg(dc *webrtc.DataChannel, prefix byte, connID uint16, payload []byte) {
	msg := make([]byte, 3+len(payload))
	msg[0] = prefix
	msg[1] = byte(connID >> 8)
	msg[2] = byte(connID)
	copy(msg[3:], payload)
	dc.Send(msg)
}
