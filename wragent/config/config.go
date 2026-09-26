package config

import (
	"encoding/json"
	"os"
	"path/filepath"
)

// Config wragent本地配置
type Config struct {
	ServerURL string `json:"server_url"`
	AgentID   string `json:"agent_id"`
	AuthToken string `json:"auth_token"`
	LogLevel  string `json:"log_level"`

	// Host key 校验: tofu | strict | insecure（默认 tofu）
	HostKeyPolicy string `json:"host_key_policy,omitempty"`
	// known_hosts 路径（默认与 config 同目录 known_hosts）
	KnownHostsFile string `json:"known_hosts_file,omitempty"`

	// 旧版隧道配置（已废弃，保留用于向后兼容）
	TunnelLocalPort  int    `json:"tunnel_local_port,omitempty"`
	TunnelTargetAddr string `json:"tunnel_target_addr,omitempty"`
	TunnelAgentID    string `json:"tunnel_agent_id,omitempty"`
}

// DefaultConfig 返回默认配置
func DefaultConfig() *Config {
	return &Config{
		ServerURL:      "ws://localhost:5588/api/ws/webrtc",
		AgentID:        "agent-001",
		AuthToken:      "",
		LogLevel:       "info",
		HostKeyPolicy:  "tofu",
		KnownHostsFile: "",
	}
}

// Load 从文件加载配置
func Load(path string) (*Config, error) {
	cfg := DefaultConfig()
	if path == "" {
		path = "config.json"
	}
	data, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return cfg, nil
		}
		return nil, err
	}
	if err := json.Unmarshal(data, cfg); err != nil {
		return nil, err
	}
	return cfg, nil
}

// Save 保存配置到文件（仅持久化身份三字段，其余由服务端 config_json 下发）
func (c *Config) Save(path string) error {
	if path == "" {
		path = "config.json"
	}
	dir := filepath.Dir(path)
	if err := os.MkdirAll(dir, 0755); err != nil {
		return err
	}
	slim := struct {
		ServerURL string `json:"server_url"`
		AgentID   string `json:"agent_id"`
		AuthToken string `json:"auth_token"`
	}{
		ServerURL: c.ServerURL,
		AgentID:   c.AgentID,
		AuthToken: c.AuthToken,
	}
	data, err := json.MarshalIndent(slim, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(path, append(data, '\n'), 0644)
}
