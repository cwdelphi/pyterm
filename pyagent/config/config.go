package config

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strconv"
	"strings"
)

// 启动模式
const (
	ModeAgent   = "agent"
	ModeGateway = "gateway"
)

// Config wr 单二进制的统一配置：mode 决定以 agent 还是 gateway 角色启动。
// agent 角色用 ServerURL/AgentID/AuthToken；gateway 角色用 Listen/TLSCert/TLSKey/GatewayID/GatewayToken。
type Config struct {
	// Mode 启动模式: "agent" | "gateway"。空串等价于 "agent"（向后兼容既有 agent 配置）。
	Mode string `json:"mode,omitempty"`

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

	// ---- gateway 专用 ----
	Listen    string `json:"listen,omitempty"`
	TLSCert   string `json:"tls_cert,omitempty"`
	TLSKey    string `json:"tls_key,omitempty"`
	GatewayID string `json:"gateway_id,omitempty"`

	GatewayToken       string `json:"gateway_token,omitempty"`
	HeartbeatInterval  int    `json:"heartbeat_interval,omitempty"`
	ReadTimeout        int    `json:"read_timeout,omitempty"`
	WriteTimeout       int    `json:"write_timeout,omitempty"`
	InsecureSkipVerify bool   `json:"insecure_skip_verify,omitempty"`
	// ICE 接口过滤（P0, docs/传输优化_ICE优化与接口过滤方案_v2.0.md §4.2）
	IceInterfaceFilter bool `json:"ice_interface_filter"`
	IceAllowTailscale  bool `json:"ice_allow_tailscale"`
	// P2 §4.5 三级回退阶梯的运维兜底开关（后台 config_json.ice 优先, 此处缺省值/env 兜底）
	IceAutoFallback bool `json:"ice_auto_fallback"`
	IcePathCache    bool `json:"ice_path_cache"`
}

// DefaultConfig 返回默认配置（agent 语义 + gateway 超时/ICE 缺省）
func DefaultConfig() *Config {
	return &Config{
		Mode:               ModeAgent,
		ServerURL:          "ws://localhost:5588/api/ws/webrtc",
		AgentID:            "agent-001",
		AuthToken:          "",
		LogLevel:           "info",
		HostKeyPolicy:      "tofu",
		KnownHostsFile:     "",
		Listen:             ":443",
		HeartbeatInterval:  30,
		ReadTimeout:        60,
		WriteTimeout:       60,
		IceInterfaceFilter: true,
		IceAutoFallback:    true,
		IcePathCache:       true,
	}
}

// Load 从文件加载配置；文件不存在时返回默认值（agent 场景允许首次启动自动生成）。
// WRG_* 环境变量按原有网关行为覆盖对应字段。
func Load(path string) (*Config, error) {
	cfg := DefaultConfig()
	if path == "" {
		path = "config.json"
	}
	data, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			applyEnv(cfg)
			return cfg, nil
		}
		return nil, err
	}
	if err := json.Unmarshal(data, cfg); err != nil {
		return nil, err
	}
	applyEnv(cfg)
	if cfg.Mode == "" {
		cfg.Mode = ModeAgent
	}
	return cfg, nil
}

func applyEnv(cfg *Config) {
	if v := os.Getenv("PYAGENT_MODE"); v != "" {
		cfg.Mode = v
	} else if v := os.Getenv("WR_MODE"); v != "" { // 旧名兼容
		cfg.Mode = v
	}
	if v := os.Getenv("WRG_LISTEN"); v != "" {
		cfg.Listen = v
	}
	if v := os.Getenv("WRG_SERVER_URL"); v != "" {
		cfg.ServerURL = v
	}
	if v := os.Getenv("WRG_GATEWAY_ID"); v != "" {
		cfg.GatewayID = v
	}
	if v := os.Getenv("WRG_GATEWAY_TOKEN"); v != "" {
		cfg.GatewayToken = v
	}
	if b, ok := boolEnv("WRG_ICE_INTERFACE_FILTER"); ok {
		cfg.IceInterfaceFilter = b
	}
	if b, ok := boolEnv("WRG_ICE_ALLOW_TAILSCALE"); ok {
		cfg.IceAllowTailscale = b
	}
	if b, ok := boolEnv("WRG_ICE_AUTO_FALLBACK"); ok {
		cfg.IceAutoFallback = b
	}
	if b, ok := boolEnv("WRG_ICE_PATH_CACHE"); ok {
		cfg.IcePathCache = b
	}
}

func boolEnv(key string) (bool, bool) {
	v := os.Getenv(key)
	if v == "" {
		return false, false
	}
	b, err := strconv.ParseBool(v)
	if err != nil {
		return false, false
	}
	return b, true
}

// IsGateway 是否以网关角色启动
func (c *Config) IsGateway() bool {
	return strings.EqualFold(c.Mode, ModeGateway)
}

// Save 保存配置到文件。持久化模式与身份字段——mode 必须保留，
// 否则 agent setup/token 续写后重启会退化为网关（或反之）。
func (c *Config) Save(path string) error {
	if path == "" {
		path = "config.json"
	}
	dir := filepath.Dir(path)
	if err := os.MkdirAll(dir, 0755); err != nil {
		return err
	}
	slim := struct {
		Mode      string `json:"mode,omitempty"`
		ServerURL string `json:"server_url"`
		AgentID   string `json:"agent_id"`
		AuthToken string `json:"auth_token"`
	}{
		Mode:      c.Mode,
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
