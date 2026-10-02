// Package tunnel 隧道插件适配器(阶段D): 将既有 TunnelManager(tcp/udp)接入插件
// Manager 统一分发。插件包不依赖 webrtc 核心, 经 Deps 闭包注入;
// 配置对比/差量重启仍由 TunnelManager.ReconcileTunnels 负责(迁出前语义不变)。
package tunnel

import (
	"encoding/json"
	"fmt"
	"sync"

	"github.com/ppy-tools/pyagent/config"
	"github.com/ppy-tools/pyagent/plugin"
)

// Deps webrtc 核心注入的隧道能力(由 signal 层构造)
type Deps struct {
	// Reconcile 应用 tcp/udp 隧道配置(内部 diff, 相同配置零重启)
	Reconcile func(tunnels []config.TunnelConfig)
	// Stop 停止全部隧道(键缺失禁用 / 进程退出)
	Stop func()
	// Status 当前运行状态 (running, detail)
	Status func() (bool, string)
	// SendConnectTunnel 向服务端申请到目标 Agent 的隧道通道(幂等, 服务端按目标去重)
	SendConnectTunnel func(agentID string) error
	// Logf 日志输出
	Logf func(format string, args ...any)
}

// Plugin 实现 plugin.Plugin(Name="tunnel")
type Plugin struct {
	deps Deps
	mu   sync.Mutex
}

// New 创建 tunnel 适配器插件
func New(deps Deps) *Plugin {
	if deps.Logf == nil {
		deps.Logf = func(format string, args ...any) {
			fmt.Printf(format+"\n", args...)
		}
	}
	return &Plugin{deps: deps}
}

// Name 插件键 = 服务端 config.plugins 字典键
func (p *Plugin) Name() string { return "tunnel" }

// Reconcile 幂等应用配置:
//   - nil/空 → 停止全部隧道(键缺失=禁用)
//   - {"tunnels":[...]} → 交 TunnelManager diff(防御性再过滤 socks5);
//     随后按目标去重发送 connect_tunnel(迁出前 handleConfigUpdate 同语义)
func (p *Plugin) Reconcile(raw json.RawMessage) error {
	p.mu.Lock()
	defer p.mu.Unlock()

	if len(raw) == 0 || string(raw) == "null" {
		if p.deps.Stop != nil {
			p.deps.Stop()
		}
		p.deps.Logf("[TUNNEL] 配置缺失, 全部隧道已停止")
		return nil
	}

	var doc struct {
		Tunnels []config.TunnelConfig `json:"tunnels"`
	}
	if err := json.Unmarshal(raw, &doc); err != nil {
		return fmt.Errorf("tunnel配置解析失败: %w", err)
	}

	// 防御性过滤: socks5 已归插件通路(normalize 已拆分, 此处兜底)
	list := make([]config.TunnelConfig, 0, len(doc.Tunnels))
	for _, t := range doc.Tunnels {
		if t.Protocol == "socks5" {
			continue
		}
		list = append(list, t)
	}

	if p.deps.Reconcile != nil {
		p.deps.Reconcile(list)
	}

	// 为每个启用且有目标Agent的隧道发送connect_tunnel(按目标去重)
	if p.deps.SendConnectTunnel != nil {
		seen := make(map[string]bool)
		for _, t := range list {
			if t.Enabled && t.TargetAgentID != "" && !seen[t.TargetAgentID] {
				seen[t.TargetAgentID] = true
				if err := p.deps.SendConnectTunnel(t.TargetAgentID); err != nil {
					p.deps.Logf("[TUNNEL] connect_tunnel to %s failed: %v", t.TargetAgentID, err)
				} else {
					p.deps.Logf("[TUNNEL] connect_tunnel sent to %s for tunnel %s", t.TargetAgentID, t.ID)
				}
			}
		}
	}
	return nil
}

// Stop 停止全部隧道(进程退出时 Manager.StopAll 调用)
func (p *Plugin) Stop() error {
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.deps.Stop != nil {
		p.deps.Stop()
	}
	return nil
}

// Status 只读运行状态(交 TunnelManager 统计)
func (p *Plugin) Status() plugin.Status {
	if p.deps.Status == nil {
		return plugin.Status{}
	}
	running, detail := p.deps.Status()
	return plugin.Status{Running: running, Detail: detail}
}
