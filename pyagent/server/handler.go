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
	"sync/atomic"
	"time"

	"github.com/gorilla/websocket"
	"github.com/pion/webrtc/v4"

	"github.com/ppy-tools/pyagent/config"
	"github.com/ppy-tools/pyagent/icefilter"
	"github.com/ppy-tools/pyagent/pathcache"
	"github.com/ppy-tools/pyagent/signaling"
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
	MSG_TERMINAL       byte = 0x00
	MSG_SSH_CONNECT    byte = 0x01
	MSG_RESIZE         byte = 0x02
	MSG_ACK            byte = 0x03 // P3: 终端输出累计确认
	MSG_SFTP_REQUEST   byte = 0x10
	MSG_SFTP_RESPONSE  byte = 0x11
	MSG_VNC_CONNECT    byte = 0x20
	MSG_VNC_DATA       byte = 0x21
	MSG_VNC_DISCONNECT byte = 0x22
	MSG_VNC_INPUT      byte = 0x23
	MSG_VNC_ERROR      byte = 0x2F
	MSG_ERROR          byte = 0xFF
	MSG_DIAGNOSTICS    byte = 0xFE
)

func msgTypeToPrefix(msgType string) byte {
	switch msgType {
	case "terminal_data":
		return MSG_TERMINAL
	case "ssh_connect":
		return MSG_SSH_CONNECT
	case "resize":
		return MSG_RESIZE
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
	ID        string
	RoomID    string
	AgentID   string
	GatewayID string
	Conn      *websocket.Conn
	SendCh    chan []byte
	Done      chan struct{}
	// P1: SendCh 在途字节数（入队 +len / 出队 -len），配合 sendByteQuota 背压。
	// 旧实现只按帧数限流 4096 帧 × VNC 60KB ≈ 240MB 峰值。
	sendBytes   atomic.Int64
	PeerConn    *webrtc.PeerConnection
	DataChannel *webrtc.DataChannel
	pendingMsgs [][]byte
	// P1: DC 背压待发队列（浏览器→agent 终端帧）
	bpPending [][]byte
	bpBytes   int
	bpDropped uint64
	bpFlush   bool
	// C: answer(remote description) 前到达的 ICE 候选缓冲（与 wragent 侧 pendingCandidates 对齐）
	pendingCandidates []webrtc.ICECandidateInit
	// P2: 三级回退阶梯（方案 §4.5）
	gen           atomic.Int32 // 世代号：每次重建 PC 递增，旧回调据此失效（原子读，避免回调持锁死锁）
	iceLevel      int          // 当前等级 1/2/3
	iceRebuilds   int          // 本会话已重建次数（上限 maxRebuild）
	autoFallback  bool         // 后台 config_json.ice.auto_fallback（缺省开）
	pathCacheOn   bool         // 后台 config_json.ice.path_cache（缺省开）
	answerApplied bool         // 当前世代的 answer 是否已生效（L1 窗口起点）
	connected     bool         // 当前世代是否已选出候选对
	iceL1Timer    *time.Timer  // L1 窗口定时器
	mu            sync.Mutex
}

// sendByteQuota SendCh 字节配额。原按帧数(4096)限流在 VNC 大帧下等于 240MB 峰值，
// 改为字节配额后慢消费者被卡在 DC 回调里形成真实背压（网关 RSS 峰值 240MB → 8MB）。
const sendByteQuota = 8 << 20

// enqueue 投递到 SendCh。block=true 时配额满则等 writePump 消费（VNC/SFTP 不丢帧），
// block=false 时配额或队列满即丢弃（终帧/诊断/广播可丢）。
// 任何情况下都会被 session.Done 打断，绝不永久阻塞 ——
// 原实现存在 session.SendCh <- j 的裸投递，会在 DC 断连瞬间永久挂起 pion 回调。
func (s *Session) enqueue(data []byte, block bool) bool {
	n := int64(len(data))
	for {
		if s.sendBytes.Load()+n <= sendByteQuota {
			select {
			case s.SendCh <- data:
				s.sendBytes.Add(n)
				return true
			default:
			}
		}
		if !block {
			return false
		}
		select {
		case <-s.Done:
			return false
		case <-time.After(50 * time.Millisecond):
		}
	}
}

type Handler struct {
	cfg      *config.Config
	client   *signaling.Client
	sessions map[string]*Session
	mu       sync.RWMutex
	// P2: 路径缓存与阶梯状态（进程级，按 agent 维度）
	pathCache   *pathcache.Cache
	ladderState *ladder
}

func NewHandler(cfg *config.Config) *Handler {
	return &Handler{
		cfg:         cfg,
		sessions:    make(map[string]*Session),
		pathCache:   pathcache.Default(),
		ladderState: newLadder(),
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
			if dc == nil || dc.ReadyState() != webrtc.DataChannelStateOpen {
				// DC 未就绪: 缓冲待 OnOpen 补发, 不静默丢弃 (与 JSON 路径一致)
				h.bufferBridgePayload(session, fmt.Sprintf("bin:0x%02x", data[0]), data)
				continue
			}
			// P1: 终端帧背压入队，其余直发（含 P3 ack 0x03）
			if data[0] == MSG_TERMINAL && dc.BufferedAmount() >= 64*1024 {
				h.enqueueSessionFrame(session, data)
			} else if err := dc.Send(data); err != nil {
				log.Printf("[WSS] DC send bin:0x%02x error: %v, buffering", data[0], err)
				h.bufferBridgePayload(session, fmt.Sprintf("bin:0x%02x", data[0]), data)
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
		case "terminal_data", "ssh_connect", "resize", "vnc_connect", "vnc_disconnect", "vnc_input", "vnc_error", "terminal_ack":
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
			session.sendBytes.Add(-int64(len(msg)))
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
				// 关连接 → readPump 退出 → close(session.Done) 统一收尾，
				// 否则 SendCh 不再被消费，阻塞投递方只能等浏览器断开
				_ = session.Conn.Close()
				return
			}
		case <-session.Done:
			return
		}
	}
}

