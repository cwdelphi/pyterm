package netinfo

import (
	"net"
	"strings"
	"testing"

	"github.com/ppy-tools/pyagent/icefilter"
)

// ipn 构造测试用地址
func ipn(s string, bits int) net.Addr {
	ip := net.ParseIP(s)
	if ip == nil {
		panic("bad ip " + s)
	}
	if ip.To4() != nil {
		return &net.IPNet{IP: ip, Mask: net.CIDRMask(24, 32)}
	}
	return &net.IPNet{IP: ip, Mask: net.CIDRMask(64, 128)}
}

// fixture42 复现宿主真实拓扑：42 接口（26×br-、9×veth、lo/enp1s0/enp3s0/wlp2s0/br0/tailscale0/docker0）
// 有全局地址 29 个：26×br- + br0 + tailscale0 + docker0
func fixture42() []rawIface {
	var raw []rawIface
	for i := 0; i < 26; i++ {
		raw = append(raw, rawIface{
			Name: "br-" + strings.Repeat("a", 10)[:10] + string(rune('0'+i%10)),
			MTU:  1500, Up: true,
			Addrs: []net.Addr{ipn("172.18.0.1", 32)},
		})
	}
	// 修正重复名（保证唯一名）
	for i := range raw {
		raw[i].Name = "br-" + padHex(i)
	}
	for i := 0; i < 9; i++ {
		raw = append(raw, rawIface{
			Name: "veth" + padHex(i), MTU: 1500, Up: true,
			Addrs: nil, // 宿主侧 veth 无全局地址
		})
	}
	raw = append(raw, rawIface{Name: "lo", MTU: 65536, Up: true, Loopback: true,
		Addrs: []net.Addr{ipn("127.0.0.1", 32)}})
	raw = append(raw, rawIface{Name: "enp1s0", MTU: 1500, Up: true, Addrs: nil})
	raw = append(raw, rawIface{Name: "enp3s0", MTU: 1500, Up: false, Addrs: nil})
	raw = append(raw, rawIface{Name: "wlp2s0", MTU: 1500, Up: true, Addrs: nil})
	raw = append(raw, rawIface{Name: "br0", MTU: 1500, Up: true, IsDefault: true,
		Addrs: []net.Addr{ipn("192.0.2.50", 32)}})
	raw = append(raw, rawIface{Name: "tailscale0", MTU: 1280, Up: true,
		Addrs: []net.Addr{ipn("203.0.113.9", 32)}})
	raw = append(raw, rawIface{Name: "docker0", MTU: 1500, Up: true,
		Addrs: []net.Addr{ipn("172.17.0.1", 32)}})
	if len(raw) != 42 {
		tPanic("fixture 应为 42 个接口, got " + itoa(len(raw)))
	}
	return raw
}

func padHex(i int) string {
	const h = "0123456789abcdef"
	return string(h[i/16]) + string(h[i%16])
}

var tPanic = func(msg string) { panic(msg) }

// T2: 42 接口宿主实测 fixture → keep == ["br0"]
func TestEvaluateFixture42(t *testing.T) {
	rep := evaluate(fixture42(), Options{})

	if rep.HostBefore != 29 {
		t.Errorf("host_before 应为 29, got %d", rep.HostBefore)
	}
	if len(rep.Rule.Keep) != 1 || rep.Rule.Keep[0] != "br0" {
		t.Errorf("keep 应为 [br0], got %v", rep.Rule.Keep)
	}
	if rep.HostAfter != 1 {
		t.Errorf("host_after 应为 1, got %d", rep.HostAfter)
	}
	if rep.AddrBefore != 29 || rep.AddrAfter != 1 {
		t.Errorf("addr_before/after 应为 29/1, got %d/%d", rep.AddrBefore, rep.AddrAfter)
	}
	if rep.EstCandBef != 34 || rep.EstCandAft != 6 {
		t.Errorf("est_candidates 应为 34/6, got %d/%d", rep.EstCandBef, rep.EstCandAft)
	}
	if !rep.Valid {
		t.Errorf("规则应有效, reason=%s", rep.Reason)
	}
	if rep.Rule.Mode != icefilter.ModeAuto {
		t.Errorf("mode 应为 auto, got %s", rep.Rule.Mode)
	}

	// 硬黑名单必须命中 br- 与 docker/veth，且绝不命中 br0
	if rep.Rule.Match("br-abc123") {
		t.Errorf("br- 必须被过滤")
	}
	if !rep.Rule.Match("br0") {
		t.Errorf("br0 必须保留（v1.0 §3 排雷要点）")
	}
	if rep.Rule.Match("docker0") || rep.Rule.Match("veth0000") || rep.Rule.Match("tailscale0") {
		t.Errorf("虚拟接口/默认 Tailscale 必须被过滤")
	}

	// 收益量化（方案 §5）
	if rep.EstPairsBef != 1156 { // (29+5)^2
		t.Errorf("est_pairs_before 应为 1156, got %d", rep.EstPairsBef)
	}
	if rep.EstPairsAft != 36 { // (1+5)^2
		t.Errorf("est_pairs_after 应为 36, got %d", rep.EstPairsAft)
	}
	if rep.EstPairsAft >= rep.EstPairsBef {
		t.Errorf("过滤后候选对应减少")
	}
	if rep.IfHash == "" {
		t.Errorf("if_hash 不应为空")
	}
}

