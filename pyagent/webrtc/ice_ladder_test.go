package webrtc

import (
	"testing"
	"time"

	"github.com/pion/webrtc/v4"

	"github.com/ppy-tools/pyagent/icefilter"
	"github.com/ppy-tools/pyagent/netinfo"
	"github.com/ppy-tools/pyagent/pathcache"
)

// ClassifyConnType 规则（方案 §4.5）：任一端 relay → relay；两端 host/srflx/prflx → P2P；其余 → BUG
// R3: 唯一实现（原 server/handler.go 副本已删，判定表两处合一于此）
func TestClassifyConnType(t *testing.T) {
	cases := []struct {
		local, remote webrtc.ICECandidateType
		want          string
	}{
		{webrtc.ICECandidateTypeHost, webrtc.ICECandidateTypeHost, "P2P"},
		{webrtc.ICECandidateTypeHost, webrtc.ICECandidateTypeSrflx, "P2P"},
		{webrtc.ICECandidateTypeSrflx, webrtc.ICECandidateTypeSrflx, "P2P"},
		{webrtc.ICECandidateTypeSrflx, webrtc.ICECandidateTypePrflx, "P2P"},
		{webrtc.ICECandidateTypePrflx, webrtc.ICECandidateTypeHost, "P2P"},
		{webrtc.ICECandidateTypeHost, webrtc.ICECandidateTypeRelay, "relay"},
		{webrtc.ICECandidateTypeRelay, webrtc.ICECandidateTypeHost, "relay"},
		{webrtc.ICECandidateTypeSrflx, webrtc.ICECandidateTypeRelay, "relay"},
		{webrtc.ICECandidateTypeRelay, webrtc.ICECandidateTypeRelay, "relay"},
		{webrtc.ICECandidateType(99), webrtc.ICECandidateTypeHost, "BUG"},
		{webrtc.ICECandidateTypeHost, webrtc.ICECandidateType(99), "BUG"},
	}
	for _, c := range cases {
		if got := ClassifyConnType(c.local, c.remote); got != c.want {
			t.Errorf("ClassifyConnType(%v,%v)=%q want %q", c.local, c.remote, got, c.want)
		}
	}
}

// 等级谓词：L3 不过滤 / L2 走规则 / L1 只放行缓存胜出接口，且缓存失效时回落 L2
func TestLevelPredicateLadder(t *testing.T) {
	oldRule := icefilter.Current()
	oldEnabled := pathcache.Enabled()
	defer func() {
		icefilter.Configure(oldRule)
		pathcache.SetEnabled(oldEnabled)
		pathcache.Default().Clear()
		icefilter.SetEnvGate(true)
	}()

	// 先跑一次真实扫描拿到 if_hash（缓存失效用例依赖非空指纹）
	if _, err := netinfo.Refresh(netinfo.Options{}); err != nil {
		t.Logf("netinfo 扫描失败（可能无网），跳过依赖 if_hash 的失效用例: %v", err)
	}

	// 黑名单规则：普通接口全放行，usb 前缀丢弃
	icefilter.Configure(icefilter.Rule{Enabled: true, Mode: icefilter.ModeBlacklist, DropPrefix: []string{"usb"}})
	pathcache.SetEnabled(true)
	pathcache.Default().Clear()

	// L3：不过滤
	if p := levelPredicate(3); p != nil {
		t.Errorf("L3 应返回 nil(不设过滤)")
	}

	// L2 / 未宣告：走进程级规则
	for _, lv := range []int{0, 2} {
		p := levelPredicate(lv)
		if p == nil {
			t.Fatalf("level=%d 应有过滤谓词", lv)
		}
		if !p("eth0") {
			t.Errorf("level=%d 黑名单规则应放行 eth0", lv)
		}
		if p("usb0") {
			t.Errorf("level=%d 黑名单规则应丢弃 usb0", lv)
		}
	}

	// L1 无缓存 → 回落 L2
	if p := levelPredicate(1); p == nil {
		t.Fatalf("L1 无缓存时应回落 L2(非 nil)")
	} else if !p("eth0") {
		t.Errorf("L1 无缓存回落应按规则放行 eth0")
	}

	// L1 命中缓存 → 只放行胜出接口
	pathcache.Default().Record(pathcache.CacheKey, "eth0", currentIfHash(), "P2P", 0)
	p := levelPredicate(1)
	if p == nil {
		t.Fatalf("L1 命中缓存时应有过滤谓词")
	}
	if !p("eth0") {
		t.Errorf("L1 应放行缓存胜出接口 eth0")
	}
	if p("wlan0") {
		t.Errorf("L1 不应放行非胜出接口 wlan0")
	}

	// 缓存被规则丢弃（usb0 在黑名单）→ 回落 L2：按规则放行普通接口、丢弃 usb0
	pathcache.Default().Record(pathcache.CacheKey, "usb0", currentIfHash(), "P2P", 0)
	p2 := levelPredicate(1)
	if p2 == nil {
		t.Fatalf("缓存失效时应回落 L2(非 nil)")
	}
	if !p2("eth0") {
		t.Errorf("回落 L2 后应按规则放行 eth0")
	}
	if p2("usb0") {
		t.Errorf("回落 L2 后规则仍应丢弃 usb0")
	}

	// if_hash 变化 → 缓存整体失效 → 回落 L2（无指纹时 Lookup 跳过校验，用例退化）
	if currentIfHash() == "" {
		t.Log("netinfo 尚无 if_hash，跳过失效用例（pathcache 单测已覆盖 hash 校验）")
	} else {
		pathcache.Default().Record(pathcache.CacheKey, "eth0", "stale-if-hash", "P2P", 0)
		p3 := levelPredicate(1)
		if p3 == nil {
			t.Fatalf("缓存失效时应回落 L2(非 nil)")
		}
		if !p3("wlan0") {
			t.Errorf("if_hash 失效后应回落 L2 放行规则内接口")
		}
	}

	// path_cache 关闭 → L1 不生效，退化为规则
	pathcache.SetEnabled(false)
	p4 := levelPredicate(1)
	if p4 == nil {
		t.Fatalf("path_cache 关闭时应回落 L2(非 nil)")
	}
	if !p4("wlan0") {
		t.Errorf("path_cache 关闭后应按规则放行")
	}
}

