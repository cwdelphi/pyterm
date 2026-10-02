// signal_vnc.go — 由 signal.go 按域拆分（R1/R2 纯移动，无逻辑变更）。
package webrtc

import (
	"context"
	"encoding/json"
	"io"
	"log"
	"net"
	"strconv"
	"time"

	"github.com/pion/webrtc/v4"
	"github.com/ppy-tools/pyagent/logger"
)

// vncSession 保存 VNC 桥接状态
type vncSession struct {
	cancel  context.CancelFunc
	inputCh chan []byte
}

// bridgeSSHToDataChannel 桥接 WebRTC DataChannel 到目标 SSH 服务器
// 使用 API 模式: dc.OnMessage 读取, dc.Send 写入

type VNCConnectMsg struct {
	Type        string      `json:"type"`
	Host        string      `json:"host"`
	Port        int         `json:"port"`
	Password    string      `json:"password"`
	PixelFormat string      `json:"pixel_format"`
	ColorDepth  interface{} `json:"color_depth"`
	ReadOnly    bool        `json:"read_only"`
}

// reportVNCError sends VNC error diagnostics to browser
func (h *SignalHandler) reportVNCError(dc *webrtc.DataChannel, stage string, errMsg string, tcpMs int64, host string, port int, roomID string) {
	diagMsg, _ := json.Marshal(map[string]interface{}{
		"_type":          "diagnostics_error",
		"error_stage":    stage,
		"error_msg":      errMsg,
		"agent_tcp_ms":   tcpMs,
		"agent_ssh_host": host,
		"agent_ssh_port": port,
	})
	if err := h.sendDCMsg(dc, MsgDiagnostics, diagMsg); err != nil {
		log.Printf("DC Send 0xFE VNC error failed: err=%v", err)
	}
	// 同时通过WS发送，确保gateway模式下浏览器也能收到
	h.sendWSDiagnostics(roomID, diagMsg)
}

