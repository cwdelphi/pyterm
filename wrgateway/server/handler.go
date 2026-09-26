package server

import (
	"crypto/tls"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net/http"
	"os"
	"strings"
	"sync"
	"time"

	"github.com/gorilla/websocket"
	"github.com/pion/webrtc/v4"

	"github.com/ppy-tools/wrgateway/config"
	"github.com/ppy-tools/wrgateway/signaling"
)

var upgrader = websocket.Upgrader{
	Subprotocols:      []string{"bearer"},
	CheckOrigin:       func(r *http.Request) bool { return true },
	EnableCompression: true,
	WriteBufferSize:   64 * 1024,
	ReadBufferSize:    64 * 1024,
}

// extractToken 浏览器侧 JWT: 优先 Authorization / Sec-WebSocket-Protocol, 回退 query（兼容旧客户端, S7 减少进 access log）
func extractToken(r *http.Request) string {
	if ah := r.Header.Get("Authorization"); strings.HasPrefix(ah, "Bearer ") {
		if t := strings.TrimSpace(strings.TrimPrefix(ah, "Bearer ")); t != "" {
			return t
		}
	}
	// Sec-WebSocket-Protocol: "bearer", "<jwt>"
	if sp := r.Header.Get("Sec-WebSocket-Protocol"); sp != "" {
		parts := strings.Split(sp, ",")
		for i := 0; i < len(parts); i++ {
			p := strings.TrimSpace(parts[i])
			if i == 0 && strings.EqualFold(p, "bearer") && i+1 < len(parts) {
				return strings.TrimSpace(parts[i+1])
			}
			if !strings.EqualFold(p, "bearer") && p != "" {
				// 单段协议若本身是 JWT 形态（含 2 个 '.'）也接受
				if strings.Count(p, ".") == 2 {
					return p
				}
			}
		}
	}
	return r.URL.Query().Get("token")
}

const (
	MSG_TERMINAL      byte = 0x00
	MSG_SSH_CONNECT   byte = 0x01
	MSG_RESIZE        byte = 0x02
	MSG_ACK           byte = 0x03 // P3: 终端输出累计确认
	MSG_SFTP_REQUEST  byte = 0x10
	MSG_SFTP_RESPONSE byte = 0x11
	MSG_VNC_CONNECT   byte = 0x20
	MSG_VNC_DATA      byte = 0x21
	MSG_VNC_DISCONNECT byte = 0x22
	MSG_VNC_INPUT     byte = 0x23
	MSG_VNC_ERROR     byte = 0x2F
	MSG_ERROR         byte = 0xFF
	MSG_DIAGNOSTICS   byte = 0xFE
)

func msgTypeToPrefix(msgType string) byte {
	switch msgType {
	case "terminal_data":
		return MSG_TERMINAL
	case "ssh_connect":
		return MSG_SSH_CONNECT
	case "resize":
		return MSG_RESIZE
	case "sftp_request":
		return MSG_SFTP_REQUEST
	case "vnc_connect":
		return MSG_VNC_CONNECT
	case "vnc_disconnect":
		return MSG_VNC_DISCONNECT
	case "vnc_input":
		return MSG_VNC_INPUT
	case "vnc_error":
		return MSG_VNC_ERROR
	case "terminal_ack":
		return MSG_ACK
	default:
		return 0
	}
}

type Session struct {
	ID          string
	RoomID      string
	AgentID     string
	GatewayID   string
	Conn        *websocket.Conn
	SendCh      chan []byte
	Done        chan struct{}
	PeerConn    *webrtc.PeerConnection
	DataChannel *webrtc.DataChannel
	pendingMsgs [][]byte
	// P1: DC 背压待发队列（浏览器→agent 终端帧）
	bpPending [][]byte
	bpBytes   int
	bpDropped uint64
	bpFlush   bool
	mu        sync.Mutex
}

type Handler struct {
	cfg      *config.Config
	client   *signaling.Client
	sessions map[string]*Session
	mu       sync.RWMutex
}

func NewHandler(cfg *config.Config) *Handler {
	return &Handler{
		cfg:      cfg,
		sessions: make(map[string]*Session),
	}
}

