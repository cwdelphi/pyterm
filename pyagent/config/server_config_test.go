package config

// 阶段D: NormalizePlugins 双通路归一测试

import (
	"encoding/json"
	"testing"
)

func rawTunnels(t *testing.T, m map[string]json.RawMessage, key string) []TunnelConfig {
	t.Helper()
	raw, ok := m[key]
	if !ok {
		return nil
	}
	var doc struct {
		Tunnels []TunnelConfig `json:"tunnels"`
	}
	if err := json.Unmarshal(raw, &doc); err != nil {
		t.Fatalf("unmarshal %s: %v", key, err)
	}
	return doc.Tunnels
}

func TestNormalize_LegacySplitsByProtocol(t *testing.T) {
	cfg := &ServerConfig{
		Tunnels: []TunnelConfig{
			{ID: "t1", Protocol: "tcp", LocalPort: 3333, Enabled: true},
			{ID: "s1", Protocol: "socks5", LocalPort: 2080, Enabled: true},
			{ID: "u1", Protocol: "udp", LocalPort: 5353, Enabled: true},
		},
	}
	out := cfg.NormalizePlugins()

	tun := rawTunnels(t, out, "tunnel")
	if len(tun) != 2 {
		t.Fatalf("tunnel tunnels = %d, want 2(tcp+udp)", len(tun))
	}
	if tun[0].ID != "t1" || tun[1].ID != "u1" {
		t.Fatalf("tunnel ids = %s,%s want t1,u1", tun[0].ID, tun[1].ID)
	}
	sk := rawTunnels(t, out, "socks5")
	if len(sk) != 1 || sk[0].ID != "s1" {
		t.Fatalf("socks5 tunnels = %+v, want [s1]", sk)
	}
}

func TestNormalize_PluginsDirectWhenPresent(t *testing.T) {
	cfg := &ServerConfig{
		// 镜像仍在, 但 plugins 存在 → 直用 plugins, 镜像不驱动本地
		Tunnels: []TunnelConfig{
			{ID: "mirror", Protocol: "tcp", LocalPort: 3333, Enabled: true},
		},
		Plugins: map[string]json.RawMessage{
			"tunnel": json.RawMessage(`{"tunnels":[{"id":"real","protocol":"tcp","local_port":4444,"enabled":true}]}`),
		},
	}
	out := cfg.NormalizePlugins()
	if _, ok := out["socks5"]; ok {
		t.Fatal("socks5 key should be absent")
	}
	tun := rawTunnels(t, out, "tunnel")
	if len(tun) != 1 || tun[0].ID != "real" {
		t.Fatalf("tunnel = %+v, want [real] (plugins direct, mirror ignored)", tun)
	}
}

func TestNormalize_EmptyBothWays(t *testing.T) {
	cfg := &ServerConfig{}
	out := cfg.NormalizePlugins()
	if len(out) != 0 {
		t.Fatalf("want empty map, got %v", out)
	}
}

func TestNormalize_LegacyNoSocks5OmitsKey(t *testing.T) {
	cfg := &ServerConfig{
		Tunnels: []TunnelConfig{{ID: "t1", Protocol: "tcp", LocalPort: 1, Enabled: true}},
	}
	out := cfg.NormalizePlugins()
	if _, ok := out["socks5"]; ok {
		t.Fatal("socks5 key should be omitted when no socks5 tunnels")
	}
	if _, ok := out["tunnel"]; !ok {
		t.Fatal("tunnel key expected")
	}
}