// 阶梯自统计：等级升高记回退、L3 记兜底、L1 记命中
func TestRecordLadderCounts(t *testing.T) {
	netinfo.ClearStats()
	defer netinfo.ClearStats()

	h := &SignalHandler{}

	h.noteOfferLevel(2)
	l := netinfo.GetLadder()
	if l == nil || l.Level != 2 {
		t.Fatalf("首次 offer 应记录 level=2, got %+v", l)
	}
	if l.Fallbacks != 0 || l.L3 != 0 || l.CacheHits != 0 {
		t.Errorf("首次 offer 不应计数, got %+v", l)
	}

	h.noteOfferLevel(3)
	l = netinfo.GetLadder()
	if l.Fallbacks != 1 || l.L3 != 1 {
		t.Errorf("2→3 应计 1 次回退 + 1 次 L3, got %+v", l)
	}

	// 3→1 是等级下降（恢复），不算回退；但 L1 每次都算缓存命中
	h.noteOfferLevel(1)
	l = netinfo.GetLadder()
	if l.Fallbacks != 1 {
		t.Errorf("3→1 等级下降不应计回退, got %+v", l)
	}
	if l.CacheHits != 1 {
		t.Errorf("level=1 应计缓存命中, got %+v", l)
	}

	// 等级不变：不重复计回退，L1 每次命中仍累加
	h.noteOfferLevel(1)
	l = netinfo.GetLadder()
	if l.Fallbacks != 1 || l.L3 != 1 {
		t.Errorf("等级未变不应计回退/L3, got %+v", l)
	}
	if l.CacheHits != 2 {
		t.Errorf("level=1 每次命中都应累加, got %+v", l)
	}
}

// 等级上调（网关指令性重建）应跳过 D4 失败节流
func TestNoteOfferLevelSkipsCooldown(t *testing.T) {
	cdMu.Lock()
	cdSkip = false
	cdFail = time.Time{}
	cdMu.Unlock()
	defer func() {
		cdMu.Lock()
		cdSkip = false
		cdMu.Unlock()
		netinfo.ClearStats()
	}()
	netinfo.ClearStats()

	h := &SignalHandler{}
	h.noteOfferLevel(2)
	cdMu.Lock()
	skip := cdSkip
	cdMu.Unlock()
	if skip {
		t.Errorf("首次 offer 不应跳过节流")
	}

	h.noteOfferLevel(3)
	cdMu.Lock()
	skip = cdSkip
	cdMu.Unlock()
	if !skip {
		t.Errorf("等级上调应设置跳过节流标记")
	}

	waitICECooldown()
	cdMu.Lock()
	skip = cdSkip
	cdMu.Unlock()
	if skip {
		t.Errorf("waitICECooldown 应消费跳过标记")
	}
}

// 胜出接口反查（看板 last_pair 的接口名）
func TestIfaceForIP(t *testing.T) {
	if got := ifaceForIP(""); got != "" {
		t.Errorf("空 IP 应返回空, got %q", got)
	}
	if got := ifaceForIP("192.0.2.123"); got != "" {
		t.Errorf("不存在的 IP 应返回空, got %q", got)
	}
	if got := ifaceForIP("127.0.0.1"); got == "" {
		t.Log("回环地址未映射到接口（平台差异），跳过")
	}
}
