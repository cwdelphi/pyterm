package icefilter

import (
	"testing"
)

// T1: 谓词 —— br0 必须保留，docker/veth/br- 前缀必须过滤（v1.0 §3 排雷要点）
func TestMatchCorePrefixes(t *testing.T) {
	// 无 Keep 的默认规则 → 安全降级为黑名单（绝不产生全过滤谓词）
	r, degraded := DefaultRule().Normalize()
	if !degraded {
		t.Fatalf("无 keep 的默认规则应降级")
	}
	if r.Mode != ModeBlacklist || len(r.Keep) != 0 {
		t.Fatalf("降级后应为 blacklist，got mode=%s keep=%v", r.Mode, r.Keep)
	}

	keepCases := []string{"br0", "eth0", "enp3s0", "wlp2s0", "enp1s0", "ib0"}
	dropCases := []string{
		"br-1a2f8ec15fb1", "br-88990d439340", // docker compose 网桥
		"docker0", "vetha8da3e2", "veth21845c7",
		"virbr0", "vboxnet0", "vmnet1", "zt8xq9", "cni0", "flannel.1", "cali12345",
		"tailscale0", // D2: allow_tailscale 缺省 false
		"lo", "sit0",
	}
	for _, n := range keepCases {
		if !r.Match(n) {
			t.Errorf("blacklist 模式应保留 %q", n)
		}
	}
	for _, n := range dropCases {
		if r.Match(n) {
			t.Errorf("应过滤 %q", n)
		}
	}
}

// T1: 白名单（auto/custom）模式 —— keep 外的接口一律过滤
func TestMatchWhitelistMode(t *testing.T) {
	r, degraded := Rule{
		Enabled: true, Mode: ModeAuto, Keep: []string{"br0"},
		AllowTailscale: false,
	}.Normalize()
	if degraded {
		t.Fatalf("keep 非空不应降级")
	}
	if !r.Match("br0") {
		t.Errorf("br0 必须保留")
	}
	if r.Match("tailscale0") {
		t.Errorf("D2: tailscale0 缺省必须过滤")
	}
	if r.Match("eth1") {
		t.Errorf("keep 外接口应被过滤")
	}
	if r.Match("br-abc") {
		t.Errorf("br- 前缀必须被过滤")
	}
}

// T1: allow_tailscale=true 时放行
func TestMatchAllowTailscale(t *testing.T) {
	r, _ := Rule{
		Enabled: true, Mode: ModeAuto, Keep: []string{"br0", "tailscale0"},
		AllowTailscale: true,
	}.Normalize()
	if !r.Match("tailscale0") {
		t.Errorf("allow_tailscale=true 时应保留 tailscale0")
	}
}

// T1: 空 keep → 降级 blacklist（pion/webrtc#3176：全过滤会导致 answerer SCTP 失败）
func TestEmptyKeepDegrades(t *testing.T) {
	r, degraded := Rule{Enabled: true, Mode: ModeAuto, Keep: nil}.Normalize()
	if !degraded {
		t.Fatalf("空 keep 应降级")
	}
	if r.Mode != ModeBlacklist {
		t.Fatalf("降级后应为 blacklist，got %s", r.Mode)
	}
	if !r.Match("br0") {
		t.Errorf("降级后不应再做白名单过滤")
	}
	if r.Match("tailscale0") {
		t.Errorf("降级后 D2: allow_tailscale=false 仍应过滤 tailscale0")
	}
}

// T1: enabled=false / mode=off → 完全不过滤（1 键熔断）
func TestDisable(t *testing.T) {
	Configure(DisableRule())
	defer Configure(DefaultRule())
	p := Predicate()
	for _, n := range []string{"br0", "br-x", "docker0", "veth0"} {
		if !p(n) {
			t.Errorf("关闭过滤后应放行 %q", n)
		}
	}
	if Enabled() {
		t.Errorf("Configure(DisableRule()) 后 Enabled 应为 false")
	}
}

// T1: envGate=false 优先级最高
func TestEnvGate(t *testing.T) {
	defer func() {
		SetEnvGate(true)
		Configure(DefaultRule())
	}()
	SetEnvGate(false)
	if Enabled() {
		t.Errorf("env 熔断时应为 disabled")
	}
	p := Predicate()
	if !p("docker0") {
		t.Errorf("env 熔断时谓词应全放行")
	}
	if Summary() != "env-gate disabled" {
		t.Errorf("Summary 应反映 env 熔断，got %q", Summary())
	}
}

// T1: Predicate 返回快照，Configure 之后不影响已建连的谓词
func TestPredicateSnapshot(t *testing.T) {
	defer Configure(DefaultRule())
	Configure(Rule{Enabled: true, Mode: ModeAuto, Keep: []string{"br0"}})
	p := Predicate()
	Configure(DisableRule())
	if p("eth9") {
		t.Errorf("谓词应为建连时的快照：keep 外接口仍被过滤，不随后续 Configure 放行")
	}
	if !p("br0") {
		t.Errorf("谓词快照应保留 br0")
	}
}

// 硬黑名单不可被后台删除
func TestHardBlacklistImmutable(t *testing.T) {
	r, _ := Rule{
		Enabled: true, Mode: ModeBlacklist,
		DropPrefix: []string{}, // 后台传空
	}.Normalize()
	for _, p := range DefaultDropPrefixes {
		if !containsStr(r.DropPrefix, p) {
			t.Errorf("硬黑名单 %q 不应被丢弃", p)
		}
	}
}
