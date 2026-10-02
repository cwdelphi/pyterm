package config

import (
	"encoding/json"

	"github.com/ppy-tools/pyagent/icefilter"
)

// TunnelConfig 服务端隧道配置
type TunnelConfig struct {
	ID            string `json:"id"`
	Name          string `json:"name"`
	Protocol      string `json:"protocol"` // "tcp"/"udp"/"socks5", default "tcp"
	LocalPort     int    `json:"local_port"`
	TargetAddr    string `json:"target_addr"`
	TargetAgentID string `json:"target_agent_id"`
	Enabled       bool   `json:"enabled"`
	SocksUsername string `json:"socks_username"`
	SocksPassword string `json:"socks_password"`
}

// ServerConfig 服务端下发的配置
type ServerConfig struct {
	WSReconnectInterval int                        `json:"ws_reconnect_interval"`
	WSHeartbeatInterval int                        `json:"ws_heartbeat_interval"`
	ICECooldown         *int                       `json:"ice_cooldown"` // D4: ICE失败重试节流(秒, 0-30); nil=未下发
	LogLevel            string                     `json:"log_level"`
	Tunnels             []TunnelConfig             `json:"tunnels"`         // 兼容镜像(合并列表), 旧通路使用
	Plugins             map[string]json.RawMessage `json:"plugins"`         // 插件字典(键=插件名)
	Ice                 *icefilter.Rule            `json:"ice"`             // P1: 后台 ICE 优化规则(nil=不下发)
	IceCacheClear       bool                       `json:"ice_cache_clear"` // P2: 清空路径缓存(仅随推送, 不落库)
}

// DefaultServerConfig 返回默认服务端配置
func DefaultServerConfig() *ServerConfig {
	return &ServerConfig{
		WSReconnectInterval: 5,
		WSHeartbeatInterval: 30,
		ICECooldown:         IntPtr(2),
		LogLevel:            "info",
		Tunnels:             []TunnelConfig{},
	}
}

// IntPtr 可选标量字段取址（服务端未下发时为 nil）
func IntPtr(v int) *int { return &v }

// NormalizePlugins 配置双通路归一(阶段D, handleConfigUpdate 唯一入口):
//   - 新格式: 存在 plugins → 直用(顶层 tunnels 为旧Agent兼容镜像, 不再驱动本地)
//   - 旧格式: 仅顶层 tunnels → 按 protocol 拆成 plugins{tunnel,socks5}
//   - 两边都缺 → 空 map, 已注册插件收到 nil → 全部停用
func (c *ServerConfig) NormalizePlugins() map[string]json.RawMessage {
	if len(c.Plugins) > 0 {
		return c.Plugins
	}
	var tSplit, sSplit []TunnelConfig
	for _, t := range c.Tunnels {
		if t.Protocol == "socks5" {
			sSplit = append(sSplit, t)
		} else {
			tSplit = append(tSplit, t)
		}
	}
	out := make(map[string]json.RawMessage, 2)
	if len(tSplit) > 0 {
		raw, _ := json.Marshal(map[string]interface{}{"tunnels": tSplit})
		out["tunnel"] = raw
	}
	if len(sSplit) > 0 {
		raw, _ := json.Marshal(map[string]interface{}{"tunnels": sSplit})
		out["socks5"] = raw
	}
	return out
}
