package webrtc

import (
	"encoding/binary"
	"encoding/json"
	"fmt"
	"log"
	"net"
	"sync"
	"sync/atomic"
	"time"

	"github.com/pion/webrtc/v4"
	"github.com/ppy-tools/pyagent/config"
	ws "github.com/ppy-tools/pyagent/websocket"
)

// tunnelMsg 隧道内TCP消息
type tunnelMsg struct {
	connID uint16
	data   []byte
}

// 隧道消息大小限制（带冗余余量，不贴上限设计）。
//
// 硬上限（不可违反）：
//
//	发送侧：pion/sctp 单条消息 65536 字节（association.go defaultMaxMessageSize），
//	  超限 dc.Send 返回 ErrOutboundPacketTooLarge，整块丢字节导致字节流错位
//	  （TLS 报 net::ERR_SSL_PROTOCOL_ERROR）。
//	接收侧：pion/webrtc 读循环缓冲 dataChannelBufferSize = math.MaxUint16 = 65535
//	  （webrtc/v4 datachannel.go:23）。超限 reassemblyQueue.read 返回
//	  io.ErrShortBuffer，读循环直接关闭本地 DataChannel 且对端无感知
//	  （观测为单侧反复 close + 传输卡在 65536 字节）。
//
// 冗余设计：总预算取 65535 - tunnelMsgMargin = 64511，两侧硬上限、未来头部
// 扩展、pion/浏览器版本差异、JSON 封装等任何额外开销都在这 1KB 余量内消化，
// 不依赖「刚好卡住」的巧合。
const (
	tunnelMsgMargin = 1024                    // 相对硬上限预留的冗余余量
	tunnelHeaderLen = 3                       // [prefix][connID] 消息头长度
	tunnelMsgBudget = 65535 - tunnelMsgMargin // 单条消息总预算（含头部）
)

// tunnelMaxPayload 单条隧道消息 payload 上限。
const tunnelMaxPayload = tunnelMsgBudget - tunnelHeaderLen // 64508

// tunnelUDPReadBuf UDP 读缓冲。payload 经 JSON+base64 后放大约 4/3：
// 3 + base64(48000)=64000 + JSON 封装(~120) ≈ 64121 ≤ 64511，距硬上限 65535
// 仍有 1400+ 字节余量。
const tunnelUDPReadBuf = 48000

// tunnelBackpressureTimeout 背压等待上限：超过后判定发送侧不可用并断开桥接。
const tunnelBackpressureTimeout = 30 * time.Second