func (h *Handler) HandleWebSocket(w http.ResponseWriter, r *http.Request) {
	token := extractToken(r)
	if token == "" {
		http.Error(w, "missing token", http.StatusUnauthorized)
		return
	}
	if err := h.verifyToken(token); err != nil {
		http.Error(w, "invalid token", http.StatusUnauthorized)
		return
	}
	conn, err := upgrader.Upgrade(w, r, nil)
	if err != nil {
		log.Printf("[WSS] upgrade error: %v", err)
		return
	}
	session := &Session{
		ID:        fmt.Sprintf("browser_%d", time.Now().UnixNano()),
		GatewayID: h.cfg.GatewayID,
		Conn:      conn,
		SendCh:    make(chan []byte, 4096),
		Done:      make(chan struct{}),
	}
	h.mu.Lock()
	h.sessions[session.ID] = session
	h.mu.Unlock()
	log.Printf("[WSS] browser connected: %s", session.ID)
	go h.readPump(session)
	go h.writePump(session)
}

func (h *Handler) readPump(session *Session) {
	defer func() {
		h.mu.Lock()
		delete(h.sessions, session.ID)
		h.mu.Unlock()
		session.mu.Lock()
		if session.DataChannel != nil {
			session.DataChannel.Close()
		}
		if session.PeerConn != nil {
			session.PeerConn.Close()
		}
		session.mu.Unlock()
		close(session.Done)
		session.Conn.Close()
		log.Printf("[WSS] session closed: %s", session.ID)
	}()
	for {
		msgType, data, err := session.Conn.ReadMessage()
		if err != nil {
			return
		}

		// Binary WebSocket frame: 直接转发到 DataChannel (零开销)
		if msgType == websocket.BinaryMessage && len(data) >= 1 {
			session.mu.Lock()
			dc := session.DataChannel
			session.mu.Unlock()
			if dc != nil && dc.ReadyState() == webrtc.DataChannelStateOpen {
				// P1: 终端帧背压入队，其余直发（含 P3 ack 0x03）
				if data[0] == MSG_TERMINAL && dc.BufferedAmount() >= 64*1024 {
					h.enqueueSessionFrame(session, data)
				} else {
					dc.Send(data)
				}
			}
			continue
		}

		// Text WebSocket frame: JSON 解析 (兼容旧协议)
		var msg map[string]interface{}
		if err := json.Unmarshal(data, &msg); err != nil {
			continue
		}
		mType, _ := msg["type"].(string)
		switch mType {
		case "connect_gateway":
			if rid, ok := msg["room_id"].(string); ok && rid != "" {
				session.mu.Lock()
				session.RoomID = rid
				session.mu.Unlock()
			}
			if err := h.client.Send(data); err != nil {
				log.Printf("[WSS] forward connect_gateway error: %v", err)
			}
		case "terminal_data", "ssh_connect", "resize", "sftp_request", "vnc_connect", "vnc_disconnect", "vnc_input", "vnc_error", "terminal_ack":
			h.bridgeWSToDataChannel(session, msg)
		case "heartbeat", "connection_type":
			// silently ignore
		default:
			log.Printf("[WSS] unknown message type from browser: %s", mType)
		}
	}
}

const dcBPThreshold = 64 * 1024
const dcBPPendingMax = 256 * 1024

// enqueueSessionFrame P1: 浏览器→DC 终端帧背压入队，低水位冲刷
func (h *Handler) enqueueSessionFrame(session *Session, frame []byte) {
	session.mu.Lock()
	for session.bpBytes+len(frame) > dcBPPendingMax && len(session.bpPending) > 0 {
		old := session.bpPending[0]
		session.bpPending = session.bpPending[1:]
		session.bpBytes -= len(old)
		session.bpDropped++
	}
	if len(frame) > dcBPPendingMax {
		session.bpDropped++
		session.mu.Unlock()
		return
	}
	session.bpPending = append(session.bpPending, frame)
	session.bpBytes += len(frame)
	needSchedule := !session.bpFlush
	if needSchedule {
		session.bpFlush = true
	}
	session.mu.Unlock()
	if needSchedule {
		go h.flushSessionBP(session)
	}
}

