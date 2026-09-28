package plugin

import (
	"encoding/json"
	"errors"
	"testing"
)

// fakePlugin 记录调用的假插件
type fakePlugin struct {
	name       string
	reconciles []json.RawMessage
	stops      int
	status     Status
	panick     bool
	err        error
}

func (f *fakePlugin) Name() string { return f.name }
func (f *fakePlugin) Reconcile(cfg json.RawMessage) error {
	if f.panick {
		panic("boom")
	}
	f.reconciles = append(f.reconciles, cfg)
	f.status.Running = cfg != nil
	return f.err
}
func (f *fakePlugin) Stop() error {
	f.stops++
	f.status.Running = false
	return nil
}
func (f *fakePlugin) Status() Status { return f.status }

// TC-PL01: 分发/键缺失→nil/幂等直通/重复注册拒绝/未知键忽略
func TestReconcileDispatchAndMissingKey(t *testing.T) {
	a := &fakePlugin{name: "tunnel"}
	b := &fakePlugin{name: "ssh"}
	m := NewManager(Deps{Logf: func(string, ...any) {}})
	if err := m.Register(a); err != nil {
		t.Fatal(err)
	}
	if err := m.Register(b); err != nil {
		t.Fatal(err)
	}
	if err := m.Register(&fakePlugin{name: "tunnel"}); err == nil {
		t.Fatal("重复注册应报错")
	}

	cfgs := map[string]json.RawMessage{
		"tunnel": json.RawMessage(`{"tunnels":[]}`),
		// ssh 键缺失 → 应收到 nil
		"unknown_future": json.RawMessage(`{}`),
	}
	m.Reconcile(cfgs)
	m.Reconcile(cfgs) // 幂等直通：由插件自行跳过，manager 不产生副作用

	if len(a.reconciles) != 2 {
		t.Fatalf("tunnel 应收到2次调用, got %d", len(a.reconciles))
	}
	if string(a.reconciles[0]) != `{"tunnels":[]}` {
		t.Fatalf("payload 透传错误: %s", a.reconciles[0])
	}
	if len(b.reconciles) != 2 || b.reconciles[0] != nil {
		t.Fatalf("ssh 键缺失应收到 nil, got %v", b.reconciles)
	}
	if st := m.Statuses(); st["tunnel"].Running != true || st["ssh"].Running != false {
		t.Fatalf("状态错误: %+v", st)
	}
}

// TC-PL02: 插件 panic 隔离 — 其余插件不受影响
func TestReconcilePanicIsolation(t *testing.T) {
	bad := &fakePlugin{name: "bad", panick: true}
	good := &fakePlugin{name: "good"}
	m := NewManager(Deps{Logf: func(string, ...any) {}})
	m.Register(bad)
	m.Register(good)

	m.Reconcile(map[string]json.RawMessage{
		"bad":  json.RawMessage(`{}`),
		"good": json.RawMessage(`{}`),
	})

	if len(good.reconciles) != 1 {
		t.Fatal("good 插件应正常收到配置")
	}
	st := m.Statuses()["bad"]
	if st.Running {
		t.Fatal("panic 插件应标记为未运行")
	}
	if st.LastErr == "" {
		t.Fatal("panic 插件应记录 LastErr")
	}
}

// StopAll 隔离测试 + 错误透传
func TestStopAllAndErrorStatus(t *testing.T) {
	f := &fakePlugin{name: "p"}
	m := NewManager(Deps{Logf: func(string, ...any) {}})
	m.Register(f)
	m.Reconcile(map[string]json.RawMessage{"p": json.RawMessage(`{}`)})
	if m.Statuses()["p"].Running != true {
		t.Fatal("应为 running")
	}
	m.StopAll()
	if f.stops != 1 || m.Statuses()["p"].Running {
		t.Fatalf("Stop 后应停止: stops=%d", f.stops)
	}

	f.err = errors.New("listen :8822 failed")
	m.Reconcile(map[string]json.RawMessage{"p": json.RawMessage(`{}`)})
	if m.Statuses()["p"].LastErr != "listen :8822 failed" {
		t.Fatalf("错误应写入 LastErr: %+v", m.Statuses()["p"])
	}
}
