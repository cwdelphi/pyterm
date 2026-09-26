package config

import (
	"encoding/json"
	"os"
)

type Config struct {
	Listen      string `json:"listen"`
	TLSCert     string `json:"tls_cert"`
	TLSKey      string `json:"tls_key"`
	ServerURL   string `json:"server_url"`
	GatewayID   string `json:"gateway_id"`
	GatewayToken string `json:"gateway_token"`
	LogLevel    string `json:"log_level"`
	HeartbeatInterval int  `json:"heartbeat_interval"`
	ReadTimeout       int  `json:"read_timeout"`
	WriteTimeout      int  `json:"write_timeout"`
	InsecureSkipVerify bool `json:"insecure_skip_verify"`
}

func Load(path string) (*Config, error) {
	cfg := &Config{
		Listen:            ":443",
		LogLevel:          "info",
		HeartbeatInterval: 30,
		ReadTimeout:       60,
		WriteTimeout:      60,
	}
	if path != "" {
		data, err := os.ReadFile(path)
		if err != nil {
			return nil, err
		}
		if err := json.Unmarshal(data, cfg); err != nil {
			return nil, err
		}
	}
	// 环境变量覆盖
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
	return cfg, nil
}