func (h *Handler) flushSessionBP(session *Session) {
	for i := 0; i < 40; i++ {
		time.Sleep(25 * time.Millisecond)
		session.mu.Lock()
		if len(session.bpPending) == 0 || session.DataChannel == nil ||
			session.DataChannel.ReadyState() != webrtc.DataChannelStateOpen {
			session.bpFlush = false
			session.mu.Unlock()
			return
		}
		if session.DataChannel.BufferedAmount() >= dcBPThreshold/2 {
			session.mu.Unlock()
			continue
		}
		batch := session.bpPending
		session.bpPending = nil
		session.bpBytes = 0
		dc := session.DataChannel
		dropped := session.bpDropped
		session.bpDropped = 0
		session.mu.Unlock()
		for _, m := range batch {
			if dc.BufferedAmount() >= dcBPThreshold {
				session.mu.Lock()
				rest := append([][]byte{m}, session.bpPending...)
				var kept [][]byte
				var bytes int
				for _, x := range rest {
					if bytes+len(x) > dcBPPendingMax {
						session.bpDropped++
						break
					}
					kept = append(kept, x)
					bytes += len(x)
				}
				session.bpPending = kept
				session.bpBytes = bytes
				session.mu.Unlock()
				break
			}
			_ = dc.Send(m)
		}
		if dropped > 0 {
			log.Printf("[WSS] backpressure dropped %d terminal frames session=%s", dropped, session.ID)
		}
	}
	session.mu.Lock()
	session.bpFlush = false
	session.mu.Unlock()
}

func (h *Handler) writePump(session *Session) {
	for {
		select {
		case msg := <-session.SendCh:
			session.Conn.SetWriteDeadline(time.Now().Add(10 * time.Second))
			// Binary frame: terminal/VNC data (prefix byte 0x00-0x2F)
			// Text frame: JSON control messages
			var err error
			if len(msg) >= 1 && msg[0] <= 0x2F {
				err = session.Conn.WriteMessage(websocket.BinaryMessage, msg)
			} else {
				err = session.Conn.WriteMessage(websocket.TextMessage, msg)
			}
			if err != nil {
				return
			}
		case <-session.Done:
			return
		}
	}
}

func (h *Handler) bridgeWSToDataChannel(session *Session, msg map[string]interface{}) {
	session.mu.Lock()
	dc := session.DataChannel
	session.mu.Unlock()
	if dc == nil {
		msgType, _ := msg["type"].(string)
		prefix := msgTypeToPrefix(msgType)
		if prefix == 0 {
			log.Printf("[WSS] DataChannel not ready, unknown type %s", msgType)
			return
		}
		var payload []byte
		switch msgType {
		case "terminal_data":
			b64, _ := msg["data"].(string)
			raw, err := base64Decode(b64)
			if err != nil {
				return
			}
			payload = make([]byte, 1+len(raw))
			payload[0] = prefix
			copy(payload[1:], raw)
		default:
			j, _ := json.Marshal(msg)
			payload = make([]byte, 1+len(j))
			payload[0] = prefix
			copy(payload[1:], j)
		}
		session.mu.Lock()
		const maxPendingMsgs = 4096
		if len(session.pendingMsgs) >= maxPendingMsgs {
			session.mu.Unlock()
			log.Printf("[WSS] pendingMsgs limit reached, dropping %s", msgType)
			return
		}
		session.pendingMsgs = append(session.pendingMsgs, payload)
		qLen := len(session.pendingMsgs)
		session.mu.Unlock()
		log.Printf("[WSS] DataChannel not ready, buffered %s (queue=%d)", msgType, qLen)
		return
	}
	msgType, _ := msg["type"].(string)
	switch msgType {
	case "terminal_data":
		b64, _ := msg["data"].(string)
		raw, err := base64Decode(b64)
		if err != nil {
			return
		}
		payload := make([]byte, 1+len(raw))
		payload[0] = MSG_TERMINAL
		copy(payload[1:], raw)
		// P1: 背压入队而非静默丢弃
		if dc.BufferedAmount() >= 64*1024 {
			h.enqueueSessionFrame(session, payload)
		} else {
			dc.Send(payload)
		}
	case "ssh_connect":
		j, _ := json.Marshal(msg)
		payload := make([]byte, 1+len(j))
		payload[0] = MSG_SSH_CONNECT
		copy(payload[1:], j)
		dc.Send(payload)
	case "resize":
		delete(msg, "type")
		delete(msg, "room_id")
		j, _ := json.Marshal(msg)
		payload := make([]byte, 1+len(j))
		payload[0] = MSG_RESIZE
		copy(payload[1:], j)
		dc.Send(payload)
	case "sftp_request":
		j, _ := json.Marshal(msg)
		payload := make([]byte, 1+len(j))
		payload[0] = MSG_SFTP_REQUEST
		copy(payload[1:], j)
		dc.Send(payload)
	case "vnc_connect":
		j, _ := json.Marshal(msg)
		prefix := msgTypeToPrefix(msgType)
		payload := make([]byte, 1+len(j))
		payload[0] = prefix
		copy(payload[1:], j)
		dc.Send(payload)
	case "vnc_input":
		b64, _ := msg["data"].(string)
		raw, err := base64Decode(b64)
		if err != nil {
			log.Printf("[WSS] vnc_input base64 decode error: %v", err)
			return
		}
		payload := make([]byte, 1+len(raw))
		payload[0] = MSG_VNC_INPUT
		copy(payload[1:], raw)
		dc.Send(payload)
	case "vnc_disconnect":
		j, _ := json.Marshal(msg)
		prefix := msgTypeToPrefix(msgType)
		payload := make([]byte, 1+len(j))
		payload[0] = prefix
		copy(payload[1:], j)
		dc.Send(payload)
	case "vnc_error":
		j, _ := json.Marshal(msg)
		prefix := msgTypeToPrefix(msgType)
		payload := make([]byte, 1+len(j))
		payload[0] = prefix
		copy(payload[1:], j)
		dc.Send(payload)
	case "terminal_ack":
		// P3: {type, seq} → binary [0x03][4B BE seq]
		seq, _ := msg["seq"].(float64)
		payload := []byte{MSG_ACK, byte(uint32(seq) >> 24), byte(uint32(seq) >> 16), byte(uint32(seq) >> 8), byte(uint32(seq))}
		dc.Send(payload)
	}
}

