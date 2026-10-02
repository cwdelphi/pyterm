// handler_connect.go — 由 handler.go 按域拆分（R1/R2 纯移动，无逻辑变更）。
package server

import (
	"encoding/json"
	"io"
	"log"
	"net/http"
	"os"
	"strings"
	"time"

	"github.com/pion/webrtc/v4"

	"github.com/ppy-tools/pyagent/icefilter"
	"github.com/ppy-tools/pyagent/signaling"
)

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
	// P2 #16: O(1) 索引替代线扫（原实现即存在性检查）
	_ = h.sessionByRoom(roomID)
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
	session := h.sessionByRoom(roomID)
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
	h.bindRoom(session, roomID)
	session.mu.Lock()
	defer session.mu.Unlock()
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
	session := h.sessionByRoom(roomID)
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
	session := h.sessionByRoom(roomID)
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
