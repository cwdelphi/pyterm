package webrtc

import (
	"testing"
	"time"

	"github.com/pion/webrtc/v4"

	"github.com/ppy-tools/pyagent/icefilter"
	"github.com/ppy-tools/pyagent/netinfo"
)

// gatherLocal 完成一次候选收集并返回本端候选数
func gatherLocal(t *testing.T, filtered bool) int {
	t.Helper()
	var (
		pc  *webrtc.PeerConnection
		err error
	)
	if filtered {
		se := webrtc.SettingEngine{}
		se.SetInterfaceFilter(icefilter.Predicate())
		pc, err = webrtc.NewAPI(webrtc.WithSettingEngine(se)).NewPeerConnection(webrtc.Configuration{})
	} else {
		pc, err = webrtc.NewPeerConnection(webrtc.Configuration{})
	}
	if err != nil {
		t.Fatalf("创建 PeerConnection 失败: %v", err)
	}
	defer pc.Close()

	if _, err := pc.CreateDataChannel("ice-filter-test", nil); err != nil {
		t.Fatalf("CreateDataChannel 失败: %v", err)
	}
	offer, err := pc.CreateOffer(nil)
	if err != nil {
		t.Fatalf("CreateOffer 失败: %v", err)
	}
	if err := pc.SetLocalDescription(offer); err != nil {
		t.Fatalf("SetLocalDescription 失败: %v", err)
	}
	select {
	case <-webrtc.GatheringCompletePromise(pc):
	case <-time.After(20 * time.Second):
		t.Log("候选收集超时，按当前结果继续")
	}
	return countLocalCandidates(pc)
}

// 集成验证：本地扫描 → 规则 → SettingEngine → 候选数确实下降
// （方案 §5 收益口径：候选 35→6、候选对 1200→36）
func TestICEFilterReducesCandidates(t *testing.T) {
	rep, err := netinfo.Refresh(netinfo.Options{})
	if err != nil {
		t.Fatalf("netinfo 扫描失败: %v", err)
	}
	t.Logf("rule=%s keep=%v valid=%v host=%d->%d addr=%d->%d pairs=%d->%d if_hash=%s",
		rep.Rule.Summary(), rep.Rule.Keep, rep.Valid,
		rep.HostBefore, rep.HostAfter, rep.AddrBefore, rep.AddrAfter,
		rep.EstPairsBef, rep.EstPairsAft, rep.IfHash)

	if !rep.Valid {
		t.Fatalf("规则无效，无法验证: %s", rep.Reason)
	}
	if len(rep.Rule.Keep) == 0 {
		t.Fatalf("keep 集不应为空")
	}

	raw := gatherLocal(t, false)
	filtered := gatherLocal(t, true)
	t.Logf("candidates: unfiltered=%d filtered=%d", raw, filtered)

	if raw == 0 {
		t.Skip("环境未收集到候选（无网络），跳过收益断言")
	}
	if filtered > raw {
		t.Errorf("过滤后候选数不应增加: raw=%d filtered=%d", raw, filtered)
	}
	if filtered > 8 {
		t.Errorf("过滤后候选数应显著收敛 (<=8)，got %d", filtered)
	}
	if filtered >= raw {
		t.Errorf("过滤未产生收益: raw=%d filtered=%d", raw, filtered)
	}
}

// 关闭过滤时谓词应全放行（1 键熔断回归）
func TestICEFilterFuse(t *testing.T) {
	defer icefilter.SetEnvGate(true)
	icefilter.SetEnvGate(false)
	p := icefilter.Predicate()
	if !p("docker0") || !p("br-abc") {
		t.Errorf("熔断关闭时谓词应全放行")
	}
}