// bufferBridgePayload DC 未就绪时挂到 pendingMsgs, OnOpen 时统一补发 (绝不静默丢弃)
func (h *Handler) bufferBridgePayload(session *Session, msgType string, payload []byte) {
	session.mu.Lock()
	const maxPendingMsgs = 4096
	if len(session.pendingMsgs) >= maxPendingMsgs {
		session.mu.Unlock()
		log.Printf("[WSS] pendingMsgs limit reached, dropping %s", msgType)
		return
	}
	session.pendingMsgs = append(session.pendingMsgs, payload)
	qLen := len(session.pendingMsgs)
	dc := session.DataChannel
	session.mu.Unlock()
	state := "nil"
	if dc != nil {
		state = dc.ReadyState().String()
	}
	log.Printf("[WSS] DataChannel not ready(state=%s), buffered %s (queue=%d)", state, msgType, qLen)
}

// sendOrBuffer 向 Agent DC 发送; DC 未 Open 或 Send 失败时缓冲。
// pion v4 Send 未 Open 时返回 ensureOpen 错误 — 必须检查, 否则 vnc_connect/ssh_connect
// 在兜底 onOpen 提前触发时会被静默丢弃 (VNC 网关模式 3 次重试全丢的根因)。
func (h *Handler) sendOrBuffer(session *Session, msgType string, payload []byte) {
	session.mu.Lock()
	dc := session.DataChannel
	session.mu.Unlock()
	if dc == nil || dc.ReadyState() != webrtc.DataChannelStateOpen {
		h.bufferBridgePayload(session, msgType, payload)
		return
	}
	if err := dc.Send(payload); err != nil {
		log.Printf("[WSS] DC send %s error: %v, buffering", msgType, err)
		h.bufferBridgePayload(session, msgType, payload)
	}
}

