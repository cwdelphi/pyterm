package server

import (
	"encoding/json"
	"testing"

	"github.com/pion/webrtc/v4"

	"github.com/ppy-tools/pyagent/signaling"
)

// Fix A 回归: DC 未 Open 时 sendOrBuffer 必须进 pendingMsgs 而非静默丢弃。
// session.DataChannel 在 CreateDataChannel 时即赋值 (远早于 OnOpen),
// 仅判 dc==nil 会把 vnc_connect/ssh_connect 打进未 Open 的通道,
// pion v4 Send 首行 ensureOpen 返回错误被忽略 → 消息静默丢失。
func TestSendOrBufferBuffersWhenChannelNotOpen(t *testing.T) {
	h := &Handler{}
	s := &Session{
		ID:     "t1",
		RoomID: "room-t",
		SendCh: make(chan []byte, 4),
		Done:   make(chan struct{}),
	}

	h.sendOrBuffer(s, "vnc_connect", []byte{0x20, 0x01, 0x02})
	if len(s.pendingMsgs) != 1 {
		t.Fatalf("sendOrBuffer: want 1 buffered msg, got %d", len(s.pendingMsgs))
	}

	h.bridgeWSToDataChannel(s, map[string]interface{}{
		"type":    "vnc_connect",
		"room_id": "room-t",
		"host":    "127.0.0.1",
		"port":    float64(5900),
	})
	if len(s.pendingMsgs) != 2 {
		t.Fatalf("bridgeWSToDataChannel: want 2 buffered msgs, got %d", len(s.pendingMsgs))
	}

	// 未知类型: 仅告警, 不进缓冲
	h.bridgeWSToDataChannel(s, map[string]interface{}{"type": "no_such_type"})
	if len(s.pendingMsgs) != 2 {
		t.Fatalf("unknown type should not buffer, got %d", len(s.pendingMsgs))
	}
}

// Fix C 回归: answer(remote description) 前到达的 ICE 候选必须缓冲,
// answer 生效后补发。此前 AddICECandidate 返回 InvalidStateError(ErrNoRemoteDescription)
// 被忽略 → 候选静默丢弃 → 网关只能靠 STUN binding 收敛(remote=prflx), ICE 耗时 14~15s。
func TestHandleCandidateBuffersBeforeAnswerThenFlushes(t *testing.T) {
	gw, err := webrtc.NewPeerConnection(webrtc.Configuration{})
	if err != nil {
		t.Fatalf("new gw pc: %v", err)
	}
	defer gw.Close()
	ag, err := webrtc.NewPeerConnection(webrtc.Configuration{})
	if err != nil {
		t.Fatalf("new agent pc: %v", err)
	}
	defer ag.Close()

	// 生产路径: 网关先 CreateDataChannel 再 CreateOffer (无 m= 行的 offer 缺 ice-ufrag)
	if _, err := gw.CreateDataChannel("data", nil); err != nil {
		t.Fatalf("create data channel: %v", err)
	}
	offer, err := gw.CreateOffer(nil)
	if err != nil {
		t.Fatalf("create offer: %v", err)
	}
	if err := gw.SetLocalDescription(offer); err != nil {
		t.Fatalf("set local offer: %v", err)
	}
	if err := ag.SetRemoteDescription(offer); err != nil {
		t.Fatalf("agent set remote offer: %v", err)
	}
	answer, err := ag.CreateAnswer(nil)
	if err != nil {
		t.Fatalf("create answer: %v", err)
	}
	if err := ag.SetLocalDescription(answer); err != nil {
		t.Fatalf("agent set local answer: %v", err)
	}

	h := &Handler{sessions: make(map[string]*Session)}
	s := &Session{
		ID:       "s-test",
		RoomID:   "room-cand",
		PeerConn: gw,
		SendCh:   make(chan []byte, 4),
		Done:     make(chan struct{}),
	}
	h.sessions[s.ID] = s

	// answer 尚未应用 → 候选必须进缓冲
	h.handleCandidate(&signaling.Message{
		RoomID:    "room-cand",
		Candidate: json.RawMessage(`{"candidate":"candidate:842163049 1 UDP 1677729535 192.0.2.1 5000 typ host","sdpMid":"0","sdpMLineIndex":0}`),
	})
	if len(s.pendingCandidates) != 1 {
		t.Fatalf("candidate must be buffered before answer, got %d", len(s.pendingCandidates))
	}

	sdpJSON, err := json.Marshal(webrtc.SessionDescription{Type: webrtc.SDPTypeAnswer, SDP: answer.SDP})
	if err != nil {
		t.Fatalf("marshal answer: %v", err)
	}
	h.handleAnswer(&signaling.Message{RoomID: "room-cand", SDP: sdpJSON})

	if gw.RemoteDescription() == nil {
		t.Fatal("remote description should be applied")
	}
	if len(s.pendingCandidates) != 0 {
		t.Fatalf("buffered candidates should be flushed after answer, got %d", len(s.pendingCandidates))
	}

	// answer 后到达的候选直接应用, 不再进缓冲
	h.handleCandidate(&signaling.Message{
		RoomID:    "room-cand",
		Candidate: json.RawMessage(`{"candidate":"candidate:842163050 1 UDP 1677729535 192.0.2.2 5001 typ host","sdpMid":"0","sdpMLineIndex":0}`),
	})
	if len(s.pendingCandidates) != 0 {
		t.Fatalf("post-answer candidate should apply directly, got %d buffered", len(s.pendingCandidates))
	}
}
