package icefilter

import "testing"

// P1: 服务端覆盖优先级 —— 后台 config_json.ice 下发后，本地 5min 自扫描不得改写规则
func TestServerOverrideBeatsLocalScan(t *testing.T) {
	t.Cleanup(func() {
		ClearServerOverride()
		SetEnvGate(true)
		Configure(DefaultRule())
	})

	// 1) 本地扫描先驱动规则
	Configure(Rule{Enabled: true, Mode: ModeBlacklist, DropPrefix: []string{"eth9"}})
	if Current().Mode != ModeBlacklist {
		t.Fatalf("本地扫描应生效, got mode=%s", Current().Mode)
	}

	// 2) 后台接管 → 之后的本地扫描被忽略
	ConfigureServer(Rule{Enabled: true, Mode: ModeCustom, Keep: []string{"br0"}})
	if !ServerOverride() {
		t.Fatalf("应处于服务端覆盖状态")
	}
	Configure(Rule{Enabled: true, Mode: ModeBlacklist, DropPrefix: []string{"eth9"}})
	got := Current()
	if got.Mode != ModeCustom || len(got.Keep) != 1 || got.Keep[0] != "br0" {
		t.Fatalf("服务端规则应压过本地扫描, got mode=%s keep=%v", got.Mode, got.Keep)
	}
	if !got.Match("br0") || got.Match("eth0") {
		t.Fatalf("custom 模式应只保留 keep 集, got summary=%s", got.Summary())
	}
	if s := Summary(); !containsSuffix(s, "src=server") {
		t.Fatalf("Summary 应标注 src=server, got %q", s)
	}

	// 3) mode=auto → 解除覆盖，本地扫描恢复
	ClearServerOverride()
	if ServerOverride() {
		t.Fatalf("应已解除服务端覆盖")
	}
	Configure(Rule{Enabled: true, Mode: ModeBlacklist})
	if Current().Mode != ModeBlacklist {
		t.Fatalf("解除覆盖后本地扫描应恢复, got mode=%s", Current().Mode)
	}
	if s := Summary(); !containsSuffix(s, "src=local") {
		t.Fatalf("Summary 应标注 src=local, got %q", s)
	}
}

// P1: 服务端熔断（enabled=false）期间本地扫描不得把过滤重新打开
func TestServerDisableSurvivesLocalScan(t *testing.T) {
	t.Cleanup(func() {
		ClearServerOverride()
		SetEnvGate(true)
		Configure(DefaultRule())
	})

	ConfigureServer(DisableRule())
	Configure(Rule{Enabled: true, Mode: ModeCustom, Keep: []string{"br0"}})
	if Enabled() {
		t.Fatalf("服务端熔断后本地扫描不应恢复过滤")
	}
	if p := Predicate(); p("docker0") != true || p("br0") != true {
		t.Fatalf("熔断时谓词必须全放行")
	}
}

// env 熔断优先级最高，压过服务端覆盖
func TestEnvGateBeatsServerRule(t *testing.T) {
	t.Cleanup(func() {
		SetEnvGate(true)
		ClearServerOverride()
		Configure(DefaultRule())
	})

	ConfigureServer(Rule{Enabled: true, Mode: ModeCustom, Keep: []string{"br0"}})
	SetEnvGate(false)
	if Enabled() {
		t.Fatalf("env 熔断应压过服务端规则")
	}
	if Current().Mode != ModeOff {
		t.Fatalf("env 熔断时 Current 应为 off, got %s", Current().Mode)
	}
}

func containsSuffix(s, suffix string) bool {
	return len(s) >= len(suffix) && s[len(s)-len(suffix):] == suffix
}
