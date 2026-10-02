// Package icecfg 把后台 config_json.ice 映射为 icefilter/netinfo 的生效状态（P1）。
// 方案：docs/传输优化_ICE优化与接口过滤方案_v2.0.md §4.1
package icecfg

import (
	"fmt"
	"strings"
	"time"

	"github.com/ppy-tools/pyagent/icefilter"
	"github.com/ppy-tools/pyagent/netinfo"
	"github.com/ppy-tools/pyagent/pathcache"
)

// Apply 应用后台下发的 ICE 规则，返回生效来源。
//
//	mode=off 或 enabled=false → 1 键熔断（全量 ICE，不过滤），同时挂起本地自扫描
//	mode=auto                → 解除服务端覆盖，恢复本地自扫描；后台参数驱动扫描
//	mode=custom / blacklist  → 服务端规则覆盖本地自扫描（本地扫描仅作断连兜底）
//
// 返回的 error 是「软提示」（keep 降级 / 自扫描失败），规则已生效，调用方记日志即可；
// mode 非法时返回空来源，且不改动当前规则。
func Apply(r *icefilter.Rule) (string, error) {
	if r == nil {
		return "", nil
	}
	switch NormalizeMode(r.Mode) {
	case icefilter.ModeAuto, icefilter.ModeCustom, icefilter.ModeBlacklist, icefilter.ModeOff:
	default:
		return "", fmt.Errorf("未知 ice.mode: %q", r.Mode)
	}
	mode := NormalizeMode(r.Mode)
	opt := &netinfo.Options{AllowTailscale: r.AllowTailscale}

	// P2 §4.5: 路径缓存开关（ice.path_cache, 缺省开）。
	// 总开关关掉/熔断（off）时一并停用：L1 快路径本质也是「只放行一个接口」的过滤。
	pathcache.SetEnabled(r.Enabled && mode != icefilter.ModeOff && r.CacheOn())

	if !r.Enabled || mode == icefilter.ModeOff {
		// 熔断：DisableRule + 服务端覆盖，保证 5min 本地重扫不会把过滤重新打开
		icefilter.ConfigureServer(icefilter.DisableRule())
		netinfo.SetOptions(opt)
		return "off", nil
	}

	if mode == icefilter.ModeAuto {
		icefilter.ClearServerOverride()
		netinfo.SetOptions(opt)
		if _, err := netinfo.Refresh(netinfo.CurrentOptions(*opt)); err != nil {
			return "auto", fmt.Errorf("本地自扫描失败: %w", err)
		}
		return "auto", nil
	}

	degraded := icefilter.ConfigureServer(*r)
	netinfo.SetOptions(opt)
	if degraded {
		return "server", fmt.Errorf("keep 为空已降级为仅黑名单(pion/webrtc#3176)")
	}
	return "server", nil
}

// Cooldown 返回 ICE 失败重试节流窗口（D4：默认 2s，0–30s）
func Cooldown(sec int) time.Duration {
	if sec < 0 {
		sec = 0
	}
	if sec > 30 {
		sec = 30
	}
	return time.Duration(sec) * time.Second
}

// NormalizeMode 后台入参 mode 归一（空 → auto）
func NormalizeMode(m string) string {
	m = strings.TrimSpace(strings.ToLower(m))
	if m == "" {
		return icefilter.ModeAuto
	}
	return m
}