func (h *Handler) bridgeWSToDataChannel(session *Session, msg map[string]interface{}) {
	msgType, _ := msg["type"].(string)
	session.mu.Lock()
	dc := session.DataChannel
	session.mu.Unlock()
	// 判定条件必须含 ReadyState: session.DataChannel 在 CreateDataChannel 时即赋值,
	// 早于 OnOpen 十余秒 (ICE 慢时), 仅判 nil 会把消息打进未 Open 的通道。
	if dc == nil || dc.ReadyState() != webrtc.DataChannelStateOpen {
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
		h.bufferBridgePayload(session, msgType, payload)
		return
	}
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
			h.sendOrBuffer(session, msgType, payload)
		}
	case "ssh_connect":
		j, _ := json.Marshal(msg)
		payload := make([]byte, 1+len(j))
		payload[0] = MSG_SSH_CONNECT
		copy(payload[1:], j)
		h.sendOrBuffer(session, msgType, payload)
	case "resize":
		delete(msg, "type")
		delete(msg, "room_id")
		j, _ := json.Marshal(msg)
		payload := make([]byte, 1+len(j))
		payload[0] = MSG_RESIZE
		copy(payload[1:], j)
		h.sendOrBuffer(session, msgType, payload)
	case "vnc_connect":
		j, _ := json.Marshal(msg)
		prefix := msgTypeToPrefix(msgType)
		payload := make([]byte, 1+len(j))
		payload[0] = prefix
		copy(payload[1:], j)
		h.sendOrBuffer(session, msgType, payload)
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
		h.sendOrBuffer(session, msgType, payload)
	case "vnc_disconnect":
		j, _ := json.Marshal(msg)
		prefix := msgTypeToPrefix(msgType)
		payload := make([]byte, 1+len(j))
		payload[0] = prefix
		copy(payload[1:], j)
		h.sendOrBuffer(session, msgType, payload)
	case "vnc_error":
		j, _ := json.Marshal(msg)
		prefix := msgTypeToPrefix(msgType)
		payload := make([]byte, 1+len(j))
		payload[0] = prefix
		copy(payload[1:], j)
		h.sendOrBuffer(session, msgType, payload)
	case "terminal_ack":
		// P3: {type, seq} → binary [0x03][4B BE seq]
		seq, _ := msg["seq"].(float64)
		payload := []byte{MSG_ACK, byte(uint32(seq) >> 24), byte(uint32(seq) >> 16), byte(uint32(seq) >> 8), byte(uint32(seq))}
		h.sendOrBuffer(session, msgType, payload)
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
		session.enqueue(data, false)
	case MSG_SFTP_RESPONSE:
		// T1.3: SFTP 已改二进制，这里零解析零重编码，原样转发（可靠投递，不丢弃）
		session.enqueue(data, true)
	case MSG_ERROR:
		var m map[string]interface{}
		if json.Unmarshal(payload, &m) == nil {
			m["type"] = "error"
			m["room_id"] = roomID
			j, _ := json.Marshal(m)
			session.enqueue(j, true)
		}
	case MSG_VNC_DATA:
		// VNC 是连续 RFB 流：字节配额满时阻塞等 writePump 消费，绝不丢帧
		// （丢帧会导致浏览器 noVNC zlib inflate failed 黑屏）
		session.enqueue(data, true)
	case MSG_VNC_CONNECT:
		var m map[string]interface{}
		if json.Unmarshal(payload, &m) == nil {
			m["type"] = "vnc_connect_result"
			m["room_id"] = roomID
			j, _ := json.Marshal(m)
			// 关键 ACK: 阻塞投递 (镜像 MSG_VNC_DATA 策略), 丢一次浏览器即超时
			session.enqueue(j, true)
		}
	case MSG_VNC_DISCONNECT:
		j, _ := json.Marshal(map[string]interface{}{
			"type":    "vnc_disconnect",
			"room_id": roomID,
		})
		session.enqueue(j, true)
	case MSG_DIAGNOSTICS:
		var m map[string]interface{}
		if json.Unmarshal(payload, &m) == nil {
			m["type"] = "agent_diagnostics"
			m["room_id"] = roomID
			j, _ := json.Marshal(m)
			session.enqueue(j, false)
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
				if !session.enqueue(data, false) {
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

	// 4.5 持久化到宿主路径(PERSIST_BIN): 容器重启/重建后不回退版本
	if dst := os.Getenv("PERSIST_BIN"); dst != "" && dst != os.Args[0] {
		if err := gwPersistBinary(os.Args[0], dst); err != nil {
			log.Printf("[GW-UPGRADE] 持久化二进制失败(不影响本次升级): %v", err)
		} else {
			log.Printf("[GW-UPGRADE] 已持久化二进制到 %s", dst)
		}
	}

	log.Printf("[GW-UPGRADE] 升级完成，正在重启...")

	// 5. 重启自身（平台相关实现）
	restartSelf()
}

// gwPersistBinary 将新二进制持久化到目标路径: 先同目录 tmp+rename(原子), 失败(文件挂载点)回退流式覆盖
func gwPersistBinary(src, dst string) error {
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

// parseIcePolicy 解析 md 随 connect_success 下发的阶梯策略（config_json.ice）。
// 后台字段优先，网关配置/环境变量 WRG_ICE_AUTO_FALLBACK / WRG_ICE_PATH_CACHE 再兜底一次（D3）。
func (h *Handler) parseIcePolicy(msg *signaling.Message) (autoFallback, pathCacheOn bool) {
	autoFallback, pathCacheOn = true, true
	if len(msg.IcePolicy) > 0 {
		var p struct {
			AutoFallback *bool `json:"auto_fallback"`
			PathCache    *bool `json:"path_cache"`
		}
		if err := json.Unmarshal(msg.IcePolicy, &p); err == nil {
			if p.AutoFallback != nil {
				autoFallback = *p.AutoFallback
			}
			if p.PathCache != nil {
				pathCacheOn = *p.PathCache
			}
		}
	}
	if h.cfg != nil {
		autoFallback = autoFallback && h.cfg.IceAutoFallback
		pathCacheOn = pathCacheOn && h.cfg.IcePathCache
	}
	return autoFallback, pathCacheOn
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
		"type":     "connect_success",
		"room_id":  roomID,
		"agent_id": agentID,
	}); err == nil {
		if session.enqueue(csMsg, false) {
			log.Printf("[GA-DC] forwarded connect_success to browser for room %s", roomID)
		}
	}
	autoFallback, pathCacheOn := h.parseIcePolicy(msg)
	level := h.startLevel(agentID, pathCacheOn)
	session.mu.Lock()
	defer session.mu.Unlock()
	session.RoomID = roomID
	session.AgentID = agentID
	session.autoFallback = autoFallback
	session.pathCacheOn = pathCacheOn
	session.iceRebuilds = 0
	h.startPeerLocked(session, roomID, agentID, level, "connect_success")
}

