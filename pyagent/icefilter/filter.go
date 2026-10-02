// Package icefilter 实现 ICE 网络接口过滤规则的匹配、进程级开关与熔断。
// 方案：docs/传输优化_ICE优化与接口过滤方案_v2.0.md
package icefilter

import (
	"fmt"
	"strings"
	"sync"
)

// 规则模式
const (
	ModeAuto      = "auto"      // 自动评分生成 keep 集（白名单）
	ModeCustom    = "custom"    // 后台自定义 keep 集（白名单）
	ModeBlacklist = "blacklist" // 仅按 drop_prefix 过滤
	ModeOff       = "off"       // 不过滤
)

// DefaultDropPrefixes 硬黑名单前缀（不可手工放开）。
// 排雷要点（v1.0 §3）："br-" 必须带连字符，绝不命中 br0（真实 LAN 网桥）。
var DefaultDropPrefixes = []string{
	"br-", "docker", "veth", "virbr", "vbox", "vmnet",
	"zt", "cni", "flannel", "cali", "kube", "weave",
}

// DefaultDropNames 精确名硬黑名单（loopback 与常见隧道设备）
var DefaultDropNames = []string{"lo", "sit0", "ip6tnl0", "ip6_vti0", "teql0", "tunl0"}

// TailscalePrefix 缺省过滤（决策 D2：allow_tailscale 缺省 false）
const TailscalePrefix = "tailscale"

// Rule 过滤规则（与后台 config_json.ice 字段对齐）
type Rule struct {
	Enabled        bool     `json:"enabled"`
	Mode           string   `json:"mode"`
	Keep           []string `json:"keep"`
	DropPrefix     []string `json:"drop_prefix"`
	AllowTailscale bool     `json:"allow_tailscale"`
	// P2 §4.5: 三级回退阶梯 / 路径缓存（nil=未下发, 按缺省"开"处理）
	AutoFallback *bool `json:"auto_fallback,omitempty"`
	PathCache    *bool `json:"path_cache,omitempty"`
	// P3: L0 连接复用（本期仅占位, 无后端行为）
	ConnReuse *bool `json:"conn_reuse,omitempty"`
}

// FallbackOn 是否允许 ICE 失败回退阶梯（缺省开）
func (r Rule) FallbackOn() bool {
	return r.AutoFallback == nil || *r.AutoFallback
}

// CacheOn 是否启用路径缓存（缺省开）
func (r Rule) CacheOn() bool {
	return r.PathCache == nil || *r.PathCache
}

// ReuseOn 连接复用（P3 占位, 缺省关）
func (r Rule) ReuseOn() bool {
	return r.ConnReuse != nil && *r.ConnReuse
}

// DefaultRule 默认规则：启用 + 自动模式 + 默认硬黑名单 + 不允许 Tailscale
func DefaultRule() Rule {
	return Rule{
		Enabled:        true,
		Mode:           ModeAuto,
		DropPrefix:     append([]string(nil), DefaultDropPrefixes...),
		AllowTailscale: false,
	}
}

// DisableRule 关闭过滤（L3 全量 ICE / 1 键熔断）
func DisableRule() Rule {
	return Rule{Enabled: false, Mode: ModeOff, AllowTailscale: false}
}

// Normalize 校验并归一化规则。
// 返回 degraded=true 表示 keep 集为空（或模式非法），已降级为「仅黑名单」模式，
// 调用方应上报 rule_invalid 指标（pion/webrtc#3176：全过滤会导致 answerer 侧 SCTP 失败）。
func (r Rule) Normalize() (Rule, bool) {
	if r.Mode == "" {
		r.Mode = ModeAuto
	}
	switch r.Mode {
	case ModeAuto, ModeCustom, ModeBlacklist, ModeOff:
	default:
		r.Mode = ModeBlacklist
	}

	// 硬黑名单永远合并，不可被后台删除
	drop := append([]string(nil), DefaultDropPrefixes...)
	for _, p := range r.DropPrefix {
		p = strings.TrimSpace(p)
		if p == "" || containsStr(drop, p) {
			continue
		}
		drop = append(drop, p)
	}
	if !r.AllowTailscale && !containsStr(drop, TailscalePrefix) {
		drop = append(drop, TailscalePrefix)
	}
	r.DropPrefix = drop

	r.Keep = dedupeNonEmpty(r.Keep)
	if !r.Enabled || r.Mode == ModeOff {
		return r, false
	}
	degraded := false
	if r.Mode == ModeAuto || r.Mode == ModeCustom {
		if len(r.Keep) == 0 {
			// 保底：绝不产生「全过滤」谓词
			r.Mode = ModeBlacklist
			degraded = true
		}
	}
	return r, degraded
}

