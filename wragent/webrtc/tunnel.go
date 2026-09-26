package webrtc

import (
	"encoding/binary"
	"encoding/json"
	"fmt"
	"log"
	"net"
	"sync"
	"time"

	"github.com/ppy-tools/wragent/config"
	ws "github.com/ppy-tools/wragent/websocket"
	"github.com/pion/webrtc/v4"
)

// tunnelMsg 隧道内TCP消息
type tunnelMsg struct {
	connID uint16
	data   []byte
}

// ── 隧道管理器 ──────────────────────────────────────────────

// TunnelManager 管理多个隧道实例
type TunnelManager struct {
	tunnels map[string]*TunnelInstance
	mu      sync.RWMutex
	ws      *ws.Client
	sh      *SignalHandler
}

// TunnelInstance 单个隧道实例
type TunnelInstance struct {
	Config     config.TunnelConfig
	listener   net.Listener
	udpConn    *net.UDPConn
	cancel     chan struct{}
	running    bool
	mu         sync.Mutex
}

var tunnelManager *TunnelManager

// InitTunnelManager 初始化隧道管理器
func InitTunnelManager(wsClient *ws.Client, sh *SignalHandler) *TunnelManager {
	tunnelManager = &TunnelManager{
		tunnels: make(map[string]*TunnelInstance),
		ws:      wsClient,
		sh:      sh,
	}
	// 正常模式下 DataChannel 关闭（对端升级/重启等）后自动重连
	SetTunnelOnClose(func() {
		log.Println("[TUNNEL] DataChannel closed, reconnecting tunnels...")
		go func() {
			time.Sleep(2 * time.Second)
			tunnelManager.ReconnectAll()
		}()
	})
	return tunnelManager
}

// ReconnectAll 为所有启用且有目标Agent的隧道重发connect_tunnel
func (tm *TunnelManager) ReconnectAll() {
	tm.mu.RLock()
	defer tm.mu.RUnlock()
	for _, inst := range tm.tunnels {
		t := inst.Config
		if !t.Enabled || t.TargetAgentID == "" {
			continue
		}
		if err := tm.ws.SendConnectTunnel(t.TargetAgentID, tm.ws.Token()); err != nil {
			log.Printf("[TUNNEL] reconnect connect_tunnel to %s failed: %v", t.TargetAgentID, err)
		} else {
			log.Printf("[TUNNEL] reconnect connect_tunnel sent to %s for tunnel %s", t.TargetAgentID, t.ID)
		}
	}
}

// ReconcileTunnels 对比并更新隧道配置（热部署核心）
func (tm *TunnelManager) ReconcileTunnels(newTunnels []config.TunnelConfig) {
	tm.mu.Lock()
	defer tm.mu.Unlock()

	// 构建新配置索引
	newMap := make(map[string]config.TunnelConfig)
	for _, t := range newTunnels {
		if t.Enabled {
			newMap[t.ID] = t
		}
	}

	// 停止已删除或禁用的隧道
	for id, inst := range tm.tunnels {
		if _, ok := newMap[id]; !ok {
			log.Printf("[TUNNEL-MGR] stopping removed tunnel: %s", id)
			inst.Stop()
			delete(tm.tunnels, id)
		}
	}

	// 更新或新增隧道
	for id, newCfg := range newMap {
		if inst, ok := tm.tunnels[id]; ok {
			// 配置变更时先停后启
			if inst.ConfigChanged(newCfg) {
				log.Printf("[TUNNEL-MGR] config changed for tunnel %s, restarting", id)
				inst.Stop()
				inst = tm.startTunnel(newCfg)
				tm.tunnels[id] = inst
			}
		} else {
			log.Printf("[TUNNEL-MGR] starting new tunnel: %s", id)
			inst := tm.startTunnel(newCfg)
			tm.tunnels[id] = inst
		}
	}
}

func (tm *TunnelManager) startTunnel(cfg config.TunnelConfig) *TunnelInstance {
	inst := &TunnelInstance{
		Config: cfg,
		cancel: make(chan struct{}),
	}

	switch cfg.Protocol {
	case "udp":
		go inst.startUDPListener(tm.sh)
	default:
		go inst.startTCPListener(tm.sh)
	}

	inst.running = true
	return inst
}