// startPeerLocked 建立（或按阶梯重建）网关↔Agent 的 PeerConnection 并发送 offer。
// 调用方必须持有 session.mu；每次重建递增世代号 gen，旧 PeerConnection 的所有回调
// 用 gen 自检后静默退出，避免旧会话的 answer/候选/DC 帧串到新会话。
func (h *Handler) startPeerLocked(session *Session, roomID, agentID string, level int, cause string) {
	select {
	case <-session.Done:
		return // 浏览器已断开，不再重建
	default:
	}
	gen := int(session.gen.Add(1))
	if session.iceL1Timer != nil {
		session.iceL1Timer.Stop()
		session.iceL1Timer = nil
	}
	session.answerApplied = false
	session.connected = false
	session.pendingCandidates = nil
	session.iceLevel = level
	if cause != "connect_success" {
		log.Printf("[ICE-LADDER] room=%s 重建为%s (cause=%s)", roomID, levelName(level), cause)
	}
	// L1: 服务端下发的 ICE 配置优先(本机 STUN 前置, 且服务端会过滤探测不可达的 STUN)；
	// 仅在完全没拿到配置时才回退 Google STUN —— 国内不可达会让候选收集白等超时(实测约 3s)
	iceServers := make([]webrtc.ICEServer, 0, 4)
	if h.client != nil && len(h.client.ICEServers()) > 0 {
		for _, raw := range h.client.ICEServers() {
			var srv webrtc.ICEServer
			if err := json.Unmarshal(raw, &srv); err == nil {
				iceServers = append(iceServers, srv)
			}
		}
	}
	if len(iceServers) == 0 {
		iceServers = append(iceServers, webrtc.ICEServer{
			URLs: []string{"stun:stun.l.google.com:19302"},
		})
	}
	// ICE 接口过滤（P0, 方案 §4.2）+ P2 等级选择（L1 只放行缓存接口 / L3 不过滤）
	se := webrtc.SettingEngine{}
	if pred := h.levelPredicate(level, agentID); pred != nil {
		se.SetInterfaceFilter(pred)
	}
	peerConn, err := webrtc.NewAPI(webrtc.WithSettingEngine(se)).NewPeerConnection(webrtc.Configuration{
		ICEServers: iceServers,
	})
	if err != nil {
		log.Printf("[GA-DC] create PeerConnection error: %v", err)
		// 新建失败且世代已前移，旧 PC 的回调已失效 → 会话作废，避免留下僵尸连接
		if session.DataChannel != nil {
			session.DataChannel.Close()
			session.DataChannel = nil
		}
		if session.PeerConn != nil {
			session.PeerConn.Close()
			session.PeerConn = nil
		}
		return
	}
	peerConn.OnICEGatheringStateChange(func(state webrtc.ICEGatheringState) {
		if state != webrtc.ICEGatheringStateComplete {
			return
		}
		log.Printf("[ICE-FILTER] gathering complete: %s local_candidates=%d level=%s",
			icefilter.Summary(), countLocalCandidates(peerConn), levelName(level))
	})
	// Close old PeerConnection if room is being reused / ladder rebuild
	if session.PeerConn != nil {
		log.Printf("[GA-DC] closing old PeerConnection for room %s (reuse)", roomID)
		if session.DataChannel != nil {
			session.DataChannel.Close()
			session.DataChannel = nil
		}
		// 旧 PC 的 gather 可能仍卡在 STUN/TURN 拨号上（Close 会等其收尾，实测最长 ~105s），
		// 持 session.mu 同步关会堵死 answer/候选处理 → 阶梯重建失败 → 换后异步关。
		oldPC := session.PeerConn
		session.PeerConn = nil
		go oldPC.Close()
	}
	session.PeerConn = peerConn
	dc, err := peerConn.CreateDataChannel("data", nil)
	if err != nil {
		go peerConn.Close()
		session.PeerConn = nil
		log.Printf("[GA-DC] create DataChannel error: %v", err)
		return
	}
	session.DataChannel = dc
	dc.OnOpen(func() {
		if !session.isGen(gen) {
			return
		}
		log.Printf("[GA-DC] DataChannel open for room %s (API mode)", roomID)

		// resend buffered messages
		session.mu.Lock()
		if session.gen.Load() != int32(gen) {
			session.mu.Unlock()
			return
		}
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
		session.enqueue(j, true)

		// 网关模式下浏览器没有本地 RTCPeerConnection，无法自取 stats 判定 P2P/relay，
		// 由网关代为判定 网关↔Agent 的 ICE 选中候选对并回传浏览器补报 timeline。
		go reportConnType(session, peerConn, roomID)
	})
	dc.OnMessage(func(msg webrtc.DataChannelMessage) {
		if !session.isGen(gen) {
			return // 旧世代 DC 的迟到帧
		}
		h.handleDataChannelMessage(session, msg.Data)
	})
	dc.OnClose(func() {
		if !session.isGen(gen) {
			return
		}
		log.Printf("[GA-DC] DataChannel closed room=%s", roomID)
		session.mu.Lock()
		if session.gen.Load() == int32(gen) {
			session.DataChannel = nil
		}
		session.mu.Unlock()
	})
	peerConn.OnICECandidate(func(candidate *webrtc.ICECandidate) {
		if candidate == nil || !session.isGen(gen) {
			return
		}
		candidateJSON := candidate.ToJSON()
		data, _ := json.Marshal(candidateJSON)
		if h.client == nil {
			return
		}
		h.client.SendMessage(&signaling.Message{
			Type:      signaling.TypeCandidate,
			RoomID:    roomID,
			AgentID:   agentID,
			GatewayID: h.cfg.GatewayID,
			Candidate: data,
		})
	})
	peerConn.OnICEConnectionStateChange(func(state webrtc.ICEConnectionState) {
		log.Printf("[GA-DC] ICE room=%s: %s (level=%s)", roomID, state.String(), levelName(level))
		switch state {
		case webrtc.ICEConnectionStateConnected, webrtc.ICEConnectionStateCompleted:
			session.mu.Lock()
			if session.gen.Load() != int32(gen) {
				session.mu.Unlock()
				return
			}
			session.connected = true
			if session.iceL1Timer != nil {
				session.iceL1Timer.Stop()
				session.iceL1Timer = nil
			}
			session.mu.Unlock()
			go h.recordPath(session, gen, roomID, agentID, level)
			h.ladderSuccess(agentID)
		case webrtc.ICEConnectionStateFailed:
			session.mu.Lock()
			if session.gen.Load() != int32(gen) {
				session.mu.Unlock()
				return
			}
			select {
			case <-session.Done:
				session.mu.Unlock()
				return
			default:
			}
			if h.escalateLocked(session, gen, roomID, agentID, "ice_failed") {
				session.mu.Unlock()
				return
			}
			h.sendConnectError(session, roomID, "WebRTC连接断开: "+state.String())
			session.mu.Unlock()
			h.ladderFailure(agentID)
		}
	})
	offer, err := peerConn.CreateOffer(nil)
	if err != nil {
		go peerConn.Close()
		session.PeerConn = nil
		log.Printf("[GA-DC] create offer error: %v", err)
		return
	}
	if err := peerConn.SetLocalDescription(offer); err != nil {
		go peerConn.Close()
		session.PeerConn = nil
		log.Printf("[GA-DC] set local desc error: %v", err)
		return
	}
	sdpJSON, _ := json.Marshal(peerConn.LocalDescription())
	if h.client == nil {
		return
	}
	h.client.SendMessage(&signaling.Message{
		Type:      signaling.TypeOffer,
		RoomID:    roomID,
		AgentID:   agentID,
		GatewayID: h.cfg.GatewayID,
		SDP:       sdpJSON,
		IceLevel:  level,
	})
	log.Printf("[GA-DC] offer sent room=%s level=%s", roomID, levelName(level))
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
		return
	}
	log.Printf("[GA-DC] answer applied room=%s (embedded candidates=%d)", roomID, strings.Count(sdp.SDP, "a=candidate"))
	// C: answer 生效后补发缓冲的 ICE 候选（agent 候选常先于 answer 到达，
	// 此前 AddICECandidate 因无 remote description 直接失败被静默丢弃 → 只能靠
	// STUN binding 收敛(remote=prflx)，ICE 耗时 14~15s）
	for _, c := range session.pendingCandidates {
		if err := session.PeerConn.AddICECandidate(c); err != nil {
			log.Printf("[GA-DC] flush buffered ICE candidate room=%s error: %v", roomID, err)
		}
	}
	if n := len(session.pendingCandidates); n > 0 {
		log.Printf("[GA-DC] flushed %d buffered ICE candidates room=%s", n, roomID)
		session.pendingCandidates = nil
	}
	// P2 §4.5 L1 快路径窗口：answer 生效即开始计时（此时远端候选才齐，连通性检查才真正开始），
	// 800ms 内未选出候选对 → 回落 L2 重建。窗口未到期前不干预。
	session.answerApplied = true
	gen := session.currentGen()
	if session.iceLevel == iceLevelL1 && session.autoFallback && session.iceL1Timer == nil {
		agentID := session.AgentID
		session.iceL1Timer = time.AfterFunc(l1Window, func() {
			h.l1WindowExpired(session, gen, roomID, agentID)
		})
		log.Printf("[ICE-LADDER] room=%s 进入 L1 快路径（窗口 %v）", roomID, l1Window)
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
		log.Printf("[GA-DC] no session for ICE candidate room=%s (dropped)", roomID)
		return
	}
	session.mu.Lock()
	defer session.mu.Unlock()
	if session.PeerConn == nil {
		log.Printf("[GA-DC] peer nil for ICE candidate room=%s (dropped)", roomID)
		return
	}
	var candidate webrtc.ICECandidateInit
	if err := json.Unmarshal(msg.Candidate, &candidate); err != nil {
		log.Printf("[GA-DC] unmarshal ICE candidate error room=%s: %v", roomID, err)
		return
	}
	if err := session.PeerConn.AddICECandidate(candidate); err != nil {
		// remote description 未就绪（answer 未到）时缓冲，answer 生效后补发（C）
		log.Printf("[GA-DC] ICE candidate buffered room=%s: %v", roomID, err)
		session.pendingCandidates = append(session.pendingCandidates, candidate)
	}
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

