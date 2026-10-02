// signal_speedtest.go — 由 signal.go 按域拆分（R1/R2 纯移动，无逻辑变更）。
package webrtc

import (
	"encoding/json"
	"fmt"
	"log"
	"math"
	"sync"
	"time"

	"github.com/pion/webrtc/v4"
	ws "github.com/ppy-tools/pyagent/websocket"
)

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

	// P2 #8: 先挂关闭回调再入表（此时指针未发布，不存在关闭先于注册的竞态）
	peer.OnClose(func() { h.forgetPeer(req.RoomID, peer) })
	h.mu.Lock()
	if oldPeer, exists := h.p[req.RoomID]; exists {
		// 同 handleOffer：旧 Peer Close 可能等 gather 收尾（STUN/TURN 不可达时阻塞），持锁异步关
		go oldPeer.Close()
	}
	h.p[req.RoomID] = peer
	// 换代即丢弃上一轮协商的候选缓冲（旧 ufrag 对新 Peer 无效）
	delete(h.pendingCandidates, req.RoomID)
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
	// P2 #13: 背压轮询(10ms)/限速步进(interval)共用一个定时器，不再循环 time.After 分配
	poll := time.NewTimer(time.Hour)
	poll.Stop()
	defer poll.Stop()
	for time.Now().Before(deadline) {
		select {
		case <-stop:
			return
		default:
		}
		for dc.BufferedAmount() > 64*1024 && time.Now().Before(deadline) {
			poll.Reset(10 * time.Millisecond)
			select {
			case <-stop:
				return
			case <-poll.C:
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
				poll.Reset(interval)
				select {
				case <-stop:
					return
				case <-poll.C:
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
	// P2 #18: attach 时建状态，OnClose Delete（旧实现仅 LoadOrStore → 每次测速泄漏一份孤儿态）
	ensureDCWriteState(dc)
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
		dcWriteStates.Delete(dc)
		if h.speedtestPlugin != nil {
			h.speedtestPlugin.Detach(roomID)
		}
	})
}