// T2: 评分与硬否决原因
func TestScoreAndHardDrop(t *testing.T) {
	raw := fixture42()
	rep := evaluate(raw, Options{})
	byName := map[string]Iface{}
	for _, f := range rep.Interfaces {
		byName[f.Name] = f
	}

	br0 := byName["br0"]
	if br0.HardDrop {
		t.Errorf("br0 不应被硬否决")
	}
	if br0.Score != 50+20+15+5 { // 默认路由+IPv4+物理+MTU = 90
		t.Errorf("br0 评分应为 90, got %d", br0.Score)
	}
	if !br0.Keep || !br0.IsDefault {
		t.Errorf("br0 应被保留且标记为默认路由")
	}

	if enp := byName["enp3s0"]; !enp.HardDrop || enp.Reason != "down" {
		t.Errorf("DOWN 接口应被硬否决, got %+v", enp)
	}
	if lo := byName["lo"]; !lo.HardDrop || !lo.IsLoopback {
		t.Errorf("loopback 应被硬否决")
	}
	ts := byName["tailscale0"]
	if !ts.HardDrop || !strings.Contains(ts.Reason, "D2") {
		t.Errorf("tailscale0 缺省应按决策 D2 硬否决, got %q", ts.Reason)
	}
}

// T2: allow_tailscale=true 且保留时放行
func TestEvaluateAllowTailscale(t *testing.T) {
	rep := evaluate(fixture42(), Options{AllowTailscale: true})
	for _, f := range rep.Interfaces {
		if f.Name == "tailscale0" && f.HardDrop && strings.Contains(f.Reason, "D2") {
			t.Errorf("allow_tailscale=true 时不应按 D2 硬否决")
		}
	}
	// br0 分数仍最高
	if len(rep.Rule.Keep) == 0 || rep.Rule.Keep[0] != "br0" {
		t.Errorf("默认路由仍应首选, got %v", rep.Rule.Keep)
	}
}

// T2: 默认路由强制保留（即使分数低于阈值）
func TestForceKeepDefaultRoute(t *testing.T) {
	// 构造：默认路由落在一个低分接口上（无 IPv4、有 IPv6、虚拟、MTU 小）
	var raw []rawIface
	raw = append(raw, rawIface{Name: "wg-core", MTU: 1000, Up: true, IsDefault: true,
		Addrs: []net.Addr{ipn("fd00::1", 128)}})
	raw = append(raw, rawIface{Name: "eth0", MTU: 1500, Up: true,
		Addrs: []net.Addr{ipn("10.0.0.5", 32)}})
	rep := evaluate(raw, Options{})
	if !contains(rep.Rule.Keep, "wg-core") {
		t.Errorf("默认路由接口应被强制保留, got %v", rep.Rule.Keep)
	}
	byName := map[string]Iface{}
	for _, f := range rep.Interfaces {
		byName[f.Name] = f
	}
	if !byName["wg-core"].Keep {
		t.Errorf("报告中默认路由应标记 Keep")
	}
}

// T2: 空 keep → 降级 blacklist（不产生全过滤谓词）
func TestDegradeWhenNoKeep(t *testing.T) {
	raw := []rawIface{
		{Name: "br-00", MTU: 1500, Up: true, Addrs: []net.Addr{ipn("172.18.0.1", 32)}},
		{Name: "lo", MTU: 65536, Up: true, Loopback: true, Addrs: []net.Addr{ipn("127.0.0.1", 32)}},
	}
	rep := evaluate(raw, Options{})
	if rep.Valid {
		t.Errorf("keep 为空应 invalid, reason=%s", rep.Reason)
	}
	if rep.Rule.Mode != icefilter.ModeBlacklist {
		t.Errorf("应降级为 blacklist, got %s", rep.Rule.Mode)
	}
	if !rep.Rule.Match("eth9") {
		t.Errorf("降级后不应做白名单过滤")
	}
}

// T2: 接口指纹随地址变化
func TestIfHashChanges(t *testing.T) {
	a := evaluate(fixture42(), Options{})
	b := evaluate(fixture42(), Options{})
	if a.IfHash != b.IfHash {
		t.Errorf("相同拓扑指纹应一致")
	}
	b2 := fixture42()
	b2[len(b2)-1].Addrs = []net.Addr{ipn("192.0.2.99", 32)}
	c := evaluate(b2, Options{})
	if a.IfHash == c.IfHash {
		t.Errorf("地址变化应导致指纹变化")
	}
	if lo := byNameOf(a, "lo"); lo.AddrCount != 0 {
		t.Errorf("loopback 地址不计入候选, got %d", lo.AddrCount)
	}
}

// T2: parseDefaultIface
func TestParseDefaultIface(t *testing.T) {
	const route = `Iface	Destination	Gateway 	Flags	RefCnt	Use	Metric	Mask		MTU	Window	IRTT
br0	00000000	0103A8C0	0003	0	0	0	00000000	0	0	0
br0	0003A8C0	00000000	0001	0	0	0	00FFFFFF	0	0	0
docker0	0011117A	00000000	0001	0	0	0	00FFFFFF	0	0	0
`
	if got := parseDefaultIface(route); got != "br0" {
		t.Errorf("默认路由接口应为 br0, got %q", got)
	}
	if got := parseDefaultIface("Iface\tDestination\tGateway\n"); got != "" {
		t.Errorf("无默认路由应返回空, got %q", got)
	}
}

// T2: 扫描结果可直接被 icefilter 消费
func TestReportRuleIntegrates(t *testing.T) {
	rep := evaluate(fixture42(), Options{})
	icefilter.Configure(rep.Rule)
	p := icefilter.Predicate()
	if !p("br0") {
		t.Errorf("br0 应放行")
	}
	if p("docker0") || p("br-11") || p("tailscale0") {
		t.Errorf("虚拟接口应过滤")
	}
}

func byNameOf(rep *Report, name string) Iface {
	for _, f := range rep.Interfaces {
		if f.Name == name {
			return f
		}
	}
	return Iface{}
}

func contains(list []string, v string) bool {
	for _, s := range list {
		if s == v {
			return true
		}
	}
	return false
}