func base64Decode(s string) ([]byte, error) {
	return base64.StdEncoding.DecodeString(s)
}

// ── 网关↔Agent DC 链路标注 (P2P/relay/BUG) ─────────────────────────────────
//
// 浏览器在网关模式下只与网关走 WSS、本地没有 RTCPeerConnection，无法自取
// RTC stats 判定链路类型；因此由网关判定 网关↔Agent 的 ICE 选中候选对，
// 经 session.SendCh 回传浏览器，浏览器再 POST /api/timeline/webrtc-path
// 落库 —— 与直连模式共用 connection_timeline.webrtc_path 同一列。

// selectedCandidatePair 取 ICE 选中候选对；未就绪/取失败返回 nil。
func selectedCandidatePair(pc *webrtc.PeerConnection) *webrtc.ICECandidatePair {
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

// classifyConnType 与浏览器 parseConnType 规则对齐：
// 任一端 relay → relay；两端均为 host/srflx/prflx → P2P；其余 → BUG。
func classifyConnType(local, remote webrtc.ICECandidateType) string {
	if local == webrtc.ICECandidateTypeRelay || remote == webrtc.ICECandidateTypeRelay {
		return "relay"
	}
	if isDirectCandidate(local) && isDirectCandidate(remote) {
		return "P2P"
	}
	return "BUG"
}

func isDirectCandidate(t webrtc.ICECandidateType) bool {
	switch t {
	case webrtc.ICECandidateTypeHost, webrtc.ICECandidateTypeSrflx, webrtc.ICECandidateTypePrflx:
		return true
	}
	return false
}

func sendConnType(session *Session, roomID, connType string, pair *webrtc.ICECandidatePair) {
	j, _ := json.Marshal(map[string]interface{}{
		"type":      "connection_type",
		"room_id":   roomID,
		"conn_type": connType,
	})
	if session.enqueue(j, false) {
		if pair != nil {
			log.Printf("[GA-DC] conn_type room=%s %s (local=%s remote=%s)",
				roomID, connType, pair.Local.Typ, pair.Remote.Typ)
		} else {
			log.Printf("[GA-DC] conn_type room=%s %s (no selected pair)", roomID, connType)
		}
	} else {
		log.Printf("[GA-DC] conn_type room=%s %s dropped (send buffer full)", roomID, connType)
	}
}

// reportConnType DC 打开后轮询选中候选对（含稳定期，防 srflx→relay 瞬时切换），
// 10s 仍取不到则判 BUG；会话关闭或 PC 被关闭（房间复用）则静默退出。
func reportConnType(session *Session, peerConn *webrtc.PeerConnection, roomID string) {
	const settle = 1500 * time.Millisecond
	deadline := time.Now().Add(10 * time.Second)
	var firstFound time.Time
	for time.Now().Before(deadline) {
		select {
		case <-session.Done:
			return
		default:
		}
		if peerConn.ConnectionState() == webrtc.PeerConnectionStateClosed {
			return
		}
		pair := selectedCandidatePair(peerConn)
		if pair == nil {
			time.Sleep(250 * time.Millisecond)
			continue
		}
		if firstFound.IsZero() {
			firstFound = time.Now()
		}
		if time.Since(firstFound) < settle {
			time.Sleep(200 * time.Millisecond)
			continue
		}
		if latest := selectedCandidatePair(peerConn); latest != nil {
			pair = latest
		}
		sendConnType(session, roomID, classifyConnType(pair.Local.Typ, pair.Remote.Typ), pair)
		return
	}
	sendConnType(session, roomID, "BUG", nil)
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
	if d := pc.CurrentLocalDescription(); d != nil {
		return strings.Count(d.SDP, "\na=candidate:")
	}
	return 0
}