// StopAll 停止所有隧道
func (tm *TunnelManager) StopAll() {
	tm.mu.Lock()
	defer tm.mu.Unlock()
	for id, inst := range tm.tunnels {
		inst.Stop()
		delete(tm.tunnels, id)
	}
}

// ── TunnelInstance 方法 ──────────────────────────────────────

// ConfigChanged 检查配置是否变更
func (ti *TunnelInstance) ConfigChanged(newCfg config.TunnelConfig) bool {
	return ti.Config.Protocol != newCfg.Protocol ||
		ti.Config.LocalPort != newCfg.LocalPort ||
		ti.Config.TargetAddr != newCfg.TargetAddr ||
		ti.Config.TargetAgentID != newCfg.TargetAgentID
}

// Stop 停止隧道
func (ti *TunnelInstance) Stop() {
	ti.mu.Lock()
	defer ti.mu.Unlock()
	if !ti.running {
		return
	}
	close(ti.cancel)
	if ti.listener != nil {
		ti.listener.Close()
	}
	if ti.udpConn != nil {
		ti.udpConn.Close()
	}
	ti.running = false
	log.Printf("[TUNNEL] stopped: %s (%s :%d)", ti.Config.Name, ti.Config.Protocol, ti.Config.LocalPort)
}

// startTCPListener 启动 TCP 隧道监听
func (ti *TunnelInstance) startTCPListener(sh *SignalHandler) {
	addr := fmt.Sprintf(":%d", ti.Config.LocalPort)
	ln, err := net.Listen("tcp", addr)
	if err != nil {
		log.Printf("[TUNNEL] TCP listen failed on port %d: %v", ti.Config.LocalPort, err)
		return
	}
	ti.listener = ln
	log.Printf("[TUNNEL] TCP listener started: %s -> %s (name=%s)", addr, ti.Config.TargetAddr, ti.Config.Name)

	for {
		select {
		case <-ti.cancel:
			return
		default:
			conn, err := ln.Accept()
			if err != nil {
				select {
				case <-ti.cancel:
					return
				default:
					log.Printf("[TUNNEL] TCP accept failed: %v", err)
					continue
				}
			}
			go bridgeTCPConn(conn, ti.Config.TargetAddr, sh)
		}
	}
}

// startUDPListener 启动 UDP 隧道监听
func (ti *TunnelInstance) startUDPListener(sh *SignalHandler) {
	addr, err := net.ResolveUDPAddr("udp", fmt.Sprintf(":%d", ti.Config.LocalPort))
	if err != nil {
		log.Printf("[TUNNEL] UDP resolve failed: %v", err)
		return
	}
	udpConn, err := net.ListenUDP("udp", addr)
	if err != nil {
		log.Printf("[TUNNEL] UDP listen failed on port %d: %v", ti.Config.LocalPort, err)
		return
	}
	ti.udpConn = udpConn
	log.Printf("[TUNNEL] UDP listener started: :%d -> %s (name=%s)", ti.Config.LocalPort, ti.Config.TargetAddr, ti.Config.Name)

	// connID -> clientAddr 映射
	sessions := make(map[uint16]*net.UDPAddr)
	sessionsMu := sync.RWMutex{}

	// 超时清理
	go func() {
		ticker := time.NewTicker(30 * time.Second)
		defer ticker.Stop()
		for {
			select {
			case <-ti.cancel:
				return
			case <-ticker.C:
				sessionsMu.Lock()
				// 简单清理: 超过60秒无数据的会话
				// (实际应用中需要 lastActive 追踪，此处简化)
				sessionsMu.Unlock()
			}
		}
	}()

	buf := make([]byte, 65536)
	for {
		select {
		case <-ti.cancel:
			return
		default:
			n, clientAddr, err := udpConn.ReadFromUDP(buf)
			if err != nil {
				select {
				case <-ti.cancel:
					return
				default:
					log.Printf("[TUNNEL] UDP read failed: %v", err)
					continue
				}
			}

			// 查找或分配 connID
			sessionsMu.RLock()
			var connID uint16
			found := false
			for id, addr := range sessions {
				if addr.IP.Equal(clientAddr.IP) && addr.Port == clientAddr.Port {
					connID = id
					found = true
					break
				}
			}
			sessionsMu.RUnlock()

			if !found {
				connID = nextConnID()
				sessionsMu.Lock()
				sessions[connID] = clientAddr
				sessionsMu.Unlock()
				log.Printf("[TUNNEL] UDP new session: connID=%d client=%s", connID, clientAddr)
			}

			// 转发到 DataChannel
			go bridgeUDPToDC(connID, buf[:n], ti.Config.TargetAddr, sh, udpConn, clientAddr)
		}
	}
}

