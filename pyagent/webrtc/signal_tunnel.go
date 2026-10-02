// signal_tunnel.go — 由 signal.go 按域拆分（R1/R2 纯移动，无逻辑变更）。
package webrtc

import (
	"encoding/json"
	"fmt"
	"log"
	"net"
	"strconv"
	"sync"
	"time"

	"github.com/pion/webrtc/v4"
)

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

	// P2 #8: 先挂关闭回调再入表；并补关同房旧 Peer（此前隧道换代只覆盖不关闭 → 泄漏）
	peer.OnClose(func() { h.forgetPeer(roomID, peer) })
	h.mu.Lock()
	oldTunnelPeer := h.p[roomID]
	h.p[roomID] = peer
	delete(h.pendingCandidates, roomID)
	h.mu.Unlock()
	if oldTunnelPeer != nil && oldTunnelPeer != peer {
		go oldTunnelPeer.Close()
	}

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
	// P2 #11: 帧缓冲池化（pion Send 同步拷贝，返回即可归还）
	msg := takeFrameBuf(3 + len(payload))
	msg[0] = prefix
	msg[1] = byte(connID >> 8)
	msg[2] = byte(connID)
	copy(msg[3:], payload)
	err := sendFrameDC(dc, msg)
	if err != nil {
		err = fmt.Errorf("dc send failed (prefix=0x%02x connID=%d len=%d): %w", prefix, connID, len(msg), err)
	}
	releaseFrameBuf(msg)
	return err
}