// Match 谓词：返回 true 表示保留该接口（pion SettingEngine.SetInterfaceFilter 语义）
func (r Rule) Match(name string) bool {
	if name == "" || !r.Enabled || r.Mode == ModeOff {
		return true
	}
	if containsStr(DefaultDropNames, name) {
		return false
	}
	for _, p := range r.DropPrefix {
		if p != "" && strings.HasPrefix(name, p) {
			return false
		}
	}
	if r.Mode == ModeBlacklist {
		return true
	}
	for _, k := range r.Keep {
		if k == name {
			return true
		}
	}
	return false
}

// Summary 单行日志摘要
func (r Rule) Summary() string {
	if !r.Enabled || r.Mode == ModeOff {
		return "disabled"
	}
	return fmt.Sprintf("mode=%s keep=[%s] drop_prefix=%d", r.Mode, strings.Join(r.Keep, ","), len(r.DropPrefix))
}

// ── 进程级状态（热更新，仅影响下一次建连）────────────────────────

var (
	mu      sync.RWMutex
	current = func() Rule { r, _ := DefaultRule().Normalize(); return r }()
	envGate = true  // 进程级熔断（env 关闭时无条件不过滤）
	remote  = false // 服务端覆盖：true 时本地自扫描不再改写 current
)

// Configure 应用新规则（本地自扫描入口）；返回 degraded 表示 keep 为空已降级。
// 服务端覆盖生效期间（ConfigureServer 之后）本地扫描被忽略，仅作断连兜底，
// 避免 5min 周期重扫把后台下发的 keep 集冲掉（方案 §4.1 P1 优先级）。
func Configure(r Rule) bool {
	norm, degraded := r.Normalize()
	mu.Lock()
	if remote {
		mu.Unlock()
		return false
	}
	current = norm
	mu.Unlock()
	return degraded
}

// ConfigureServer 应用后台 config_json.ice 下发的规则（优先级高于本地自扫描）；
// 返回 degraded 表示 keep 为空已降级
func ConfigureServer(r Rule) bool {
	norm, degraded := r.Normalize()
	mu.Lock()
	current = norm
	remote = true
	mu.Unlock()
	return degraded
}

// ClearServerOverride 解除服务端覆盖，恢复本地自扫描驱动（mode=auto 时调用）
func ClearServerOverride() {
	mu.Lock()
	remote = false
	mu.Unlock()
}

// ServerOverride 是否处于服务端规则覆盖状态
func ServerOverride() bool {
	mu.RLock()
	defer mu.RUnlock()
	return remote
}

// SetEnvGate 进程级熔断（false = 强制不过滤，优先级最高）
func SetEnvGate(ok bool) {
	mu.Lock()
	envGate = ok
	mu.Unlock()
}

// EnvGate 返回当前进程级熔断状态
func EnvGate() bool {
	mu.RLock()
	defer mu.RUnlock()
	return envGate
}

// Current 返回当前生效规则（含 envGate 折算后的 Enabled）
func Current() Rule {
	mu.RLock()
	r := current
	gate := envGate
	mu.RUnlock()
	if !gate {
		return DisableRule()
	}
	return r
}

// Enabled 是否启用过滤
func Enabled() bool {
	r := Current()
	return r.Enabled && r.Mode != ModeOff
}

// Predicate 返回用于 SettingEngine.SetInterfaceFilter 的谓词（每次建连取一次快照）
func Predicate() func(string) bool {
	r := Current()
	if !r.Enabled || r.Mode == ModeOff {
		return func(string) bool { return true }
	}
	return r.Match
}

// Summary 当前状态单行摘要
func Summary() string {
	if !EnvGate() {
		return "env-gate disabled"
	}
	src := "local"
	if ServerOverride() {
		src = "server"
	}
	return Current().Summary() + " src=" + src
}

// ── 小工具 ──────────────────────────────────────────────────────

func containsStr(list []string, v string) bool {
	for _, s := range list {
		if s == v {
			return true
		}
	}
	return false
}

func dedupeNonEmpty(in []string) []string {
	out := make([]string, 0, len(in))
	seen := make(map[string]struct{}, len(in))
	for _, s := range in {
		s = strings.TrimSpace(s)
		if s == "" {
			continue
		}
		if _, ok := seen[s]; ok {
			continue
		}
		seen[s] = struct{}{}
		out = append(out, s)
	}
	return out
}