func (h *Handler) handleDataChannelMessage(session *Session, data []byte) {
	if len(data) < 1 {
		return
	}
	prefix := data[0]
	payload := data[1:]
	roomID := session.RoomID
	switch prefix {
	case MSG_TERMINAL:
		// Binary frame: 直接转发 [prefix][payload] (零开销)
		select {
		case session.SendCh <- data:
		default:
		}
	case MSG_SFTP_RESPONSE:
		var m map[string]interface{}
		if json.Unmarshal(payload, &m) == nil {
			m["type"] = "sftp_response"
			m["room_id"] = roomID
			j, _ := json.Marshal(m)
			session.SendCh <- j
		}
	case MSG_ERROR:
		var m map[string]interface{}
		if json.Unmarshal(payload, &m) == nil {
			m["type"] = "error"
			m["room_id"] = roomID
			j, _ := json.Marshal(m)
			session.SendCh <- j
		}
	case MSG_VNC_DATA:
		// VNC 是连续 RFB 流：SendCh 满时阻塞等 writePump 消费，绝不丢帧
		// （丢帧会导致浏览器 noVNC zlib inflate failed 黑屏）
		select {
		case session.SendCh <- data:
		case <-session.Done:
		}
	case MSG_VNC_CONNECT:
		var m map[string]interface{}
		if json.Unmarshal(payload, &m) == nil {
			m["type"] = "vnc_connect_result"
			m["room_id"] = roomID
			j, _ := json.Marshal(m)
			select {
			case session.SendCh <- j:
			default:
				log.Printf("[WSS] SendCh full, dropping vnc_connect_result session=%s", session.ID)
			}
		}
	case MSG_VNC_DISCONNECT:
		j, _ := json.Marshal(map[string]interface{}{
			"type":    "vnc_disconnect",
			"room_id": roomID,
		})
		select {
		case session.SendCh <- j:
		default:
		}
	case MSG_DIAGNOSTICS:
		var m map[string]interface{}
		if json.Unmarshal(payload, &m) == nil {
			m["type"] = "agent_diagnostics"
			m["room_id"] = roomID
			j, _ := json.Marshal(m)
			select {
			case session.SendCh <- j:
			default:
			}
		}
	default:
		log.Printf("[G-BRIDGE] unknown prefix: 0x%02x", prefix)
	}
}