// ── TCP 桥接函数 ──────────────────────────────────────────────

func bridgeTCPConn(conn net.Conn, targetAddr string, sh *SignalHandler) {
	defer conn.Close()

	dc := sh.findTunnelDataChannel()
	if dc == nil {
		log.Printf("[TUNNEL] no available tunnel DataChannel")
		return
	}

	connID := nextConnID()

	// 解析目标地址
	host, portStr, err := net.SplitHostPort(targetAddr)
	if err != nil {
		host = targetAddr
		portStr = "0"
	}
	var port int
	fmt.Sscanf(portStr, "%d", &port)

	// 发送连接请求
	reqPayload, _ := json.Marshal(map[string]interface{}{
		"host": host,
		"port": port,
	})
	tunnelSend(dc, MsgTunnelConnect, connID, reqPayload)

	// 等待连接确认
	ok, err := tunnelWaitOK(dc, connID, 10*time.Second)
	if err != nil || !ok {
		detail := "连接失败"
		if err != nil {
			detail = err.Error()
		}
		log.Printf("[TUNNEL] TCP connect rejected: %s (connID=%d)", detail, connID)
		return
	}

	log.Printf("[TUNNEL] TCP bridge started (connID=%d, remote=%s)", connID, conn.RemoteAddr())

	// 注册DataChannel→TCP转发
	dataCh := make(chan []byte, 64)
	tunnelConnsMu.Lock()
	tunnelConns[connID] = dataCh
	tunnelConnsMu.Unlock()

	defer func() {
		tunnelConnsMu.Lock()
		if ch, ok := tunnelConns[connID]; ok {
			close(ch)
			delete(tunnelConns, connID)
		}
		tunnelConnsMu.Unlock()
		tunnelSend(dc, MsgTunnelDisconnect, connID, nil)
		log.Printf("[TUNNEL] TCP bridge ended (connID=%d)", connID)
	}()

	done := make(chan struct{})
	var wg sync.WaitGroup

	// TCP → DataChannel
	wg.Add(1)
	go func() {
		defer wg.Done()
		defer close(done)
		buf := make([]byte, 65536)
		for {
			n, err := conn.Read(buf)
			if n > 0 {
				tunnelSend(dc, MsgTunnelData, connID, buf[:n])
			}
			if err != nil {
				return
			}
		}
	}()

	// DataChannel → TCP
	wg.Add(1)
	go func() {
		defer wg.Done()
		for {
			select {
			case data, ok := <-dataCh:
				if !ok {
					return
				}
				if len(data) > 0 {
					conn.Write(data)
				}
			case <-done:
				return
			}
		}
	}()

	wg.Wait()
}

// ── UDP 桥接函数 ──────────────────────────────────────────────

func bridgeUDPToDC(connID uint16, data []byte, targetAddr string, sh *SignalHandler, localConn *net.UDPConn, clientAddr *net.UDPAddr) {
	dc := sh.findTunnelDataChannel()
	if dc == nil {
		log.Printf("[TUNNEL] no available tunnel DataChannel for UDP")
		return
	}

	// 解析目标地址
	host, portStr, err := net.SplitHostPort(targetAddr)
	if err != nil {
		host = targetAddr
		portStr = "53"
	}
	var port int
	fmt.Sscanf(portStr, "%d", &port)

	// 通过 DataChannel 发送 UDP 数据
	payload, _ := json.Marshal(map[string]interface{}{
		"host":       host,
		"port":       port,
		"conn_id":    connID,
		"client_ip":  clientAddr.IP.String(),
		"client_port": clientAddr.Port,
		"data":       data,
	})
	tunnelSend(dc, MsgTunnelUDPData, connID, payload)
}

// ── DataChannel 消息路由 ──────────────────────────────────────

// 隧道DataChannel存储
var (
	tunnelDC      *webrtc.DataChannel
	tunnelDCMu    sync.RWMutex
	tunnelConns   = make(map[uint16]chan []byte)
	tunnelConnsMu sync.RWMutex
	tunnelOnClose func()
)

