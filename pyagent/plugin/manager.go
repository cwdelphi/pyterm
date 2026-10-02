package plugin

import (
	"encoding/json"
	"fmt"
	"log"
	"reflect"
	"sync"
	"time"
)

// Manager 插件注册与分发器：串行 Reconcile、panic 隔离、状态汇总。
type Manager struct {
	mu       sync.Mutex
	deps     Deps
	plugins  []Plugin
	statuses map[string]*Status
}

// NewManager 创建管理器（deps.Bridge/SendConnectTunnel 可为 nil，由插件自行判空）
func NewManager(deps Deps) *Manager {
	if deps.Logf == nil {
		deps.Logf = func(format string, args ...any) {
			log.Printf(format, args...)
		}
	}
	return &Manager{
		deps:     deps,
		statuses: make(map[string]*Status),
	}
}

// Deps 返回注入能力（插件构造时获取）
func (m *Manager) Deps() Deps { return m.deps }

// Register 注册插件（重复名称报错）；应在首次 Reconcile 前完成
func (m *Manager) Register(p Plugin) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	name := p.Name()
	for _, existing := range m.plugins {
		if existing.Name() == name {
			return fmt.Errorf("plugin %q already registered", name)
		}
	}
	m.plugins = append(m.plugins, p)
	m.statuses[name] = &Status{}
	return nil
}

// Reconcile 按 plugins 字典键分发配置（串行）：
//   - 已注册插件取对应键；键缺失→nil→插件应自行禁用
//   - 未知键仅告警（前向兼容：服务端先行于 Agent 升级）
//   - 单插件 panic 被隔离，不影响其余插件与核心
func (m *Manager) Reconcile(cfgs map[string]json.RawMessage) {
	m.mu.Lock()
	plugins := make([]Plugin, len(m.plugins))
	copy(plugins, m.plugins)
	m.mu.Unlock()

	used := make(map[string]bool, len(plugins))
	for _, p := range plugins {
		name := p.Name()
		used[name] = true
		var raw json.RawMessage
		if v, ok := cfgs[name]; ok {
			raw = v
		}
		m.reconcileOne(p, raw)
	}

	for key := range cfgs {
		if !used[key] {
			m.deps.Logf("[PLUGIN] 未知插件配置键 %q, 已忽略(可能为更高版本能力)", key)
		}
	}
}

func (m *Manager) reconcileOne(p Plugin, raw json.RawMessage) {
	name := p.Name()
	defer func() {
		if r := recover(); r != nil {
			st := m.getStatus(name)
			st.Running = false
			st.LastErr = fmt.Sprintf("reconcile panic: %v", r)
			m.deps.Logf("[PLUGIN] %s Reconcile panic: %v (已隔离, 其余插件不受影响)", name, r)
		}
	}()

	before := p.Status()
	err := p.Reconcile(raw)
	after := p.Status()
	if err != nil {
		after.Running = false
		after.LastErr = err.Error()
		m.deps.Logf("[PLUGIN] %s Reconcile 失败: %v", name, err)
	}
	st := m.getStatus(name)
	*st = after
	_ = before

	if !reflect.DeepEqual(before, after) {
		m.deps.Logf("[PLUGIN] %s running=%v detail=%q err=%q",
			name, after.Running, after.Detail, after.LastErr)
	}
}

func (m *Manager) getStatus(name string) *Status {
	m.mu.Lock()
	defer m.mu.Unlock()
	st, ok := m.statuses[name]
	if !ok {
		st = &Status{}
		m.statuses[name] = st
	}
	return st
}

// Dispatch 向实现 MessageHandler 的插件分发 DataChannel 消息
// （kind: "data"/"ctrl"）。未实现该接口的插件静默跳过；panic 隔离。
func (m *Manager) Dispatch(name, kind string, payload []byte) {
	m.mu.Lock()
	var target Plugin
	for _, p := range m.plugins {
		if p.Name() == name {
			target = p
			break
		}
	}
	m.mu.Unlock()
	if target == nil {
		return
	}
	h, ok := target.(MessageHandler)
	if !ok {
		return
	}
	defer func() {
		if r := recover(); r != nil {
			m.deps.Logf("[PLUGIN] %s HandleMessage panic: %v (已隔离, 其余插件不受影响)", name, r)
		}
	}()
	h.HandleMessage(kind, payload)
}

// Statuses 返回插件状态快照
func (m *Manager) Statuses() map[string]Status {
	m.mu.Lock()
	defer m.mu.Unlock()
	out := make(map[string]Status, len(m.statuses))
	for k, v := range m.statuses {
		out[k] = *v
	}
	return out
}

// StopAll 优雅停止全部插件（进程退出时调用），逐个隔离 panic
func (m *Manager) StopAll() {
	m.mu.Lock()
	plugins := make([]Plugin, len(m.plugins))
	copy(plugins, m.plugins)
	m.mu.Unlock()

	for _, p := range plugins {
		func(p Plugin) {
			defer func() {
				if r := recover(); r != nil {
					m.deps.Logf("[PLUGIN] %s Stop panic: %v", p.Name(), r)
				}
			}()
			wasRunning := p.Status().Running
			if err := p.Stop(); err != nil {
				m.deps.Logf("[PLUGIN] %s Stop 错误: %v", p.Name(), err)
			} else if wasRunning {
				m.deps.Logf("[PLUGIN] %s 已停止", p.Name())
			}
			st := m.getStatus(p.Name())
			*st = p.Status()
		}(p)
	}
}

// Status 便捷构造
func NewStatus(running bool, detail string) Status {
	return Status{Running: running, Detail: detail, Since: time.Now()}
}