func (h *Handler) SetClient(client *signaling.Client) {
	h.client = client
	client.OnMessage(func(msg *signaling.Message) {
		switch msg.Type {
		case signaling.TypeConnectSuccess:
			h.handleConnectSuccess(msg)
		case signaling.TypeAnswer:
			h.handleAnswer(msg)
		case signaling.TypeCandidate:
			h.handleCandidate(msg)
		case signaling.TypeBrowserConnect:
			h.handleBrowserConnect(msg)
		case "upgrade":
			log.Printf("[GW-UPGRADE] 收到升级指令: %s", string(msg.Data))
			go h.handleUpgrade(msg.Data)
		default:
			data, _ := json.Marshal(msg)
			h.mu.RLock()
			for _, session := range h.sessions {
			select {
			case session.SendCh <- data:
			default:
				log.Printf("[WSS] SendCh full, dropping message for session=%s", session.ID)
			}
			}
			h.mu.RUnlock()
		}
	})
}

// handleUpgrade 处理服务端推送的自升级指令
func (h *Handler) handleUpgrade(data json.RawMessage) {
	var req struct {
		Version     string `json:"version"`
		DownloadURL string `json:"download_url"`
	}
	if err := json.Unmarshal(data, &req); err != nil {
		log.Printf("[GW-UPGRADE] 解析升级指令失败: %v", err)
		return
	}
	log.Printf("[GW-UPGRADE] 开始升级: 目标版本=%s 下载地址=%s", req.Version, req.DownloadURL)

	// 1. 下载新二进制到临时文件
	tmpPath := os.Args[0] + ".new"
	resp, err := http.Get(req.DownloadURL)
	if err != nil {
		log.Printf("[GW-UPGRADE] 下载新版本失败: %v", err)
		return
	}
	defer resp.Body.Close()
	if resp.StatusCode != 200 {
		log.Printf("[GW-UPGRADE] 下载新版本失败: HTTP %d", resp.StatusCode)
		return
	}
	out, err := os.Create(tmpPath)
	if err != nil {
		log.Printf("[GW-UPGRADE] 创建临时文件失败: %v", err)
		return
	}
	written, err := io.Copy(out, resp.Body)
	out.Close()
	if err != nil {
		log.Printf("[GW-UPGRADE] 写入临时文件失败: %v", err)
		os.Remove(tmpPath)
		return
	}
	log.Printf("[GW-UPGRADE] 下载完成: %d bytes", written)

	// 2. 设置可执行权限
	if err := os.Chmod(tmpPath, 0755); err != nil {
		log.Printf("[GW-UPGRADE] 设置权限失败: %v", err)
		os.Remove(tmpPath)
		return
	}

	// 3. 备份当前二进制
	backupPath := os.Args[0] + ".bak"
	if err := os.Rename(os.Args[0], backupPath); err != nil {
		log.Printf("[GW-UPGRADE] 备份当前版本失败: %v", err)
		os.Remove(tmpPath)
		return
	}

	// 4. 替换为新版本
	if err := os.Rename(tmpPath, os.Args[0]); err != nil {
		log.Printf("[GW-UPGRADE] 替换二进制失败: %v", err)
		os.Rename(backupPath, os.Args[0])
		return
	}

	log.Printf("[GW-UPGRADE] 升级完成，正在重启...")

	// 5. 重启自身（平台相关实现）
	restartSelf()
}

func (h *Handler) handleBrowserConnect(msg *signaling.Message) {
	roomID := msg.RoomID
	if roomID == "" {
		return
	}
	h.mu.RLock()
	defer h.mu.RUnlock()
	for _, s := range h.sessions {
		s.mu.Lock()
		if s.RoomID == roomID {
			s.mu.Unlock()
			return
		}
		s.mu.Unlock()
	}
}