// waitTunnelSendCapacity 背压：DC 发送缓冲超过阈值时暂停读取本地 TCP，
// 让数据量回落，避免 pion 内部无界堆积。返回 false 表示 DC 不可用或等待超时。
func waitTunnelSendCapacity(dc *webrtc.DataChannel) bool {
	deadline := time.Now().Add(tunnelBackpressureTimeout)
	for dc.BufferedAmount() > dcBackpressureThreshold {
		if dc.ReadyState() != webrtc.DataChannelStateOpen {
			return false
		}
		if time.Now().After(deadline) {
			return false
		}
		time.Sleep(10 * time.Millisecond)
	}
	return dc.ReadyState() == webrtc.DataChannelStateOpen
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
	Config   config.TunnelConfig
	listener net.Listener
	udpConn  *net.UDPConn
	cancel   chan struct{}
	running  bool
	mu       sync.Mutex
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

// ReconnectAll 为所有启用且有目标Agent的隧道重发connect_tunnel（按目标去重）
func (tm *TunnelManager) ReconnectAll() {
	tm.mu.RLock()
	defer tm.mu.RUnlock()
	seen := make(map[string]bool)
	for _, inst := range tm.tunnels {
		t := inst.Config
		if !t.Enabled || t.TargetAgentID == "" || seen[t.TargetAgentID] {
			continue
		}
		seen[t.TargetAgentID] = true
		if err := tm.ws.SendConnectTunnel(t.TargetAgentID, tm.ws.Token()); err != nil {
			log.Printf("[TUNNEL] reconnect connect_tunnel to %s failed: %v", t.TargetAgentID, err)
		} else {
			log.Printf("[TUNNEL] reconnect connect_tunnel sent to %s for tunnel %s", t.TargetAgentID, t.ID)
		}
	}
}

// hasPendingTunnels 是否存在启用且有目标Agent的隧道
func (tm *TunnelManager) hasPendingTunnels() bool {
	tm.mu.RLock()
	defer tm.mu.RUnlock()
	for _, inst := range tm.tunnels {
		if inst.Config.Enabled && inst.Config.TargetAgentID != "" {
			return true
		}
	}
	return false
}

// StartTunnelWatchdog 启动隧道看门狗（正常模式）。
// 兜底场景：connect_tunnel 被服务端拒绝（如 target_agent_offline）后无人重试，
// 对端上线后隧道永久卡死；DC 从未建立时 OnClose 也不会触发。
func StartTunnelWatchdog(wsClient *ws.Client, interval time.Duration) {
	if interval <= 0 {
		interval = 30 * time.Second
	}
	go func() {
		ticker := time.NewTicker(interval)
		defer ticker.Stop()
		for range ticker.C {
			if tunnelManager == nil || !wsClient.IsConnected() {
				continue
			}
			if isTunnelHealthy() || !tunnelManager.hasPendingTunnels() {
				continue
			}
			log.Printf("[TUNNEL] watchdog: DataChannel not ready, reconnecting tunnels")
			tunnelManager.ReconnectAll()
		}
	}()
}

// ReconcileTunnels 对比并更新隧道配置（热部署核心）
func (tm *TunnelManager) ReconcileTunnels(newTunnels []config.TunnelConfig) {
	tm.mu.Lock()
	defer tm.mu.Unlock()

	// 构建新配置索引
	newMap := make(map[string]config.TunnelConfig)
	for _, t := range newTunnels {
		// 阶段C: socks5 已迁出至 plugins/socks5 插件, 旧通路只保留 tcp/udp
		if t.Protocol == "socks5" {
			continue
		}
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

// Stats 运行统计(阶段D: tunnel 插件 Status 汇总用)
func (tm *TunnelManager) Stats() (int, string) {
	tm.mu.RLock()
	defer tm.mu.RUnlock()
	parts := make([]string, 0, len(tm.tunnels))
	for _, inst := range tm.tunnels {
		parts = append(parts, fmt.Sprintf("%s:%d", inst.Config.Protocol, inst.Config.LocalPort))
	}
	detail := "0 tunnels"
	if len(parts) > 0 {
		detail = fmt.Sprintf("%d tunnels", len(parts))
		for i := 0; i < len(parts); i++ {
			detail += " " + parts[i]
		}
	}
	return len(tm.tunnels), detail
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
		ti.Config.TargetAgentID != newCfg.TargetAgentID ||
		ti.Config.SocksUsername != newCfg.SocksUsername ||
		ti.Config.SocksPassword != newCfg.SocksPassword
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
			go bridgeTCPConn(conn, ti.Config.TargetAddr, sh, nil)
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

	// P0: 有界 worker 池替代“每包一个 goroutine”。
	// 旧实现 go bridgeUDPToDC(connID, buf[:n], ...) 既随包速率起 goroutine，
	// 又把复用中的共享 buf 交给异步协程 → 数据竞争。
	type udpPkt struct {
		connID uint16
		data   []byte // 已拷贝，读循环可复用 buf
		addr   *net.UDPAddr
	}
	const (
		udpWorkers = 8
		udpQueue   = 512
	)
	udpCh := make(chan udpPkt, udpQueue)
	var udpDropped atomic.Uint64
	var lastDropLog atomic.Int64
	for i := 0; i < udpWorkers; i++ {
		go func() {
			for {
				select {
				case <-ti.cancel:
					return
				case p := <-udpCh:
					bridgeUDPToDC(p.connID, p.data, ti.Config.TargetAddr, sh, udpConn, p.addr)
				}
			}
		}()
	}

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

	buf := make([]byte, tunnelUDPReadBuf)
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

			// 转发到 DataChannel：先拷贝（读循环复用 buf），再进有界队列。
			// 队列满则丢包（UDP 本就允许丢），限频记日志避免洪水。
			pkt := udpPkt{connID: connID, addr: clientAddr, data: make([]byte, n)}
			copy(pkt.data, buf[:n])
			select {
			case udpCh <- pkt:
			default:
				if n := udpDropped.Add(1); n%1024 == 1 {
					if now := time.Now().Unix(); now-lastDropLog.Load() >= 5 {
						lastDropLog.Store(now)
						log.Printf("[TUNNEL] UDP queue full, dropped=%d", n)
					}
				}
			}
		}
	}
}

// ── TCP 桥接函数 ──────────────────────────────────────────────

func bridgeTCPConn(conn net.Conn, targetAddr string, sh *SignalHandler, onResult func(ok bool)) {
	defer conn.Close()

	dc := sh.findTunnelDataChannel()
	if dc == nil {
		log.Printf("[TUNNEL] no available tunnel DataChannel")
		if onResult != nil {
			onResult(false)
		}
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

	// 先注册等待器与 DataChannel→TCP 队列再发 Connect：
	// 1) 对端 OK/Data 可能先于本地注册到达，晚注册会丢字节（流错位）；
	// 2) 等待器晚注册会白等 10s 超时。
	dataCh := make(chan []byte, 64)
	tunnelConnsMu.Lock()
	tunnelConns[connID] = &tunnelConn{
		ch:     dataCh,
		cancel: func() { conn.Close() },
	}
	tunnelConnsMu.Unlock()
	unregistered := false
	unregister := func() {
		if unregistered {
			return
		}
		unregistered = true
		tunnelConnsMu.Lock()
		tc, ok := tunnelConns[connID]
		if ok {
			delete(tunnelConns, connID)
		}
		tunnelConnsMu.Unlock()
		if ok {
			close(tc.ch)
		}
	}

	waiter, cancelWait := registerTunnelOKWaiter(connID)
	defer cancelWait()
	defer unregister()

	// 发送连接请求
	reqPayload, _ := json.Marshal(map[string]interface{}{
		"host": host,
		"port": port,
	})
	if err := tunnelSend(dc, MsgTunnelConnect, connID, reqPayload); err != nil {
		log.Printf("[TUNNEL] send Connect failed: %s (connID=%d)", err, connID)
		if onResult != nil {
			onResult(false)
		}
		return
	}

	// 等待连接确认
	ok, err := waitTunnelOK(waiter, 10*time.Second)
	if err != nil || !ok {
		detail := "连接失败"
		if err != nil {
			detail = err.Error()
		}
		log.Printf("[TUNNEL] TCP connect rejected: %s (connID=%d)", detail, connID)
		if onResult != nil {
			onResult(false)
		}
		return
	}
	if onResult != nil {
		onResult(true)
	}

	log.Printf("[TUNNEL] TCP bridge started (connID=%d, remote=%s)", connID, conn.RemoteAddr())

	defer func() {
		unregister()
		if err := tunnelSend(dc, MsgTunnelDisconnect, connID, nil); err != nil {
			log.Printf("[TUNNEL] send Disconnect failed: %s (connID=%d)", err, connID)
		}
		log.Printf("[TUNNEL] TCP bridge ended (connID=%d)", connID)
	}()

	done := make(chan struct{})
	var wg sync.WaitGroup

	// TCP → DataChannel
	wg.Add(1)
	go func() {
		defer wg.Done()
		defer close(done)
		buf := make([]byte, tunnelMaxPayload)
		for {
			// 背压：DC 发送缓冲超阈值时暂停读本地 TCP，避免无界堆积
			if !waitTunnelSendCapacity(dc) {
				log.Printf("[TUNNEL] backpressure timeout/unavailable, closing bridge (connID=%d)", connID)
				return
			}
			n, err := conn.Read(buf)
			if n > 0 {
				if sendErr := tunnelSend(dc, MsgTunnelData, connID, buf[:n]); sendErr != nil {
					// 发送失败说明字节已丢失，继续转发会造成流错位，直接断开
					log.Printf("[TUNNEL] send Data failed: %s, closing bridge (connID=%d)", sendErr, connID)
					return
				}
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
					if _, werr := conn.Write(data); werr != nil {
						log.Printf("[TUNNEL] TCP write failed: %s (connID=%d), closing conn", werr, connID)
						// 关闭 conn 让另一侧 Read 返回错误，避免 wg.Wait 永久阻塞
						conn.Close()
						return
					}
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
		"host":        host,
		"port":        port,
		"conn_id":     connID,
		"client_ip":   clientAddr.IP.String(),
		"client_port": clientAddr.Port,
		"data":        data,
	})
	if err := tunnelSend(dc, MsgTunnelUDPData, connID, payload); err != nil {
		log.Printf("[TUNNEL] UDP send failed: %s (connID=%d)", err, connID)
	}
}

// ── DataChannel 消息路由 ──────────────────────────────────────

// 隧道DataChannel存储
var (
	tunnelDC      *webrtc.DataChannel
	tunnelDCMu    sync.RWMutex
	tunnelConns   = make(map[uint16]*tunnelConn)
	tunnelConnsMu sync.RWMutex
	tunnelOnClose func()
)

// tunnelConn 单条隧道TCP连接的本地端点
type tunnelConn struct {
	ch     chan []byte
	cancel func() // 关闭本地 TCP 连接
}

// SetTunnelOnClose 设置隧道DataChannel关闭回调
func SetTunnelOnClose(handler func()) {
	tunnelDCMu.Lock()
	tunnelOnClose = handler
	tunnelDCMu.Unlock()
}

// RegisterTunnelDC 注册隧道DataChannel（仅监听端路由）
func RegisterTunnelDC(dc *webrtc.DataChannel) {
	registerTunnelDC(dc, nil, nil)
}

// registerTunnelDC 注册隧道DataChannel，单一 OnMessage/OnClose 持有者。
//
// bridgeMsg/bridgeClose 为可选的桥接端（对端 Listener 一侧）回调：
// bridgeMsg 返回 true 表示已消费该消息。pion 的 OnMessage/OnClose 是单槽，
// 若监听端与桥接端各自注册会相互覆盖（后者胜出），导致先注册方的路由与
// 断连回调永久失效，因此必须合并到同一处注册。
func registerTunnelDC(dc *webrtc.DataChannel,
	bridgeMsg func(prefix byte, connID uint16, payload []byte) bool,
	bridgeClose func()) {

	tunnelDCMu.Lock()
	defer tunnelDCMu.Unlock()
	tunnelDC = dc

	dc.OnClose(func() {
		if bridgeClose != nil {
			bridgeClose()
		}
		tunnelDCMu.Lock()
		// 代际守卫：仅当关闭的是当前隧道 DC 才清空并触发重连。
		// 旧 peer 的 DC 迟到 OnClose 不得清掉已就绪的新 DC，
		// 否则会误判断线并引发 ReconnectAll 重连风暴。
		stale := tunnelDC != dc
		if !stale {
			tunnelDC = nil
		}
		handler := tunnelOnClose
		tunnelDCMu.Unlock()
		if stale {
			log.Println("[TUNNEL] DataChannel closed (stale, ignored)")
			return
		}
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

		// 桥接端优先消费（自己发起的 Connect、自己 dial 出去的 conn 的 Data/Disconnect）
		if bridgeMsg != nil && bridgeMsg(prefix, connID, payload) {
			return
		}

		switch prefix {
		case MsgTunnelConnectOK:
			tunnelOKMu.Lock()
			if ch, ok := tunnelOKWaiters[connID]; ok {
				var resp struct {
					OK     bool   `json:"ok"`
					Detail string `json:"detail"`
				}
				json.Unmarshal(payload, &resp)
				// 非阻塞：ch 容量为 1，重复/迟到的 OK 不得卡在锁内
				select {
				case ch <- resp.OK:
				default:
				}
			}
			tunnelOKMu.Unlock()
		case MsgTunnelData:
			tunnelConnsMu.RLock()
			tc, ok := tunnelConns[connID]
			tunnelConnsMu.RUnlock()
			if !ok {
				log.Printf("[TUNNEL] data for unknown conn (connID=%d, %d bytes), dropped", connID, len(payload))
				return
			}
			select {
			case tc.ch <- payload:
			default:
				// 队列满：静默丢字节会破坏字节流（TLS/SSH 等直接错位），
				// 改为断开该连接，让上层按连接失败重试，绝不丢字节。
				log.Printf("[TUNNEL] dataCh full, closing conn (connID=%d, cap=%d), %d bytes dropped",
					connID, cap(tc.ch), len(payload))
				tunnelConnsMu.Lock()
				if cur, exists := tunnelConns[connID]; exists && cur == tc {
					delete(tunnelConns, connID)
				}
				tunnelConnsMu.Unlock()
				tc.cancel()
			}
		case MsgTunnelDisconnect:
			tunnelConnsMu.Lock()
			tc, ok := tunnelConns[connID]
			if ok {
				delete(tunnelConns, connID)
			}
			tunnelConnsMu.Unlock()
			if ok {
				close(tc.ch)
				tc.cancel()
			}
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

func tunnelSend(dc *webrtc.DataChannel, prefix byte, connID uint16, payload []byte) error {
	if len(payload) > tunnelMaxPayload {
		return fmt.Errorf("tunnel payload %d > %d (prefix=0x%02x connID=%d)", len(payload), tunnelMaxPayload, prefix, connID)
	}
	msg := make([]byte, 3+len(payload))
	msg[0] = prefix
	binary.BigEndian.PutUint16(msg[1:3], connID)
	copy(msg[3:], payload)
	if err := dc.Send(msg); err != nil {
		return fmt.Errorf("dc send failed (prefix=0x%02x connID=%d len=%d): %w", prefix, connID, len(msg), err)
	}
	return nil
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

// registerTunnelOKWaiter 注册 ConnectOK 等待器。必须在发送 Connect 之前调用，
// 否则对端极快返回的 OK 会先于注册到达而被丢弃，导致白等超时。
// 返回等待通道与注销函数（调用方需 defer 注销）。
func registerTunnelOKWaiter(connID uint16) (chan bool, func()) {
	ch := make(chan bool, 1)
	tunnelOKMu.Lock()
	tunnelOKWaiters[connID] = ch
	tunnelOKMu.Unlock()

	return ch, func() {
		tunnelOKMu.Lock()
		if cur, ok := tunnelOKWaiters[connID]; ok && cur == ch {
			delete(tunnelOKWaiters, connID)
		}
		tunnelOKMu.Unlock()
	}
}

func waitTunnelOK(ch chan bool, timeout time.Duration) (bool, error) {
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
