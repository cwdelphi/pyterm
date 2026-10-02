// handler_conn.go — 由 handler.go 按域拆分（R1/R2 纯移动，无逻辑变更）。
package server

import (
	"encoding/json"
	"fmt"
	"log"
	"strings"
	"time"

	"github.com/pion/webrtc/v4"

	wrtc "github.com/ppy-tools/pyagent/webrtc"
)

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

// sendConnType 上报 DC 链路分类给浏览器（浏览器再补报 timeline 的 webrtc_path 列）。
// H4：conn_type 是关键小消息（~80B），SendCh/字节配额满时不再一丢了之 —— 改为有界
// 重试（50ms × ≤20 ≈ 1s，写泵在此期间会消费腾位）；会话已关则写泵不再消费，
// 尽力投递一次即收工，绝不长时间挂住 reportConnType goroutine。返回是否投递成功。
func sendConnType(session *Session, roomID, connType string, pair *webrtc.ICECandidatePair) bool {
	j, _ := json.Marshal(map[string]interface{}{
		"type":      "connection_type",
		"room_id":   roomID,
		"conn_type": connType,
	})
	detail := "no selected pair"
	if pair != nil {
		detail = fmt.Sprintf("local=%s remote=%s", pair.Local.Typ, pair.Remote.Typ)
	}
	const maxWait = 20
	for attempt := 0; attempt <= maxWait; attempt++ {
		if session.enqueue(j, false) {
			log.Printf("[GA-DC] conn_type room=%s %s (%s)", roomID, connType, detail)
			return true
		}
		select {
		case <-session.Done:
			// 会话已关：writePump 停止消费，队列不会再腾位 → 最后尽力投一次
			if session.enqueue(j, false) {
				log.Printf("[GA-DC] conn_type room=%s %s (%s) flushed on close", roomID, connType, detail)
				return true
			}
			log.Printf("[GA-DC] conn_type room=%s %s dropped (session closed)", roomID, connType)
			return false
		case <-time.After(50 * time.Millisecond):
		}
	}
	log.Printf("[GA-DC] conn_type room=%s %s dropped (send buffer full after %dms)",
		roomID, connType, maxWait*50)
	return false
}

// reportConnType DC 打开后轮询选中候选对并上报 DC 链路分类。
// H1（1.0.3，原「静默退出」缺陷）：
//   - 首拿 pair 立即首报：原逻辑固定等 1.5s settle，会话/PC 提前关闭即一条不发，
//     <1.5s 的短会话在网关模式下 webrtc_path 必然落空（诊断面板 none 行来源）；
//   - settle 后取最新 pair，分类与首报不同才发修正（防 srflx→relay 瞬时切换误报）；
//   - 会话 Done / PC 关闭（房间复用）前把最后已知分类 best-effort 补发一次；
//   - 10s 仍从未选中过候选对才判 BUG。
func reportConnType(session *Session, peerConn *webrtc.PeerConnection, roomID string) {
	const settle = 1500 * time.Millisecond
	deadline := time.Now().Add(10 * time.Second)
	var firstFound time.Time
	var lastPair *webrtc.ICECandidatePair // 最近一次选中候选对（关闭前补发用）
	sent := ""                            // 已成功上报的分类，"" = 尚未上报

	// send 分类未变化则不重复发（浏览器侧幂等，少一次往返）
	send := func(pair *webrtc.ICECandidatePair) {
		ct := wrtc.ClassifyConnType(pair.Local.Typ, pair.Remote.Typ)
		if ct == sent {
			return
		}
		if sendConnType(session, roomID, ct, pair) {
			sent = ct
		}
	}
	flush := func() {
		if lastPair != nil {
			send(lastPair)
		}
	}
	for time.Now().Before(deadline) {
		select {
		case <-session.Done:
			flush()
			return
		default:
		}
		if peerConn.ConnectionState() == webrtc.PeerConnectionStateClosed {
			flush()
			return
		}
		pair := selectedCandidatePair(peerConn)
		if pair == nil {
			time.Sleep(250 * time.Millisecond)
			continue
		}
		lastPair = pair
		if firstFound.IsZero() {
			firstFound = time.Now()
			send(pair) // H1：首拿即报，不等 settle
			continue
		}
		if time.Since(firstFound) < settle {
			time.Sleep(200 * time.Millisecond)
			continue
		}
		if latest := selectedCandidatePair(peerConn); latest != nil {
			pair = latest
			lastPair = pair
		}
		send(pair)
		return
	}
	if lastPair == nil {
		sendConnType(session, roomID, "BUG", nil)
		return
	}
	flush()
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