func (h *Handler) handleConnectSuccess(msg *signaling.Message) {
	roomID := msg.RoomID
	agentID := msg.AgentID
	h.mu.RLock()
	var session *Session
	for _, s := range h.sessions {
		s.mu.Lock()
		if s.RoomID == roomID {
			session = s
			s.mu.Unlock()
			break
		}
		s.mu.Unlock()
	}
	h.mu.RUnlock()
	// R2: 禁止抢占 RoomID=="" 的会话（多浏览器并发会错绑）
	if session == nil {
		log.Printf("[GA-DC] no session for room %s (no empty-room fallback)", roomID)
		return
	}
	// Forward connect_success to browser so signalOk is set (timing step 2-5)
	if csMsg, err := json.Marshal(map[string]interface{}{
		"type":    "connect_success",
		"room_id": roomID,
		"agent_id": agentID,
	}); err == nil {
		select {
		case session.SendCh <- csMsg:
			log.Printf("[GA-DC] forwarded connect_success to browser for room %s", roomID)
		default:
		}
	}
	session.mu.Lock()
	session.RoomID = roomID
	session.AgentID = agentID
	iceServers := []webrtc.ICEServer{
		{URLs: []string{"stun:stun.l.google.com:19302"}},
	}
	if h.client != nil && len(h.client.ICEServers()) > 0 {
		for _, raw := range h.client.ICEServers() {
			var srv webrtc.ICEServer
			if err := json.Unmarshal(raw, &srv); err == nil {
				iceServers = append(iceServers, srv)
			}
		}
	}
	peerConn, err := webrtc.NewPeerConnection(webrtc.Configuration{
		ICEServers: iceServers,
	})
	if err != nil {
		session.mu.Unlock()
		log.Printf("[GA-DC] create PeerConnection error: %v", err)
		return
	}
	// Close old PeerConnection if room is being reused
	if session.PeerConn != nil {
		log.Printf("[GA-DC] closing old PeerConnection for room %s (reuse)", roomID)
		if session.DataChannel != nil {
			session.DataChannel.Close()
			session.DataChannel = nil
		}
		session.PeerConn.Close()
		session.PeerConn = nil
	}
	session.PeerConn = peerConn
	dc, err := peerConn.CreateDataChannel("data", nil)
	if err != nil {
		session.mu.Unlock()
		peerConn.Close()
		log.Printf("[GA-DC] create DataChannel error: %v", err)
		return
	}
	session.DataChannel = dc
	dc.OnOpen(func() {
		log.Printf("[GA-DC] DataChannel open for room %s (API mode)", roomID)

		// resend buffered messages
		session.mu.Lock()
		pending := session.pendingMsgs
		session.pendingMsgs = nil
		session.bpPending = nil
		session.bpBytes = 0
		session.bpDropped = 0
		session.mu.Unlock()
		for _, payload := range pending {
			if err := dc.Send(payload); err != nil {
				log.Printf("[G-BRIDGE] send buffered msg error: %v", err)
			}
		}
		log.Printf("[G-BRIDGE] resent %d buffered messages for room %s", len(pending), roomID)

		// notify browser that DataChannel is ready
		j, _ := json.Marshal(map[string]interface{}{
			"type":    "datachannel_ready",
			"room_id": roomID,
		})
		session.SendCh <- j
	})
	dc.OnMessage(func(msg webrtc.DataChannelMessage) {
		h.handleDataChannelMessage(session, msg.Data)
	})
	dc.OnClose(func() {
		log.Printf("[GA-DC] DataChannel closed room=%s", roomID)
		session.mu.Lock()
		session.DataChannel = nil
		session.mu.Unlock()
	})
	peerConn.OnICECandidate(func(candidate *webrtc.ICECandidate) {
		if candidate == nil {
			return
		}
		candidateJSON := candidate.ToJSON()
		data, _ := json.Marshal(candidateJSON)
		h.client.SendMessage(&signaling.Message{
			Type:      signaling.TypeCandidate,
			RoomID:    roomID,
			AgentID:   agentID,
			GatewayID: h.cfg.GatewayID,
			Candidate: data,
		})
	})
	peerConn.OnICEConnectionStateChange(func(state webrtc.ICEConnectionState) {
		log.Printf("[GA-DC] ICE room=%s: %s", roomID, state.String())
		switch state {
		case webrtc.ICEConnectionStateFailed:
			peerConn.Close()
			j, _ := json.Marshal(map[string]interface{}{
				"type":    "error",
				"room_id": roomID,
				"detail":  "WebRTC连接断开: " + state.String(),
			})
			session.SendCh <- j
		}
	})
	offer, err := peerConn.CreateOffer(nil)
	if err != nil {
		session.mu.Unlock()
		peerConn.Close()
		log.Printf("[GA-DC] create offer error: %v", err)
		return
	}
	if err := peerConn.SetLocalDescription(offer); err != nil {
		session.mu.Unlock()
		peerConn.Close()
		log.Printf("[GA-DC] set local desc error: %v", err)
		return
	}
	session.mu.Unlock()
	sdpJSON, _ := json.Marshal(peerConn.LocalDescription())
	h.client.SendMessage(&signaling.Message{
		Type:      signaling.TypeOffer,
		RoomID:    roomID,
		AgentID:   agentID,
		GatewayID: h.cfg.GatewayID,
		SDP:       sdpJSON,
	})
	log.Printf("[GA-DC] offer sent room=%s", roomID)
}