// SetTunnelOnClose 设置隧道DataChannel关闭回调
func SetTunnelOnClose(handler func()) {
	tunnelDCMu.Lock()
	tunnelOnClose = handler
	tunnelDCMu.Unlock()
}

// RegisterTunnelDC 注册隧道DataChannel
func RegisterTunnelDC(dc *webrtc.DataChannel) {
	tunnelDCMu.Lock()
	defer tunnelDCMu.Unlock()
	tunnelDC = dc

	dc.OnClose(func() {
		tunnelDCMu.Lock()
		tunnelDC = nil
		handler := tunnelOnClose
		tunnelDCMu.Unlock()
		log.Println("[TUNNEL] DataChannel closed")
		if handler != nil {
			go handler()
		}
	})

	// 设置消息路由
	dc.OnMessage(func(msg webrtc.DataChannelMessage) {
		data := msg.Data
		if len(data) < 3 {
			return
		}
		prefix := data[0]
		connID := binary.BigEndian.Uint16(data[1:3])
		payload := data[3:]

		switch prefix {
		case MsgTunnelConnectOK:
			tunnelOKMu.Lock()
			if ch, ok := tunnelOKWaiters[connID]; ok {
				var resp struct {
					OK     bool   `json:"ok"`
					Detail string `json:"detail"`
				}
				json.Unmarshal(payload, &resp)
				ch <- resp.OK
			}
			tunnelOKMu.Unlock()
		case MsgTunnelData:
			tunnelConnsMu.RLock()
			ch, ok := tunnelConns[connID]
			tunnelConnsMu.RUnlock()
			if ok {
				select {
				case ch <- payload:
				default:
				}
			}
		case MsgTunnelDisconnect:
			tunnelConnsMu.Lock()
			if ch, ok := tunnelConns[connID]; ok {
				close(ch)
				delete(tunnelConns, connID)
			}
			tunnelConnsMu.Unlock()
		case MsgTunnelUDPData:
			// UDP 响应数据 - 转发到本地客户端
			handleUDPResponse(payload)
		}
	})
}

func handleUDPResponse(payload []byte) {
	var resp struct {
		ConnID     uint16 `json:"conn_id"`
		ClientIP   string `json:"client_ip"`
		ClientPort int    `json:"client_port"`
		Data       []byte `json:"data"`
	}
	if err := json.Unmarshal(payload, &resp); err != nil {
		log.Printf("[TUNNEL] UDP response parse failed: %v", err)
		return
	}

	// 找到对应的本地 UDP 连接并转发
	// 这里需要通过 TunnelManager 找到对应的实例
	// 简化实现: 通过 tunnelManager 查找
	if tunnelManager != nil {
		tunnelManager.mu.RLock()
		for _, inst := range tunnelManager.tunnels {
			if inst.Config.Protocol == "udp" && inst.udpConn != nil {
				addr := &net.UDPAddr{
					IP:   net.ParseIP(resp.ClientIP),
					Port: resp.ClientPort,
				}
				inst.udpConn.WriteToUDP(resp.Data, addr)
				break
			}
		}
		tunnelManager.mu.RUnlock()
	}
}

func tunnelSend(dc *webrtc.DataChannel, prefix byte, connID uint16, payload []byte) {
	msg := make([]byte, 3+len(payload))
	msg[0] = prefix
	binary.BigEndian.PutUint16(msg[1:3], connID)
	copy(msg[3:], payload)
	dc.Send(msg)
}

// connID分配器
var (
	connIDCounter uint16
	connIDMu      sync.Mutex
)

func nextConnID() uint16 {
	connIDMu.Lock()
	defer connIDMu.Unlock()
	connIDCounter++
	if connIDCounter == 0 {
		connIDCounter = 1
	}
	return connIDCounter
}

// tunnelOK等待器
var (
	tunnelOKMu      sync.Mutex
	tunnelOKWaiters = make(map[uint16]chan bool)
)

func tunnelWaitOK(dc *webrtc.DataChannel, connID uint16, timeout time.Duration) (bool, error) {
	ch := make(chan bool, 1)
	tunnelOKMu.Lock()
	tunnelOKWaiters[connID] = ch
	tunnelOKMu.Unlock()

	defer func() {
		tunnelOKMu.Lock()
		delete(tunnelOKWaiters, connID)
		tunnelOKMu.Unlock()
	}()

	select {
	case ok := <-ch:
		return ok, nil
	case <-time.After(timeout):
		return false, fmt.Errorf("等待连接确认超时")
	}
}

