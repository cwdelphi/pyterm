package webrtc

import (
	"context"
	"crypto/sha256"
	"encoding/binary"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log"
	"math"
	"net"
	"net/http"
	"os"
	"os/exec"
	"runtime"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/creack/pty"
	"github.com/pion/webrtc/v4"
	"github.com/pkg/sftp"
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

// sendSpeedtestSignal 插件 Deps.SendSignal: 完整 JSON(含 type/room_id)拆装 ws.Message 上报
func (h *SignalHandler) sendSpeedtestSignal(payload []byte) error {
	var probe struct {
		Type   string `json:"type"`
		RoomID string `json:"room_id"`
	}
	_ = json.Unmarshal(payload, &probe)
	if probe.Type == "" {
		return fmt.Errorf("speedtest payload missing type")
	}
	return h.client.Send(&ws.Message{Type: probe.Type, RoomID: probe.RoomID, Data: payload})
}

// sendSpeedtestError 向服务端/浏览器回报测速错误
func (h *SignalHandler) sendSpeedtestError(roomID, detail string) {
	raw, _ := json.Marshal(map[string]any{"type": "speedtest_error", "room_id": roomID, "detail": detail})
	_ = h.sendSpeedtestSignal(raw)
}

// speedtestPing 记录 pong 往返延迟(ms)
type speedtestPing struct {
	mu   sync.Mutex
	rtts []float64
}

// runPingPhase 源端 ping 阶段: 发 N 个 ping 帧, 收集 pong RTT
func (h *SignalHandler) runPingPhase(dc *webrtc.DataChannel, cancel chan struct{}, ping *speedtestPing) (pingMs, jitterMs, lossPct float64) {
	const n = 8
	for i := int64(0); i < n; i++ {
		ts := time.Now().UnixNano()
		raw, _ := json.Marshal(map[string]any{"t": "ping", "seq": i, "ts": ts})
		if err := dc.Send(append([]byte{0x60}, raw...)); err != nil {
			break
		}
		select {
		case <-cancel:
			return 0, 0, 100
		case <-time.After(55 * time.Millisecond):
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

// reportSpeedtestPing 上报 ping 结果(前端展示 + md 合并进历史)
func (h *SignalHandler) reportSpeedtestPing(roomID string, pingMs, jitterMs, lossPct float64) {
	raw, _ := json.Marshal(map[string]any{
		"type": "speedtest_ping", "room_id": roomID,
		"ping_ms": pingMs, "jitter_ms": jitterMs, "loss_pct": lossPct,
	})
	if err := h.sendSpeedtestSignal(raw); err != nil {
		log.Printf("[SPEEDTEST] ping 上报失败: %v", err)
	}
}

// startSpeedtest 发起端编排: relay-only Peer + DC "speedtest" + 阶段1发送(阶段S)
func (h *SignalHandler) startSpeedtest(msg *ws.Message) {
	var req struct {
		RoomID   string `json:"room_id"`
		Duration int    `json:"duration"`
		MBPS     int    `json:"mbps_limit"`
	}
	if err := json.Unmarshal(msg.Data, &req); err != nil || req.RoomID == "" {
		log.Printf("[SPEEDTEST] 指令解析失败: %v", err)
		return
	}
	if req.Duration <= 0 {
		req.Duration = 10
	}

	serverICE := h.client.GetICEServers()
	if len(serverICE) == 0 {
		// relay 强制: 无服务端 ICE/TURN → 明确报错(md 侧已预检, 此处兜底)
		h.sendSpeedtestError(req.RoomID, "需配置Coturn")
		log.Printf("[SPEEDTEST] 无服务端TURN配置, 中止 room=%s", req.RoomID)
		return
	}
	var iceServers []webrtc.ICEServer
	for _, st := range serverICE {
		iceServers = append(iceServers, webrtc.ICEServer{
			URLs:       st.URLs,
			Username:   st.Username,
			Credential: st.Credential,
		})
	}
	config := webrtc.Configuration{
		ICEServers:         iceServers,
		ICETransportPolicy: webrtc.ICETransportPolicyRelay,
	}
	peer, err := NewPeer(h.client.AgentID(), config)
	if err != nil {
		h.sendSpeedtestError(req.RoomID, err.Error())
		return
	}

	h.mu.Lock()
	if oldPeer, exists := h.p[req.RoomID]; exists {
		// 同 handleOffer：旧 Peer Close 可能等 gather 收尾（STUN/TURN 不可达时阻塞），持锁异步关
		go oldPeer.Close()
	}
	h.p[req.RoomID] = peer
	cancel := make(chan struct{})
	if oldCancel, exists := h.speedtestCancels[req.RoomID]; exists {
		close(oldCancel)
	}
	h.speedtestCancels[req.RoomID] = cancel
	h.mu.Unlock()

	peer.OnICECandidate(func(candidate *webrtc.ICECandidate) {
		if candidate == nil {
			return
		}
		candidateJSON := candidate.ToJSON()
		h.sendCandidate(&candidateJSON, req.RoomID)
	})

	dc, err := peer.CreateDataChannel("speedtest", true)
	if err != nil {
		h.sendSpeedtestError(req.RoomID, err.Error())
		h.cleanupSpeedtestRoom(req.RoomID, peer, cancel)
		return
	}
	var recvBytes uint64
	var recvMu sync.Mutex
	ping := &speedtestPing{}
	dc.OnMessage(func(m webrtc.DataChannelMessage) {
		if len(m.Data) == 0 {
			return
		}
		switch m.Data[0] {
		case 0x61:
			recvMu.Lock()
			recvBytes += uint64(len(m.Data) - 1)
			recvMu.Unlock()
		case 0x60:
			var pr struct {
				T  string `json:"t"`
				TS int64  `json:"ts"`
			}
			if json.Unmarshal(m.Data[1:], &pr) == nil && pr.T == "pong" && pr.TS > 0 {
				rtt := float64(time.Now().UnixNano()-pr.TS) / 1e6
				ping.mu.Lock()
				ping.rtts = append(ping.rtts, rtt)
				ping.mu.Unlock()
			}
		}
	})
	dc.OnOpen(func() {
		log.Printf("[SPEEDTEST] DataChannel 已打开, 开始测速 room=%s limit=%dMbps", req.RoomID, req.MBPS)
		h.runSpeedtestSource(dc, peer, cancel, &recvMu, &recvBytes, ping, req.RoomID, req.Duration, req.MBPS)
	})

	offer, err := peer.CreateOffer()
	if err != nil {
		h.sendSpeedtestError(req.RoomID, err.Error())
		h.cleanupSpeedtestRoom(req.RoomID, peer, cancel)
		return
	}
	h.sendOffer(offer, req.RoomID)
	log.Printf("[SPEEDTEST] Offer已发送 room=%s (relay-only)", req.RoomID)
}

// runSpeedtestSource 阶段1 上行发送 → 通知阶段2 → 等待+收尾
func (h *SignalHandler) runSpeedtestSource(dc *webrtc.DataChannel, peer *Peer, cancel chan struct{},
	recvMu *sync.Mutex, recvBytes *uint64, ping *speedtestPing, roomID string, duration, mbps int) {
	defer h.cleanupSpeedtestRoom(roomID, peer, cancel)

	sendCtrl := func(t string, dur, limit int) {
		raw, _ := json.Marshal(map[string]any{"t": t, "room": roomID, "duration": dur, "mbps": limit})
		if err := dc.Send(append([]byte{0x60}, raw...)); err != nil {
			log.Printf("[SPEEDTEST] ctrl %s 发送失败: %v", t, err)
		}
	}

	sendCtrl("start", duration, mbps)
	// 阶段0: PING 往返延迟/抖动/丢包(浏览器与历史展示)
	pingMs, jitterMs, lossPct := h.runPingPhase(dc, cancel, ping)
	h.reportSpeedtestPing(roomID, pingMs, jitterMs, lossPct)
	var sentBytes uint64
	speedtestSendLoop(dc, duration, mbps, cancel, &sentBytes)
	sendCtrl("down", duration, mbps)

	// 阶段2: 接收目标发送的下行数据(硬时限+1s grace 等 result 经信令上报)
	deadline := time.After(time.Duration(duration)*time.Second + time.Second)
	for {
		select {
		case <-cancel:
			log.Printf("[SPEEDTEST] 已取消 room=%s", roomID)
			return
		case <-deadline:
			recvMu.Lock()
			downBytes := *recvBytes
			recvMu.Unlock()
			log.Printf("[SPEEDTEST] 发起端收尾 room=%s 阶段2收到=%dB", roomID, downBytes)
			// 兜底: 目标端未上报 result(会话仍活跃)时, 源端以自身收发字节补发 result, 保证浏览器必然收到终止信号
			upMbps := float64(sentBytes) * 8 / float64(duration) / 1e6
			downMbps := float64(downBytes) * 8 / float64(duration) / 1e6
			raw, _ := json.Marshal(map[string]any{
				"type": "speedtest_result", "room_id": roomID,
				"up_mbps":         math.Round(upMbps*100) / 100,
				"down_mbps":       math.Round(downMbps*100) / 100,
				"up_bytes":        sentBytes,
				"down_bytes":      downBytes,
				"duration":        duration,
				"source_fallback": true,
			})
			if err := h.sendSpeedtestSignal(raw); err != nil {
				log.Printf("[SPEEDTEST] 源端兜底 result 上报失败: %v", err)
			} else {
				log.Printf("[SPEEDTEST] 源端兜底 result 上报 room=%s up=%.1f down=%.1f", roomID, upMbps, downMbps)
			}
			return
		}
	}
}

// speedtestSendLoop 阶段1 上行发送: 限速档 + 背压 + 硬时限
func speedtestSendLoop(dc *webrtc.DataChannel, duration, mbps int, stop <-chan struct{}, sent *uint64) {
	deadline := time.Now().Add(time.Duration(duration) * time.Second)
	frame := make([]byte, 1+16*1024)
	frame[0] = 0x61
	for time.Now().Before(deadline) {
		select {
		case <-stop:
			return
		default:
		}
		for dc.BufferedAmount() > 64*1024 && time.Now().Before(deadline) {
			select {
			case <-stop:
				return
			case <-time.After(10 * time.Millisecond):
			}
		}
		if err := dc.Send(frame); err != nil {
			log.Printf("[SPEEDTEST] 阶段1发送失败: %v", err)
			return
		}
		if sent != nil {
			*sent += uint64(len(frame) - 1)
		}
		if mbps > 0 {
			interval := time.Duration(float64(len(frame)) / (float64(mbps) * 1e6 / 8) * float64(time.Second))
			if interval > 0 {
				select {
				case <-stop:
					return
				case <-time.After(interval):
				}
			}
		}
	}
}

// cleanupSpeedtestRoom 关闭 Peer 并清理房间注册与取消信号(幂等)
func (h *SignalHandler) cleanupSpeedtestRoom(roomID string, peer *Peer, cancel chan struct{}) {
	// gather 卡住时 Close 会等 STUN/TURN 收尾（实测可达 ~105s）→ 异步关，避免堵死调用方
	go peer.Close()
	h.mu.Lock()
	if h.p[roomID] == peer {
		delete(h.p, roomID)
	}
	if c, ok := h.speedtestCancels[roomID]; ok && c == cancel {
		delete(h.speedtestCancels, roomID)
	}
	h.mu.Unlock()
}

// stopSpeedtest speedtest_stop: 关发送循环/关 Peer/复位插件会话
func (h *SignalHandler) stopSpeedtest(roomID string) {
	h.mu.Lock()
	if c, ok := h.speedtestCancels[roomID]; ok {
		close(c)
		delete(h.speedtestCancels, roomID)
	}
	peer, hasPeer := h.p[roomID]
	delete(h.p, roomID)
	h.mu.Unlock()
	if hasPeer {
		go peer.Close()
	}
	if h.speedtestPlugin != nil {
		h.speedtestPlugin.Reset()
	}
}

// attachSpeedtestDC 接收端 DC 挂载: 帧分发给 speedtest 插件(同步, 防首帧丢失)
func (h *SignalHandler) attachSpeedtestDC(dc *webrtc.DataChannel, roomID string) {
	log.Printf("[SPEEDTEST] 接收端DataChannel打开 (room=%s)", roomID)
	mgr := h.getPluginManager()
	if h.speedtestPlugin != nil {
		h.speedtestPlugin.AttachSender(roomID,
			func(b []byte) error { return dc.Send(b) },
			func() uint64 { return dc.BufferedAmount() })
	}
	dc.OnMessage(func(m webrtc.DataChannelMessage) {
		if len(m.Data) == 0 {
			return
		}
		switch m.Data[0] {
		case 0x60:
			mgr.Dispatch("speedtest", "ctrl", m.Data[1:])
		case 0x61:
			mgr.Dispatch("speedtest", "data", m.Data[1:])
		}
	})
	dc.OnClose(func() {
		if h.speedtestPlugin != nil {
			h.speedtestPlugin.Detach(roomID)
		}
	})
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
		log.Printf("[ICE-DIAG] local candidate room=%s: %s", roomID, candidateJSON.Candidate)
		h.sendCandidate(&candidateJSON, roomID)
	})

	// 创建 DataChannel "tcp-tunnel"
	dc, err := peer.CreateDataChannel("tcp-tunnel", true)
	if err != nil {
		log.Printf("[TUNNEL] 创建DataChannel失败: %v", err)
		go peer.Close()
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
		go peer.Close()
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

	h.mu.Lock()
	oldPeer := h.p[roomID]
	h.p[roomID] = peer
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

type SSHConnectMsg struct {
	Type     string `json:"type"`
	Host     string `json:"host"`
	Port     int    `json:"port"`
	Username string `json:"username"`
	AuthType string `json:"auth_type"`
	Password string `json:"password"`
	Cols     int    `json:"cols"`
	Rows     int    `json:"rows"`
	Mode     string `json:"mode"` // "local"=webterm 本地shell(免SSH服务)
}

type ResizeMsg struct {
	Cols int `json:"cols"`
	Rows int `json:"rows"`
}

// sshSession 保存 SSH 会话状态(webterm: sshConn/session 为 nil, pty/cmd 生效)
type sshSession struct {
	dc      *webrtc.DataChannel
	sshConn *gossh.Client
	session *gossh.Session
	stdin   interface{ Write([]byte) (int, error) }
	pty     *os.File  // webterm 本地 PTY(creack/pty)
	cmd     *exec.Cmd // webterm shell 进程
}

// vncSession 保存 VNC 桥接状态
type vncSession struct {
	cancel  context.CancelFunc
	inputCh chan []byte
}

const dcBackpressureThreshold = 64 * 1024 // 64KB
const dcPendingMaxBytes = 256 * 1024      // P1: 背压待发队列上限 256KB
// SFTP 出站背压上限：BufferedAmount 是"已写入未被对端确认"的字节（ack 口径），
// 硬卡 64KB 会把 WAN 吞吐压到 64KB/RTT，故取 256KB（LAN 无感，WAN 256KB/RTT
// ≈ 8.5MB/s@30ms）；关键收益是杜绝 427 片 30ms 内灌出 ~10MB 的病态队列。
const dcSFTPMaxOutstanding = 256 * 1024

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

	// SFTP 响应 (0x11)：批量顺序分片必须背压直发——不进共享 pending 队列
	// （各前缀在前端独立解复用，跨前缀顺序无关，也避免与 VNC/终端丢帧逻辑纠缠）。
	// 实测 427 片在 ~30ms 内全部 dc.Send 会让 BufferedAmount 冲到 ~10MB，
	// 一旦对端 rwnd/UDP 缓冲跟不上即停摆：浏览器收到 5–9MB 后 60s 等不到收尾，
	// 且积压会拖累同连接后续传输（第 2、3 次下载只收到十几帧）。
	if prefix == MsgSFTPResponse {
		deadline := time.Now().Add(30 * time.Second)
		for dc.BufferedAmount() > dcSFTPMaxOutstanding {
			if dc.ReadyState() != webrtc.DataChannelStateOpen {
				return fmt.Errorf("datachannel closed")
			}
			if time.Now().After(deadline) {
				return fmt.Errorf("send backpressure timeout (buffered=%d)", dc.BufferedAmount())
			}
			time.Sleep(500 * time.Microsecond)
		}
		var err error
		for attempt := 0; attempt < 4; attempt++ {
			if dc.ReadyState() != webrtc.DataChannelStateOpen {
				return fmt.Errorf("datachannel closed")
			}
			h.dcSendMu.Lock()
			err = dc.Send(msg)
			h.dcSendMu.Unlock()
			if err == nil {
				return nil
			}
			// DC 仍 Open 的失败按瞬时处理（如缓冲满），退避后重试；
			// 真正关闭时 ensureOpen 每轮都会命中，快速上抛
			time.Sleep(time.Duration(attempt+1) * 5 * time.Millisecond)
		}
		log.Printf("[A-BRIDGE] DC send error prefix=0x%02x len=%d: %v", prefix, len(data), err)
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
				// 数据面 per-frame 日志：级别判断前移到构造 hexStr 之前，
				// Info 级（默认）下零开销 —— 鼠标事件每秒可达上百条
				if logger.Enabled(logger.LevelDebug) {
					if len(data) <= 80 {
						hexStr := ""
						for _, b := range data {
							hexStr += fmt.Sprintf("%02x ", b)
						}
						logger.Debugf("[A-BRIDGE] VNC input %d bytes: %s (room=%s)", len(data), hexStr, roomID)
					} else {
						logger.Debugf("[A-BRIDGE] VNC input %d bytes (room=%s)", len(data), roomID)
					}
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
			// 已有会话时收到新连接指令 → 关闭旧会话后重建 (R6 幂等重连, SSH/webterm 通用)
			closeSSHSession(sess)
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
			if json.Unmarshal(payload, &resize) != nil {
				break
			}
			if sess.session != nil {
				if err := sess.session.WindowChange(resize.Rows, resize.Cols); err != nil {
					log.Printf("WindowChange failed room=%s: %v", roomID, err)
				}
			} else if sess.pty != nil {
				// webterm: TIOCSWINSZ 直改本地 PTY
				if err := pty.Setsize(sess.pty, &pty.Winsize{Rows: uint16(resize.Rows), Cols: uint16(resize.Cols)}); err != nil {
					log.Printf("[WEBTERM] Setsize failed room=%s: %v", roomID, err)
				}
			}
		case MsgSFTPRequest:
			if len(payload) >= 1 {
				// 上传分片：同步入缓冲（与请求 goroutine 并发时分片可能晚于请求落盘）
				if payload[0] == sftpSubUpload {
					if reqID, idx, cnt, raw, ok := parseSftpUploadFrame(payload); ok {
						storeSFTPUpload(reqID, idx, cnt, raw)
					}
					break
				}
				if sess.sshConn != nil {
					sshConnRef := sess.sshConn
					go h.handleSFTPData(dc, payload, sshConnRef)
				}
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
			closeSSHSession(sess)
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
	// webterm: mode=local → Agent 本地 shell, 不拨号 SSH(内嵌SSH服务已移除)
	if connectMsg.Mode == "local" {
		return h.connectLocal(dc, connectMsg, roomID)
	}
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

// ── DC 消息分片 ──────────────────────────────────────────────
// WebRTC DataChannel 单条消息上限 65536 字节（pion 与浏览器 SCTP 一致），
// 超过即被 pion 拒绝（outbound packet larger than maximum message size），
// 因此 SFTP 读写响应/请求超过阈值时按分片发送，接收端按 req_id 重组。
const dcSFTPSingleLimit = 32 * 1024 // 小于该值直接单条发送
const dcSFTPChunkSize = 24 * 1024   // 分片原始字节数（base64 后 32KB + 信封 < 64KB）

// T1.3: SFTP 二进制帧（编号暂沿 0x10/0x11，子类型首字节区分）
// 0x10 请求: [sub=CMD][op:1][reqId:4BE][metaLen:2BE][meta JSON]
//
//	[sub=UPLOAD][reqId:4BE][idx:2BE][cnt:2BE][raw]
//
// 0x11 响应: [sub=META][reqId:4BE][metaLen:2BE][meta JSON]
//
//	[sub=CHUNK][reqId:4BE][idx:2BE][cnt:2BE][raw]
const (
	sftpSubCmd    byte = 0x00
	sftpSubUpload byte = 0x01
)
const (
	sftpSubMeta  byte = 0x00
	sftpSubChunk byte = 0x01
)
const (
	sftpOpList   byte = 0x01
	sftpOpRead   byte = 0x02
	sftpOpWrite  byte = 0x03
	sftpOpMkdir  byte = 0x04
	sftpOpRename byte = 0x05
	sftpOpDelete byte = 0x06
	sftpOpStat   byte = 0x07
)

// 上传分片重组缓冲：uint32 reqId
var (
	sftpUploadMu    sync.Mutex
	sftpUploadBuf   = map[uint32]map[uint16][]byte{}
	sftpUploadN     = map[uint32]uint16{}
	sftpUploadSeen  = map[uint32]time.Time{}
	sftpUploadBytes int64 // 当前重组缓冲总字节（sftpUploadMu 保护）
)

// dropSFTPUploadLocked 立即回收一次上传的全部分片（调用方须已持 sftpUploadMu）
func dropSFTPUploadLocked(reqID uint32) {
	if parts, ok := sftpUploadBuf[reqID]; ok {
		for _, b := range parts {
			sftpUploadBytes -= int64(len(b))
		}
	}
	delete(sftpUploadBuf, reqID)
	delete(sftpUploadN, reqID)
	delete(sftpUploadSeen, reqID)
	if sftpUploadBytes < 0 {
		sftpUploadBytes = 0
	}
}

// sftpUploadTTL 重组缓冲的存活时间（超时未完成的上传即视为已放弃）
const sftpUploadTTL = 60 * time.Second

// sftpUploadSweeper 重组缓冲的独立清扫器。
// 原实现只在“下一分片到达时”顺带扫 TTL：一旦上传中途放弃（断连/取消），
// 就再也不会有新分片，整份文件体积的分片永久驻留 —— 这是确定性的内存泄露。
var sftpUploadSweeperOnce sync.Once

func ensureSFTPUploadSweeper() {
	sftpUploadSweeperOnce.Do(func() {
		go func() {
			t := time.NewTicker(15 * time.Second)
			defer t.Stop()
			for range t.C {
				sweepSFTPUploads(time.Now())
			}
		}()
	})
}

func sweepSFTPUploads(now time.Time) (removed int) {
	sftpUploadMu.Lock()
	defer sftpUploadMu.Unlock()
	return sweepSFTPUploadsLocked(now)
}

func sweepSFTPUploadsLocked(now time.Time) (removed int) {
	for id, ts := range sftpUploadSeen {
		if now.Sub(ts) > sftpUploadTTL {
			dropSFTPUploadLocked(id)
			removed++
		}
	}
	return removed
}

// sftpUploadMaxBytes 全局重组字节上限（软上限）：超限时先强制清扫，
// 仍超则拒绝本分片 —— 让该次上传以“分片不完整”失败，而不是把进程内存打爆。
const sftpUploadMaxBytes = 256 << 20

// storeSFTPUpload 存储上传分片；DC 有序，分片必先于 CMD 到达
func storeSFTPUpload(reqID uint32, idx, cnt uint16, raw []byte) {
	if cnt <= 0 || idx >= cnt {
		return
	}
	ensureSFTPUploadSweeper()
	now := time.Now()
	sftpUploadMu.Lock()
	defer sftpUploadMu.Unlock()
	for id, ts := range sftpUploadSeen {
		if now.Sub(ts) > sftpUploadTTL {
			dropSFTPUploadLocked(id)
		}
	}
	if sftpUploadBytes+int64(len(raw)) > sftpUploadMaxBytes {
		if removed := sweepSFTPUploadsLocked(now); removed > 0 {
			log.Printf("[SFTP] 重组缓冲超限, 清理 %d 个过期上传", removed)
		}
		if sftpUploadBytes+int64(len(raw)) > sftpUploadMaxBytes {
			log.Printf("[SFTP] 重组缓冲仍超 %d 字节, 拒绝分片 reqID=%d idx=%d", sftpUploadBytes, reqID, idx)
			return
		}
	}
	parts, ok := sftpUploadBuf[reqID]
	if !ok {
		parts = map[uint16][]byte{}
		sftpUploadBuf[reqID] = parts
		sftpUploadN[reqID] = cnt
	}
	if _, dup := parts[idx]; dup {
		return
	}
	parts[idx] = raw
	sftpUploadSeen[reqID] = now
	sftpUploadBytes += int64(len(raw))
}

// takeSFTPUpload 取出并拼装完整上传内容
func takeSFTPUpload(reqID uint32, cnt uint16) ([]byte, bool) {
	sftpUploadMu.Lock()
	defer sftpUploadMu.Unlock()
	// 分片不全时只返回 false、不回收：重组的最终回收由 TTL 清扫器负责
	// （独立 15s ticker，不依赖“下一分片到达”），避免中途 take 误删有效数据。
	parts, ok := sftpUploadBuf[reqID]
	if !ok || sftpUploadN[reqID] != cnt || len(parts) != int(cnt) {
		return nil, false
	}
	var total int
	for i := uint16(0); i < cnt; i++ {
		p, ok2 := parts[i]
		if !ok2 {
			return nil, false
		}
		total += len(p)
	}
	out := make([]byte, 0, total)
	for i := uint16(0); i < cnt; i++ {
		out = append(out, parts[i]...)
	}
	dropSFTPUploadLocked(reqID)
	return out, true
}

// ── SFTP 二进制帧编解码（本文件内单一事实源，单测见 sftp_frame_test.go）──
// web 侧同构实现见 web/src/utils/frame.ts；两侧以
// web/src/__tests__/fixtures/sftp-vectors.json 逐字节交叉校验。

// parseSftpCmdFrame 解析 0x10 CMD 请求帧体：[sub][op:1][reqId:4BE][metaLen:2BE][meta JSON]。
// 出错时一并返回已解析出的 reqID（帧过短/子类型错误时为 0），供上层回错误帧。
func parseSftpCmdFrame(payload []byte) (op byte, reqID uint32, meta []byte, err error) {
	if len(payload) < 1+1+4+2 {
		return 0, 0, nil, errors.New("SFTP请求帧过短")
	}
	if payload[0] != sftpSubCmd {
		return 0, 0, nil, errors.New("SFTP请求帧子类型错误")
	}
	op = payload[1]
	reqID = binary.BigEndian.Uint32(payload[2:])
	metaLen := int(binary.BigEndian.Uint16(payload[6:]))
	if 8+metaLen > len(payload) {
		return op, reqID, nil, errors.New("SFTP请求帧 meta 越界")
	}
	return op, reqID, payload[8 : 8+metaLen], nil
}

// parseSftpUploadFrame 解析 0x10 UPLOAD 分片帧体：[sub][reqId:4BE][idx:2BE][cnt:2BE][raw]
func parseSftpUploadFrame(payload []byte) (reqID uint32, idx, cnt uint16, raw []byte, ok bool) {
	if len(payload) < 1+4+2+2 || payload[0] != sftpSubUpload {
		return 0, 0, 0, nil, false
	}
	return binary.BigEndian.Uint32(payload[1:]),
		binary.BigEndian.Uint16(payload[5:]),
		binary.BigEndian.Uint16(payload[7:]),
		payload[9:], true
}

// buildSftpMetaFrame 构造 0x11 META 响应帧体：[sub][reqId:4BE][metaLen:2BE][meta JSON]
func buildSftpMetaFrame(reqID uint32, meta []byte) []byte {
	frame := make([]byte, 1+4+2+len(meta))
	frame[0] = sftpSubMeta
	binary.BigEndian.PutUint32(frame[1:], reqID)
	binary.BigEndian.PutUint16(frame[5:], uint16(len(meta)))
	copy(frame[7:], meta)
	return frame
}

// buildSftpChunkFrame 构造 0x11 CHUNK 响应帧体：[sub][reqId:4BE][idx:2BE][cnt:2BE][raw]
func buildSftpChunkFrame(reqID uint32, idx, cnt uint16, raw []byte) []byte {
	frame := make([]byte, 1+4+2+2+len(raw))
	frame[0] = sftpSubChunk
	binary.BigEndian.PutUint32(frame[1:], reqID)
	binary.BigEndian.PutUint16(frame[5:], idx)
	binary.BigEndian.PutUint16(frame[7:], cnt)
	copy(frame[9:], raw)
	return frame
}

// sendSftpMeta 发送 META 响应帧（0x11）
func (h *SignalHandler) sendSftpMeta(dc *webrtc.DataChannel, reqID uint32, meta []byte) error {
	return h.sendDCMsg(dc, MsgSFTPResponse, buildSftpMetaFrame(reqID, meta))
}

// sendSftpChunk 发送 CHUNK 响应帧（0x11）
func (h *SignalHandler) sendSftpChunk(dc *webrtc.DataChannel, reqID uint32, idx, cnt uint16, raw []byte) error {
	return h.sendDCMsg(dc, MsgSFTPResponse, buildSftpChunkFrame(reqID, idx, cnt, raw))
}

// sendSftpResponse 发送响应：content==nil → 单 META（超限按 CHUNK 分片）；否则 CHUNK+收尾 META
func (h *SignalHandler) sendSftpResponse(dc *webrtc.DataChannel, reqID uint32, resp map[string]interface{}, content []byte) {
	if content == nil {
		meta, _ := json.Marshal(resp)
		if len(meta) > dcSFTPSingleLimit {
			h.sendChunkedMeta(dc, reqID, meta)
			return
		}
		h.sendSftpMeta(dc, reqID, meta)
		return
	}
	chunks := splitSftpChunks(content)
	cnt := uint16(len(chunks))
	start := time.Now()
	for i, part := range chunks {
		if err := h.sendSftpChunk(dc, reqID, uint16(i), cnt, part); err != nil {
			log.Printf("[A-BRIDGE] SFTP chunk %d/%d send error req=%d: %v", i+1, cnt, reqID, err)
			// 发错误收尾 META：前端 finishSftp 见 ok:false 立即 reject，
			// 否则会等满 30s 超时（探针视角是 60s 无下载事件的"挂死"）
			resp["ok"] = false
			resp["detail"] = fmt.Sprintf("传输中断于 %d/%d 片: %v", i+1, cnt, err)
			if m, e := json.Marshal(resp); e == nil {
				h.sendSftpMeta(dc, reqID, m)
			}
			return
		}
	}
	resp["chunks"] = int(cnt)
	meta, _ := json.Marshal(resp)
	h.sendSftpMeta(dc, reqID, meta)
	log.Printf("[A-BRIDGE] SFTP content sent req=%d size=%d chunks=%d elapsed=%dms buffered=%d",
		reqID, len(content), cnt, time.Since(start).Milliseconds(), dc.BufferedAmount())
}

// splitSftpChunks 按 dcSFTPChunkSize 顺序切分，返回与发送序号一一对应的分片。
// 空输入返回空序列（对应 cnt=0，调用方不发任何 CHUNK）。单测见 sftp_frame_test.go。
func splitSftpChunks(buf []byte) [][]byte {
	if len(buf) == 0 {
		return nil
	}
	cnt := (len(buf) + dcSFTPChunkSize - 1) / dcSFTPChunkSize
	out := make([][]byte, 0, cnt)
	for i := 0; i < cnt; i++ {
		s := i * dcSFTPChunkSize
		e := s + dcSFTPChunkSize
		if e > len(buf) {
			e = len(buf)
		}
		out = append(out, buf[s:e])
	}
	return out
}

// sendChunkedMeta 超大 META（罕见，如巨型目录列表）：meta 字节按 CHUNK 发送 + 收尾 META 带 chunks
func (h *SignalHandler) sendChunkedMeta(dc *webrtc.DataChannel, reqID uint32, meta []byte) {
	chunks := splitSftpChunks(meta)
	cnt := uint16(len(chunks))
	for i, part := range chunks {
		if err := h.sendSftpChunk(dc, reqID, uint16(i), cnt, part); err != nil {
			log.Printf("[A-BRIDGE] SFTP meta chunk send error req=%d: %v", reqID, err)
			// 同 sendSftpResponse：错误收尾 META 防前端挂等到超时
			if m, e := json.Marshal(map[string]interface{}{
				"type": "sftp", "ok": false,
				"detail": fmt.Sprintf("目录列表传输中断于 %d/%d 片: %v", i+1, cnt, err),
			}); e == nil {
				h.sendSftpMeta(dc, reqID, m)
			}
			return
		}
	}
	finalMeta, _ := json.Marshal(map[string]interface{}{"ok": true, "chunks": int(cnt)})
	h.sendSftpMeta(dc, reqID, finalMeta)
}

type SftpItem struct {
	Name  string `json:"name"`
	Size  int64  `json:"size"`
	IsDir bool   `json:"is_dir"`
	Mode  string `json:"mode"`
	Mtime string `json:"mtime"`
}

func (h *SignalHandler) sendDCSftpResponse(dc *webrtc.DataChannel, reqID uint32, op, path string, items []SftpItem, content []byte) {
	resp := map[string]interface{}{
		"type": "sftp",
		"op":   op,
		"path": path,
		"ok":   true,
	}
	if items != nil {
		resp["items"] = items
	}
	h.sendSftpResponse(dc, reqID, resp, content)
}

func (h *SignalHandler) sendDCSftpOk(dc *webrtc.DataChannel, reqID uint32, op string) {
	resp := map[string]interface{}{
		"type": "sftp",
		"op":   op,
		"ok":   true,
	}
	h.sendSftpResponse(dc, reqID, resp, nil)
}

func (h *SignalHandler) sendDCSftpError(dc *webrtc.DataChannel, reqID uint32, detail string) {
	resp := map[string]interface{}{
		"type":   "sftp",
		"ok":     false,
		"detail": detail,
	}
	h.sendSftpResponse(dc, reqID, resp, nil)
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

func (h *SignalHandler) handleSFTPData(dc *webrtc.DataChannel, payload []byte, sshConn *gossh.Client) {
	op, reqID, meta, err := parseSftpCmdFrame(payload)
	if err != nil {
		h.sendDCSftpError(dc, reqID, err.Error())
		return
	}

	var req struct {
		Path    string `json:"path"`
		OldPath string `json:"old_path"`
		NewName string `json:"new_name"`
		Chunks  int    `json:"chunks"`
	}
	if err := json.Unmarshal(meta, &req); err != nil {
		h.sendDCSftpError(dc, reqID, "SFTP请求meta解析失败: "+err.Error())
		return
	}

	// 写入内容：上传分片（[sub=UPLOAD]）已先于 CMD 到达，按 reqID 重组
	var content []byte
	if op == sftpOpWrite && req.Chunks > 0 {
		raw, ok := takeSFTPUpload(reqID, uint16(req.Chunks))
		if !ok {
			h.sendDCSftpError(dc, reqID, fmt.Sprintf("上传分片不完整 (期望 %d 片)", req.Chunks))
			return
		}
		content = raw
	}

	client, err := sftp.NewClient(sshConn)
	if err != nil {
		h.sendDCSftpError(dc, reqID, "SFTP连接失败: "+err.Error())
		return
	}
	defer client.Close()

	switch op {
	case sftpOpList:
		entries, err := client.ReadDir(req.Path)
		if err != nil {
			h.sendDCSftpError(dc, reqID, "读取目录失败: "+err.Error())
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
		h.sendDCSftpResponse(dc, reqID, "list", req.Path, items, nil)

	case sftpOpRead:
		f, err := client.Open(req.Path)
		if err != nil {
			h.sendDCSftpError(dc, reqID, "读取文件失败: "+err.Error())
			return
		}
		data, err := io.ReadAll(f)
		f.Close()
		if err != nil {
			h.sendDCSftpError(dc, reqID, "读取内容失败: "+err.Error())
			return
		}
		h.sendDCSftpResponse(dc, reqID, "read", req.Path, nil, data)

	case sftpOpWrite:
		f, err := client.Create(req.Path)
		if err != nil {
			h.sendDCSftpError(dc, reqID, "创建文件失败: "+err.Error())
			return
		}
		_, err = f.Write(content)
		f.Close()
		if err != nil {
			h.sendDCSftpError(dc, reqID, "写入文件失败: "+err.Error())
			return
		}
		h.sendDCSftpOk(dc, reqID, "write")

	case sftpOpMkdir:
		if err := client.MkdirAll(req.Path); err != nil {
			h.sendDCSftpError(dc, reqID, "创建目录失败: "+err.Error())
			return
		}
		h.sendDCSftpOk(dc, reqID, "mkdir")

	case sftpOpDelete:
		if err := client.Remove(req.Path); err != nil {
			h.sendDCSftpError(dc, reqID, "删除失败: "+err.Error())
			return
		}
		h.sendDCSftpOk(dc, reqID, "delete")

	case sftpOpRename:
		if err := client.Rename(req.OldPath, req.NewName); err != nil {
			h.sendDCSftpError(dc, reqID, "重命名失败: "+err.Error())
			return
		}
		h.sendDCSftpOk(dc, reqID, "rename")

	case sftpOpStat:
		_, err := client.Stat(req.Path)
		if err != nil {
			h.sendDCSftpError(dc, reqID, "获取状态失败: "+err.Error())
			return
		}
		h.sendDCSftpOk(dc, reqID, "stat")

	default:
		h.sendDCSftpError(dc, reqID, "未知操作")
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

	addr := net.JoinHostPort(connMsg.Host, strconv.Itoa(connMsg.Port))
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
		// 读缓冲须保证 1 字节前缀 + n ≤ 65535（pion 接收侧读缓冲上限），
		// 64KB 满读会得到 65537 字节消息：发送侧报 ErrOutboundPacketTooLarge，
		// 且对端接收会因超限关闭 DataChannel。
		buf := make([]byte, 60*1024)
		for {
			// 背压：DC 缓冲/待发队列高时暂停读 TCP，让 VNC 服务端降速（防丢帧）
			if !h.waitVCNSendCapacity(ctx, dc) {
				return
			}
			n, err := tcpConn.Read(buf)
			if n > 0 {
				// RFB 下行每条消息都进这里：级别判断前移，Info 级零开销且不再额外 copy
				if logger.Enabled(logger.LevelDebug) {
					if n <= 80 {
						hexStr := ""
						for _, b := range buf[:n] {
							hexStr += fmt.Sprintf("%02x ", b)
						}
						logger.Debugf("[A-BRIDGE] VNC TCP recv %d bytes: %s (room=%s)", n, hexStr, roomID)
					} else {
						logger.Debugf("[A-BRIDGE] VNC TCP recv %d bytes (room=%s)", n, roomID)
					}
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

// bridgeTCPTunnel 处理来自隧道Listener的DataChannel，桥接到本地TCP连接。
// 桥接端的 OnMessage/OnClose 与监听端路由合并注册（registerTunnelDC），
// 避免 pion 单槽回调互相覆盖导致先注册方失效。
func (h *SignalHandler) bridgeTCPTunnel(dc *webrtc.DataChannel, roomID string) {
	log.Printf("[TUNNEL-BRIDGE] tcp-tunnel DataChannel opened (room=%s)", roomID)

	conns := make(map[uint16]net.Conn)
	var mu sync.Mutex

	// 桥接端消息路由：返回 true 表示已消费。
	// Connect/Data/Disconnect 由桥接端独占（connID 由对端 Listener 分配），
	// 其余前缀（ConnectOK/UDPData）交回监听端路由处理。
	handleMsg := func(prefix byte, connID uint16, payload []byte) bool {
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
				return true
			}
			addr := net.JoinHostPort(req.Host, strconv.Itoa(req.Port))
			log.Printf("[TUNNEL-BRIDGE] 建立TCP连接: %s (connID=%d, room=%s)", addr, connID, roomID)

			conn, err := net.DialTimeout("tcp", addr, 10*time.Second)
			if err != nil {
				log.Printf("[TUNNEL-BRIDGE] TCP连接失败: %v (connID=%d)", err, connID)
				h.sendTunnelConnectOK(dc, connID, false, err.Error())
				return true
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
					if err := h.sendTunnelMsg(dc, MsgTunnelDisconnect, connID, nil); err != nil {
						log.Printf("[TUNNEL-BRIDGE] send Disconnect failed: %s (connID=%d)", err, connID)
					}
					log.Printf("[TUNNEL-BRIDGE] TCP连接关闭: %s (connID=%d)", addr, connID)
				}()
				buf := make([]byte, tunnelMaxPayload)
				for {
					// 背压：DC 发送缓冲超阈值时暂停读本地 TCP，避免无界堆积
					if !waitTunnelSendCapacity(dc) {
						log.Printf("[TUNNEL-BRIDGE] backpressure timeout/unavailable, closing TCP (connID=%d)", connID)
						return
					}
					n, err := conn.Read(buf)
					if n > 0 {
						if sendErr := h.sendTunnelMsg(dc, MsgTunnelData, connID, buf[:n]); sendErr != nil {
							// 发送失败说明字节已丢失，继续转发会造成流错位，直接断开
							log.Printf("[TUNNEL-BRIDGE] send Data failed: %s, closing TCP (connID=%d)", sendErr, connID)
							return
						}
					}
					if err != nil {
						return
					}
				}
			}()
			return true

		case MsgTunnelData:
			// DataChannel → TCP
			mu.Lock()
			conn, ok := conns[connID]
			mu.Unlock()
			if !ok {
				log.Printf("[TUNNEL-BRIDGE] data for unknown conn (connID=%d, %d bytes), dropped", connID, len(payload))
				return true
			}
			if len(payload) > 0 {
				if _, err := conn.Write(payload); err != nil {
					log.Printf("[TUNNEL-BRIDGE] TCP写入失败: %v (connID=%d), closing", err, connID)
					mu.Lock()
					if cur, exists := conns[connID]; exists && cur == conn {
						delete(conns, connID)
					}
					mu.Unlock()
					conn.Close()
					if sendErr := h.sendTunnelMsg(dc, MsgTunnelDisconnect, connID, nil); sendErr != nil {
						log.Printf("[TUNNEL-BRIDGE] send Disconnect failed: %s (connID=%d)", sendErr, connID)
					}
				}
			}
			return true

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
			return true
		}
		return false
	}

	handleClose := func() {
		log.Printf("[TUNNEL-BRIDGE] DataChannel关闭 (room=%s)", roomID)
		mu.Lock()
		for connID, conn := range conns {
			conn.Close()
			delete(conns, connID)
		}
		mu.Unlock()
	}

	registerTunnelDC(dc, handleMsg, handleClose)
}

func (h *SignalHandler) sendTunnelConnectOK(dc *webrtc.DataChannel, connID uint16, ok bool, detail string) {
	payload, _ := json.Marshal(map[string]interface{}{"ok": ok, "detail": detail})
	if err := h.sendTunnelMsg(dc, MsgTunnelConnectOK, connID, payload); err != nil {
		log.Printf("[TUNNEL-BRIDGE] send ConnectOK failed: %s (connID=%d)", err, connID)
	}
}

// sendTunnelMsg 发送隧道消息。返回错误时字节已丢失，调用方必须断开对应桥接，
// 绝不能静默继续（会导致字节流错位，如 TLS net::ERR_SSL_PROTOCOL_ERROR）。
func (h *SignalHandler) sendTunnelMsg(dc *webrtc.DataChannel, prefix byte, connID uint16, payload []byte) error {
	if len(payload) > tunnelMaxPayload {
		return fmt.Errorf("tunnel payload %d > %d (prefix=0x%02x connID=%d)", len(payload), tunnelMaxPayload, prefix, connID)
	}
	msg := make([]byte, 3+len(payload))
	msg[0] = prefix
	msg[1] = byte(connID >> 8)
	msg[2] = byte(connID)
	copy(msg[3:], payload)
	if err := dc.Send(msg); err != nil {
		return fmt.Errorf("dc send failed (prefix=0x%02x connID=%d len=%d): %w", prefix, connID, len(msg), err)
	}
	return nil
}
