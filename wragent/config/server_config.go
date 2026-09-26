package config

// TunnelConfig 服务端隧道配置
type TunnelConfig struct {
    ID            string `json:"id"`
    Name          string `json:"name"`
    Protocol      string `json:"protocol"`       // "tcp" or "udp", default "tcp"
    LocalPort     int    `json:"local_port"`
    TargetAddr    string `json:"target_addr"`
    TargetAgentID string `json:"target_agent_id"`
    Enabled       bool   `json:"enabled"`
}

// ServerConfig 服务端下发的配置
type ServerConfig struct {
	WSReconnectInterval int           `json:"ws_reconnect_interval"`
	WSHeartbeatInterval int           `json:"ws_heartbeat_interval"`
	LogLevel            string        `json:"log_level"`
	Tunnels             []TunnelConfig `json:"tunnels"`
}

// DefaultServerConfig 返回默认服务端配置
func DefaultServerConfig() *ServerConfig {
	return &ServerConfig{
		WSReconnectInterval: 5,
		WSHeartbeatInterval: 30,
		LogLevel:            "info",
		Tunnels:             []TunnelConfig{},
	}
}
