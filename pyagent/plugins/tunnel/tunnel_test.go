package tunnel

// 阶段D: tunnel 适配器接线测试 (TC-PL03/04 的 Go 层等价)

import (
	"encoding/json"
	"errors"
	"testing"

	"github.com/ppy-tools/pyagent/config"
)

type stub struct {
	reconciled [][]config.TunnelConfig
	stopped    int
	targets    []string
	running    bool
	detail     string
}

func newTestDeps(s *stub) Deps {
	return Deps{
		Reconcile: func(ts []config.TunnelConfig) { s.reconciled = append(s.reconciled, ts) },
		Stop:      func() { s.stopped++ },
		Status:    func() (bool, string) { return s.running, s.detail },
		SendConnectTunnel: func(agentID string) error {
			s.targets = append(s.targets, agentID)
			return nil
		},
		Logf: func(string, ...any) {},
	}
}

func reconcile(t *testing.T, p *Plugin, doc string) {
	t.Helper()
	if err := p.Reconcile(json.RawMessage(doc)); err != nil {
		t.Fatalf("reconcile: %v", err)
	}
}

func TestReconcile_ForwardsTcpUdpFiltersSocks5(t *testing.T) {
	s := &stub{}
	p := New(newTestDeps(s))
	reconcile(t, p, `{"tunnels":[
		{"id":"t1","protocol":"tcp","local_port":3333,"target_agent_id":"a1","enabled":true},
		{"id":"u1","protocol":"udp","local_port":5353,"enabled":true},
		{"id":"s1","protocol":"socks5","local_port":2080,"enabled":true}
	]}`)
	if len(s.reconciled) != 1 {
		t.Fatalf("reconcile calls = %d, want 1", len(s.reconciled))
	}
	got := s.reconciled[0]
	if len(got) != 2 {
		t.Fatalf("forwarded %d tunnels, want 2 (socks5 filtered)", len(got))
	}
	if got[0].ID != "t1" || got[1].ID != "u1" {
		t.Fatalf("ids = %s,%s want t1,u1", got[0].ID, got[1].ID)
	}
}

func TestReconcile_NilStops(t *testing.T) {
	s := &stub{running: true, detail: "1 tunnels"}
	p := New(newTestDeps(s))
	if err := p.Reconcile(nil); err != nil {
		t.Fatalf("reconcile nil: %v", err)
	}
	if s.stopped != 1 {
		t.Fatalf("stop calls = %d, want 1", s.stopped)
	}
	// 模拟 TunnelManager.StopAll 后状态归零
	s.running, s.detail = false, ""
	if st := p.Status(); st.Running {
		t.Fatalf("want stopped status, got %+v", st)
	}
}

func TestReconcile_ConnectTunnelDedupedByTarget(t *testing.T) {
	s := &stub{}
	p := New(newTestDeps(s))
	reconcile(t, p, `{"tunnels":[
		{"id":"a","protocol":"tcp","local_port":1001,"target_agent_id":"agt-x","enabled":true},
		{"id":"b","protocol":"tcp","local_port":1002,"target_agent_id":"agt-x","enabled":true},
		{"id":"c","protocol":"tcp","local_port":1003,"target_agent_id":"agt-y","enabled":true},
		{"id":"d","protocol":"tcp","local_port":1004,"target_agent_id":"agt-off","enabled":false},
		{"id":"e","protocol":"tcp","local_port":1005,"enabled":true}
	]}`)
	if len(s.targets) != 2 || s.targets[0] != "agt-x" || s.targets[1] != "agt-y" {
		t.Fatalf("targets = %v, want [agt-x agt-y] (dedupe + skip disabled/no-target)", s.targets)
	}
}

func TestReconcile_SendTunnelErrorDoesNotFailReconcile(t *testing.T) {
	s := &stub{}
	p := New(Deps{
		Reconcile: func(ts []config.TunnelConfig) { s.reconciled = append(s.reconciled, ts) },
		Stop:      func() {},
		Status:    func() (bool, string) { return true, "" },
		SendConnectTunnel: func(agentID string) error {
			return errors.New("target offline")
		},
		Logf: func(string, ...any) {},
	})
	reconcile(t, p, `{"tunnels":[{"id":"a","protocol":"tcp","local_port":1,"target_agent_id":"x","enabled":true}]}`)
	if len(s.reconciled) != 1 {
		t.Fatal("reconcile should proceed despite connect_tunnel error")
	}
}

func TestStatusPassthrough(t *testing.T) {
	s := &stub{running: true, detail: "2 tunnels tcp:3333 udp:5353"}
	p := New(newTestDeps(s))
	st := p.Status()
	if !st.Running || st.Detail != "2 tunnels tcp:3333 udp:5353" {
		t.Fatalf("status = %+v", st)
	}
}

func TestStopStopsTunnels(t *testing.T) {
	s := &stub{}
	p := New(newTestDeps(s))
	if err := p.Stop(); err != nil {
		t.Fatalf("stop: %v", err)
	}
	if s.stopped != 1 {
		t.Fatalf("stop calls = %d, want 1", s.stopped)
	}
}

func TestInvalidJSONReturnsError(t *testing.T) {
	p := New(newTestDeps(&stub{}))
	if err := p.Reconcile(json.RawMessage(`{bad`)); err == nil {
		t.Fatal("want parse error")
	}
}

// TestNormalizeToManager 双通路归一 → Manager 分发 的集成路径
func TestNormalizeToManager(t *testing.T) {
	s := &stub{}
	p := New(newTestDeps(s))
	cfg := &config.ServerConfig{
		Tunnels: []config.TunnelConfig{
			{ID: "t1", Protocol: "tcp", LocalPort: 3333, Enabled: true},
			{ID: "s1", Protocol: "socks5", LocalPort: 2080, Enabled: true},
		},
	}
	cfgs := cfg.NormalizePlugins()
	raw, ok := cfgs["tunnel"]
	if !ok {
		t.Fatal("normalize missing tunnel key")
	}
	if err := p.Reconcile(raw); err != nil {
		t.Fatalf("reconcile: %v", err)
	}
	if len(s.reconciled) != 1 || len(s.reconciled[0]) != 1 || s.reconciled[0][0].ID != "t1" {
		t.Fatalf("reconciled = %+v, want only [t1]", s.reconciled)
	}
	if _, hasSocks5 := cfgs["socks5"]; !hasSocks5 {
		t.Fatal("normalize missing socks5 key (owned by socks5 plugin)")
	}
}
