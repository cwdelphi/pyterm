// handler_pump.go — 由 handler.go 按域拆分（R1/R2 纯移动，无逻辑变更）。
package server

import (
	"encoding/json"
	"fmt"
	"log"
	"time"

	"github.com/gorilla/websocket"
	"github.com/pion/webrtc/v4"
)

func (h *Handler) readPump(session *Session) {
	defer func() {
		session.mu.Lock()
		roomID := session.RoomID
		session.mu.Unlock()
		h.mu.Lock()
		if roomID != "" && h.roomToSession[roomID] == session {
			delete(h.roomToSession, roomID)
		}
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
				session.logRL.Printf("[WSS] DC send bin:0x%02x error: %v, buffering", data[0], err)
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
				h.bindRoom(session, rid)
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
