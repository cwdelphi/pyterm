// Package plugin 定义 Agent 插件框架契约：核心(webrtc)禁止被本包 import，
// 插件单向依赖核心；共享能力经 Deps 注入。
package plugin

import (
	"encoding/json"
	"net"
	"time"
)

// Plugin Agent 内插件统一接口。
// Name 与服务端 config.plugins 字典键一一对应（"tunnel"/"socks5"/"ssh"）。
type Plugin interface {
	// Name 插件唯一名称（=plugins 字典键）
	Name() string
	// Reconcile 幂等应用配置：相同配置应快速跳过；cfg=nil（键缺失）表示禁用并停止
	Reconcile(cfg json.RawMessage) error
	// Stop 优雅停止（关端口/断会话/释放资源），可重复调用
	Stop() error
	// Status 只读运行状态
	Status() Status
}

// Status 插件运行状态（每次 Reconcile 后由 Manager 汇总打日志）
type Status struct {
	Running bool      // 是否在运行
	Detail  string    // 运行细节（如监听地址）
	Since   time.Time // 本次启动时间
	LastErr string    // 最近一次错误（成功后清空）
}

// MessageHandler 可选扩展接口：实现它的插件接收 DataChannel 消息分发
// （speedtest 用；类型断言判定，不改 Plugin 主接口）。
// kind 取值 "data"（0x61 负载）/ "ctrl"（0x60 JSON）；panic 由 Manager.Dispatch 隔离。
type MessageHandler interface {
	HandleMessage(kind string, payload []byte)
}

// Deps 核心能力注入（由 signal 层构造 Manager 时传入）
type Deps struct {
	// Bridge 将已接受的 TCP 连接经目标 Agent 的 ICE DataChannel 桥接（socks5/tunnel 插件用）。
	// onResult 在隧道建立成功/失败后同步回调（socks5 用于回 CONNECT 应答），可为 nil。
	Bridge func(conn net.Conn, addr string, onResult func(ok bool))
	// SendConnectTunnel 向服务端申请与目标 Agent 的隧道通道（幂等，服务端按目标去重）
	SendConnectTunnel func(agentID string) error
	// SendSignal 经信令 WS 上报消息到服务端（speedtest progress/result 用），可为 nil。
	// payload 为完整 JSON（含 type/room_id 字段，由 signal 层拆装 ws.Message）。
	SendSignal func(payload []byte) error
	// Logf 日志输出（nil 时用标准 log）
	Logf func(format string, args ...any)
}
