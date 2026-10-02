package icecfg

import (
	"testing"

	"github.com/ppy-tools/pyagent/icefilter"
	"github.com/ppy-tools/pyagent/netinfo"
)

func cleanupIce(t *testing.T) {
	t.Helper()
	t.Cleanup(func() {
		icefilter.ClearServerOverride()
		icefilter.SetEnvGate(true)
		netinfo.SetOptions(nil)
		icefilter.Configure(icefilter.DefaultRule())
	})
}

// enabled=false → 1 键熔断，且本地自扫描不得翻案
func TestApplyOffIsHardFuse(t *testing.T) {
	cleanupIce(t)

	src, err := Apply(&icefilter.Rule{Enabled: false, Mode: icefilter.ModeCustom, Keep: []string{"br0"}})
	if err != nil {
		t.Fatalf("熔断不应报错: %v", err)
	}
	if src != "off" {
		t.Fatalf("src 应为 off, got %q", src)
	}
	if icefilter.Enabled() {
		t.Fatalf("熔断后应关闭过滤")
	}
	// 本地 5min 重扫试图打开 → 被服务端覆盖挡住
	icefilter.Configure(icefilter.Rule{Enabled: true, Mode: icefilter.ModeCustom, Keep: []string{"br0"}})
	if icefilter.Enabled() {
		t.Fatalf("熔断必须压过本地自扫描")
	}
}

// mode=custom → 服务端规则覆盖本地自扫描
func TestApplyCustomOverridesLocal(t *testing.T) {
	cleanupIce(t)

	src, err := Apply(&icefilter.Rule{Enabled: true, Mode: icefilter.ModeCustom, Keep: []string{"br0"}})
	if err != nil {
		t.Fatalf("custom 模式不应报错: %v", err)
	}
	if src != "server" {
		t.Fatalf("src 应为 server, got %q", src)
	}
	if !icefilter.ServerOverride() {
		t.Fatalf("custom 模式应挂起本地自扫描")
	}
	if r := icefilter.Current(); r.Mode != icefilter.ModeCustom || !r.Match("br0") || r.Match("eth1") {
		t.Fatalf("规则未按 keep 集生效: %s", r.Summary())
	}
}

// mode=auto → 解除服务端覆盖，本地自扫描恢复
func TestApplyAutoRestoresLocalScan(t *testing.T) {
	cleanupIce(t)

	icefilter.ConfigureServer(icefilter.Rule{Enabled: true, Mode: icefilter.ModeCustom, Keep: []string{"br0"}})
	src, err := Apply(&icefilter.Rule{Enabled: true, Mode: icefilter.ModeAuto, AllowTailscale: false})
	if src != "auto" {
		t.Fatalf("src 应为 auto, got %q", src)
	}
	if err != nil {
		t.Fatalf("auto 模式自扫描失败: %v", err)
	}
	if icefilter.ServerOverride() {
		t.Fatalf("auto 模式应解除服务端覆盖")
	}
	if r := icefilter.Current(); r.Mode != icefilter.ModeAuto && r.Mode != icefilter.ModeBlacklist {
		t.Fatalf("auto 模式规则应由本地扫描产出, got %s", r.Summary())
	}
	// 之后本地重扫可自由改写
	icefilter.Configure(icefilter.Rule{Enabled: true, Mode: icefilter.ModeBlacklist})
	if icefilter.Current().Mode != icefilter.ModeBlacklist {
		t.Fatalf("解除覆盖后本地扫描应生效")
	}
}

// keep 为空 → 降级为黑名单并给出软提示（pion/webrtc#3176 护栏）
func TestApplyEmptyKeepDegrades(t *testing.T) {
	cleanupIce(t)

	src, err := Apply(&icefilter.Rule{Enabled: true, Mode: icefilter.ModeCustom, Keep: nil})
	if src != "server" {
		t.Fatalf("src 应为 server, got %q", src)
	}
	if err == nil {
		t.Fatalf("keep 为空应返回降级提示")
	}
	r := icefilter.Current()
	if r.Mode != icefilter.ModeBlacklist {
		t.Fatalf("应降级为 blacklist, got %s", r.Mode)
	}
	if !r.Match("br0") || r.Match("br-abc") {
		t.Fatalf("降级后黑名单谓词不符: %s", r.Summary())
	}
}

// 非法 mode → 不改动当前规则
func TestApplyUnknownModeRejected(t *testing.T) {
	cleanupIce(t)

	icefilter.Configure(icefilter.Rule{Enabled: true, Mode: icefilter.ModeBlacklist})
	src, err := Apply(&icefilter.Rule{Enabled: true, Mode: "banana", Keep: []string{"br0"}})
	if err == nil {
		t.Fatalf("非法 mode 应报错")
	}
	if src != "" {
		t.Fatalf("非法 mode 不应有生效来源, got %q", src)
	}
	if icefilter.Current().Mode != icefilter.ModeBlacklist {
		t.Fatalf("非法 mode 不应改动规则")
	}
}

// D4: 节流窗口边界
func TestCooldownClamp(t *testing.T) {
	cases := []struct{ in, want int }{{-5, 0}, {0, 0}, {2, 2}, {30, 30}, {99, 30}}
	for _, c := range cases {
		got := Cooldown(c.in)
		if int(got.Seconds()) != c.want {
			t.Errorf("Cooldown(%d)=%v, want %ds", c.in, got, c.want)
		}
	}
}

func TestNormalizeMode(t *testing.T) {
	if NormalizeMode("") != icefilter.ModeAuto {
		t.Errorf("空 mode 应归一为 auto")
	}
	if NormalizeMode(" Custom ") != "custom" {
		t.Errorf("mode 应 trim+小写")
	}
}
