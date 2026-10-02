// handler_bridge.go — 由 handler.go 按域拆分（R1/R2 纯移动，无逻辑变更）。
package server

import (
	"encoding/json"
	"log"
	"sync"
	"time"

	"github.com/pion/webrtc/v4"
)

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

// rateLogger 热路径 5s 限频日志（P2 #14）：DC 未就绪/队列满等每帧打一条会淹没日志，
// 且绕过级别短路走全局锁+syscall。首条直打，之后 5s 内聚合计数，窗口首条附带抑制数。
type rateLogger struct {
	mu         sync.Mutex
	last       time.Time
	suppressed int
}

func (r *rateLogger) Printf(format string, args ...any) {
	r.mu.Lock()
	now := time.Now()
	if !r.last.IsZero() && now.Sub(r.last) < 5*time.Second {
		r.suppressed++
		r.mu.Unlock()
		return
	}
	n := r.suppressed
	r.suppressed = 0
	r.last = now
	r.mu.Unlock()
	if n > 0 {
		log.Printf(format, args...)
		log.Printf("[rate-limit] 另有 %d 条同类日志在 5s 窗口内被抑制", n)
		return
	}
	log.Printf(format, args...)
}

// gBridgeLogRL G-BRIDGE 前缀类热路径限频（无 session 归属）
var gBridgeLogRL rateLogger

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

// bufferBridgePayload DC 未就绪时挂到 pendingMsgs, OnOpen 时统一补发 (绝不静默丢弃)
func (h *Handler) bufferBridgePayload(session *Session, msgType string, payload []byte) {
	session.mu.Lock()
	const maxPendingMsgs = 4096
	if len(session.pendingMsgs) >= maxPendingMsgs {
		session.mu.Unlock()
		session.logRL.Printf("[WSS] pendingMsgs limit reached, dropping %s", msgType)
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
	session.logRL.Printf("[WSS] DataChannel not ready(state=%s), buffered %s (queue=%d)", state, msgType, qLen)
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
		session.logRL.Printf("[WSS] DC send %s error: %v, buffering", msgType, err)
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
			session.logRL.Printf("[WSS] DataChannel not ready, unknown type %s", msgType)
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
		gBridgeLogRL.Printf("[G-BRIDGE] unknown prefix: 0x%02x", prefix)
	}
}