// findTunnelDataChannel 查找可用的tunnel DataChannel
func (h *SignalHandler) findTunnelDataChannel() *webrtc.DataChannel {
	tunnelDCMu.RLock()
	defer tunnelDCMu.RUnlock()
	return tunnelDC
}

// StartTunnel 启动隧道入口模式（兼容旧接口）
func StartTunnel(cfg *config.Config) {
	// 旧版兼容: 如果有隧道配置，转换为新格式
	tunnels := []config.TunnelConfig{}
	if cfg.TunnelLocalPort > 0 && cfg.TunnelAgentID != "" && cfg.TunnelTargetAddr != "" {
		tunnels = append(tunnels, config.TunnelConfig{
			ID:            "legacy-tunnel",
			Name:          "Legacy Tunnel",
			Protocol:      "tcp",
			LocalPort:     cfg.TunnelLocalPort,
			TargetAddr:    cfg.TunnelTargetAddr,
			TargetAgentID: cfg.TunnelAgentID,
			Enabled:       true,
		})
	}
	StartTunnels(cfg.ServerURL, cfg.AuthToken, cfg.AgentID, tunnels)
}

// StartTunnels 启动多隧道模式
func StartTunnels(serverURL, token, agentID string, tunnels []config.TunnelConfig) {
	wsClient := ws.NewClient(serverURL, token, agentID)
	sh := NewSignalHandler(wsClient)

	// 初始化隧道管理器
	tm := InitTunnelManager(wsClient, sh)

	// 重连限速
	const minReconnectInterval = 15 * time.Second
	var reconnectMu sync.Mutex
	lastConnectAttempt := time.Time{}

	reconnect := func() {
		reconnectMu.Lock()
		if time.Since(lastConnectAttempt) < minReconnectInterval {
			reconnectMu.Unlock()
			return
		}
		lastConnectAttempt = time.Now()
		reconnectMu.Unlock()
		log.Printf("[TUNNEL] initiating reconnection")
		if err := wsClient.SendConnectTunnel("", wsClient.Token()); err != nil {
			log.Printf("[TUNNEL] send connect_tunnel failed: %v", err)
		}
	}

	sh.OnRegister(func() {
		log.Printf("[TUNNEL] registered, starting tunnels")
		// 启动所有隧道
		tm.ReconcileTunnels(tunnels)
		reconnect()
	})

	sh.OnConnectSuccess(func(roomID string) {
		log.Printf("[TUNNEL] connected, creating peer: room=%s", roomID)
		sh.CreateTunnelPeer(roomID)
	})

	SetTunnelOnClose(func() {
		log.Printf("[TUNNEL] DataChannel closed, reconnecting...")
		go func() {
			time.Sleep(2 * time.Second)
			reconnect()
		}()
	})

	sh.OnReady(func() {
		log.Println("[TUNNEL] DataChannel ready")
	})

	sh.OnClose(func() {
		log.Println("[TUNNEL] WebRTC connection closed")
	})

	sh.OnAuthFailed(func() {
		log.Println("[TUNNEL] token invalid")
	})

	if err := sh.Start(); err != nil {
		log.Printf("[TUNNEL] start failed: %v", err)
		return
	}

	// 看门狗
	go func() {
		for {
			time.Sleep(10 * time.Second)
			if !isTunnelHealthy() {
				log.Printf("[TUNNEL] watchdog: unhealthy, reconnecting")
				reconnect()
			}
		}
	}()

	// 等待 DataChannel 就绪
	go func() {
		for {
			sh.mu.RLock()
			hasPeer := len(sh.p) > 0
			sh.mu.RUnlock()
			if hasPeer {
				break
			}
			time.Sleep(500 * time.Millisecond)
		}
		log.Println("[TUNNEL] all tunnels active")
	}()

	select {}
}

// isTunnelHealthy 检查隧道DataChannel是否可用
func isTunnelHealthy() bool {
	tunnelDCMu.RLock()
	defer tunnelDCMu.RUnlock()
	return tunnelDC != nil && tunnelDC.ReadyState() == webrtc.DataChannelStateOpen
}