func (h *Handler) handleAnswer(msg *signaling.Message) {
	roomID := msg.RoomID
	h.mu.RLock()
	var session *Session
	for _, s := range h.sessions {
		s.mu.Lock()
		if s.RoomID == roomID {
			session = s
			s.mu.Unlock()
			break
		}
		s.mu.Unlock()
	}
	h.mu.RUnlock()
	if session == nil {
		log.Printf("[GA-DC] no session for answer room=%s", roomID)
		return
	}
	session.mu.Lock()
	defer session.mu.Unlock()
	if session.PeerConn == nil {
		return
	}
	var sdp webrtc.SessionDescription
	if err := json.Unmarshal(msg.SDP, &sdp); err != nil {
		log.Printf("[GA-DC] unmarshal answer SDP error: %v", err)
		return
	}
	if err := session.PeerConn.SetRemoteDescription(sdp); err != nil {
		log.Printf("[GA-DC] set remote desc error: %v", err)
	}
}

func (h *Handler) handleCandidate(msg *signaling.Message) {
	roomID := msg.RoomID
	h.mu.RLock()
	var session *Session
	for _, s := range h.sessions {
		s.mu.Lock()
		if s.RoomID == roomID {
			session = s
			s.mu.Unlock()
			break
		}
		s.mu.Unlock()
	}
	h.mu.RUnlock()
	if session == nil {
		return
	}
	session.mu.Lock()
	defer session.mu.Unlock()
	if session.PeerConn == nil {
		return
	}
	var candidate webrtc.ICECandidateInit
	if err := json.Unmarshal(msg.Candidate, &candidate); err != nil {
		return
	}
	session.PeerConn.AddICECandidate(candidate)
}

// deriveVerifyBase 后端 /api/auth/verify 基址（token 走 Authorization 头，不进 query/log）
func (h *Handler) deriveVerifyBase() string {
	su := h.cfg.ServerURL
	if strings.HasPrefix(su, "wss://") {
		su = "https://" + strings.TrimPrefix(su, "wss://")
	} else if strings.HasPrefix(su, "ws://") {
		su = "http://" + strings.TrimPrefix(su, "ws://")
	}
	idx := strings.Index(su, "/api/")
	if idx > 0 {
		su = su[:idx]
	}
	return su + "/api/auth/verify"
}

func (h *Handler) verifyToken(token string) error {
	verifyURL := h.deriveVerifyBase()
	client := &http.Client{Timeout: 5 * time.Second}
	if h.cfg.InsecureSkipVerify {
		client.Transport = &http.Transport{
			TLSClientConfig: &tls.Config{InsecureSkipVerify: true},
		}
	}
	req, err := http.NewRequest(http.MethodGet, verifyURL, nil)
	if err != nil {
		return fmt.Errorf("verify request build failed: %w", err)
	}
	req.Header.Set("Authorization", "Bearer "+token)
	resp, err := client.Do(req)
	if err != nil {
		log.Printf("[WSS] verify token request failed: %v", err)
		return fmt.Errorf("verify request failed: %w", err)
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(resp.Body)
	if resp.StatusCode != 200 {
		log.Printf("[WSS] token verify failed: status=%d body=%s", resp.StatusCode, string(body))
		return fmt.Errorf("token invalid: status %d", resp.StatusCode)
	}
	var result map[string]interface{}
	if err := json.Unmarshal(body, &result); err != nil {
		return fmt.Errorf("invalid verify response: %w", err)
	}
	log.Printf("[WSS] token verified for user %s", result["username"])
	return nil
}

func base64Encode(data []byte) string {
	return base64.StdEncoding.EncodeToString(data)
}

func base64Decode(s string) ([]byte, error) {
	return base64.StdEncoding.DecodeString(s)
}