// bridgeVNC 桥接 WebRTC DataChannel 到目标 VNC 服务器 (透明 TCP 代理)
// Agent 不做 RFB 解析，让 noVNC 处理完整 RFB 协议
func (h *SignalHandler) bridgeVNC(ctx context.Context, dc *webrtc.DataChannel, roomID string, connectPayload []byte, inputCh chan []byte) {
	// V0: 此处禁止 close(inputCh)。handler DC 读循环的 MsgVNCInput 投递只判 vnc!=nil，
	// bridge 提前退出（拨号失败/TCP/DC 错误）后 handler 侧 vnc 不会置 nil，对已关
	// 通道 send 会 panic "send on closed channel"（S4 并发压测实测 ×8 次）。
	// 消费 goroutine 已由 done/ctx.Done 终止，不依赖 close 退出；通道与残留队列
	// （≤64 帧）随 vncSession 被 GC。若未来确需 close，必须与投递侧共持一把锁
	// （closed 标志），任何单侧 close 都会复现该 panic。

	var connMsg VNCConnectMsg
	if err := json.Unmarshal(connectPayload, &connMsg); err != nil {
		h.sendDCErrorMsg(dc, "VNC连接指令解析失败: "+err.Error())
		return
	}

	addr := net.JoinHostPort(connMsg.Host, strconv.Itoa(connMsg.Port))
	log.Printf("[A-BRIDGE] VNC connect: %s (room=%s)", addr, roomID)

	// TCP connect to VNC server
	vncStart := time.Now()
	tcpConn, err := net.DialTimeout("tcp", addr, 10*time.Second)
	tcpMs := time.Since(vncStart).Milliseconds()
	if err != nil {
		h.reportVNCError(dc, "agent_tcp", "VNC连接失败: "+err.Error(), tcpMs, connMsg.Host, connMsg.Port, roomID)
		h.sendDCErrorMsg(dc, "VNC连接失败: "+err.Error())
		return
	}
	defer tcpConn.Close()
	log.Printf("[A-BRIDGE] VNC TCP connected: %s (room=%s)", addr, roomID)

	// Notify browser that TCP connection is ready
	resultData, _ := json.Marshal(map[string]interface{}{
		"type": "vnc_connect_result",
		"ok":   true,
	})
	h.sendDCMsg(dc, MsgVNCConnect, resultData)

	// 发送VNC诊断消息(0xFE) — 同时通过DC和WS双路发送，确保浏览器收到
	diagMsg, _ := json.Marshal(map[string]interface{}{
		"_type":            "diagnostics",
		"agent_connect_ms": tcpMs,
		"agent_tcp_ms":     tcpMs,
		"agent_ssh_ms":     0,
		"agent_shell_ms":   0,
		"agent_ssh_host":   connMsg.Host,
		"agent_ssh_port":   connMsg.Port,
	})
	if err := h.sendDCMsg(dc, MsgDiagnostics, diagMsg); err != nil {
		log.Printf("VNC DC Send 0xFE failed: room=%s err=%v", roomID, err)
	}
	// 同时通过WS发送，确保gateway模式下浏览器也能收到诊断数据
	h.sendWSDiagnostics(roomID, diagMsg)
	log.Printf("VNC连接诊断: %s (room=%s, tcp=%dms)", addr, roomID, tcpMs)

	// Bidirectional bridge with context cancellation
	done := make(chan struct{})

	// TCP -> DataChannel (VNC server responses to browser)
	go func() {
		defer close(done)
		// 读缓冲须保证 1 字节前缀 + n ≤ 65535（pion 接收侧读缓冲上限），
		// 64KB 满读会得到 65537 字节消息：发送侧报 ErrOutboundPacketTooLarge，
		// 且对端接收会因超限关闭 DataChannel。
		buf := make([]byte, 60*1024)
		for {
			// 背压：DC 缓冲/待发队列高时暂停读 TCP，让 VNC 服务端降速（防丢帧）
			if !h.waitVCNSendCapacity(ctx, dc) {
				return
			}
			n, err := tcpConn.Read(buf)
			if n > 0 {
				// RFB 下行每条消息都进这里：级别判断前移，Info 级零开销且不再额外 copy
				if logger.Enabled(logger.LevelDebug) {
					if n <= 80 {
						logger.Debugf("[A-BRIDGE] VNC TCP recv %d bytes: %s (room=%s)", n, hexDump(buf[:n]), roomID)
					} else {
						logger.Debugf("[A-BRIDGE] VNC TCP recv %d bytes (room=%s)", n, roomID)
					}
				}
				if sendErr := h.sendDCMsg(dc, MsgVNCData, buf[:n]); sendErr != nil {
					log.Printf("[A-BRIDGE] VNC DC send failed (room=%s): %v", roomID, sendErr)
					return
				}
			}
			if err != nil {
				if err != io.EOF {
					log.Printf("[A-BRIDGE] VNC TCP read error (room=%s): %v", roomID, err)
				}
				return
			}
		}
	}()

	// DataChannel input -> TCP (browser/noVNC requests to VNC server)
	go func() {
		for {
			select {
			case msg, ok := <-inputCh:
				if !ok {
					return
				}
				if len(msg) > 1 {
					if _, err := tcpConn.Write(msg[1:]); err != nil {
						log.Printf("[A-BRIDGE] VNC TCP write error (room=%s): %v", roomID, err)
						return
					}
				}
			case <-ctx.Done():
				return
			case <-done:
				return
			}
		}
	}()

	// Wait: either TCP read finishes or context is cancelled
	select {
	case <-done:
		// TCP side ended (server closed or error)
	case <-ctx.Done():
		// Browser requested disconnect — close TCP to stop everything
		log.Printf("[A-BRIDGE] VNC context cancelled, closing TCP (room=%s)", roomID)
		tcpConn.Close()
		<-done // wait for goroutines to finish
	}
	log.Printf("[A-BRIDGE] VNC bridge closed (room=%s)", roomID)
}
