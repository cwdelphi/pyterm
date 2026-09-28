package webrtc

// webterm: Agent 本地 shell 控制台 —— 浏览器经 WebRTC DataChannel 直起 Agent
// 主机上的登录 shell(creack/pty), 不监听端口、不走 SSH 协议、无需 sshd。
// 首帧复用 ssh_connect(0x01) JSON, 仅多带 mode:"local" 字段(网关整体重打包自动透传)。

import (
	"encoding/json"
	"log"
	"os"
	"os/exec"
	"sync"
	"time"

	"github.com/creack/pty"
	"github.com/pion/webrtc/v4"
)

// connectLocal 启动 Agent 本地 shell 并桥接 DC ↔ PTY。
// 返回 sshSession: sshConn/session 为 nil, stdin=pty, pty/cmd 供 resize/回收。
func (h *SignalHandler) connectLocal(dc *webrtc.DataChannel, connectMsg *SSHConnectMsg, roomID string) *sshSession {
	tStart := time.Now()
	cols := connectMsg.Cols
	if cols <= 0 {
		cols = 120
	}
	rows := connectMsg.Rows
	if rows <= 0 {
		rows = 40
	}

	shell := os.Getenv("SHELL")
	if shell == "" {
		shell = "/bin/bash"
	}
	if _, err := exec.LookPath(shell); err != nil {
		shell = "/bin/sh"
	}
	cmd := exec.Command(shell, "-l")
	cmd.Env = append(os.Environ(), "TERM=xterm-256color")

	f, err := pty.StartWithSize(cmd, &pty.Winsize{Rows: uint16(rows), Cols: uint16(cols)})
	if err != nil {
		log.Printf("[WEBTERM] pty启动失败 room=%s: %v", roomID, err)
		h.reportAgentError(dc, "agent_shell", err.Error(), 0, 0, 0, "local", 0)
		h.sendDCErrorMsg(dc, "本地shell启动失败: "+err.Error())
		return nil
	}

	sess := &sshSession{dc: dc, stdin: f, pty: f, cmd: cmd}

	// 诊断 0xFE: 与 SSH 路径同构, 首帧终端数据之后发送
	tShellDone := time.Since(tStart).Milliseconds()
	diagMsg, _ := json.Marshal(map[string]interface{}{
		"_type":            "diagnostics",
		"agent_connect_ms": tShellDone,
		"agent_tcp_ms":     0,
		"agent_ssh_ms":     0,
		"agent_shell_ms":   tShellDone,
		"agent_ssh_host":   "local",
		"agent_ssh_port":   0,
		"mode":             "local",
	})
	var diagSent sync.Once
	sendDiagOnce := func() {
		diagSent.Do(func() {
			if err := h.sendDCMsg(dc, MsgDiagnostics, diagMsg); err != nil {
				log.Printf("[WEBTERM] DC Send 0xFE failed, fallback to WS: room=%s err=%v", roomID, err)
				h.sendWSDiagnostics(roomID, diagMsg)
			}
		})
	}

	// PTY 输出 → DC
	go func() {
		buf := make([]byte, 32*1024)
		for {
			n, err := f.Read(buf)
			if n > 0 {
				h.sendDCTerminalData(dc, buf[:n])
				sendDiagOnce()
			}
			if err != nil {
				break
			}
		}
		sendDiagOnce()
	}()

	// shell 退出 → 通知前端并关闭 DC(前端置 disconnected, 可重连重建 shell)
	go func() {
		_ = cmd.Wait()
		log.Printf("[WEBTERM] shell已退出 (room=%s)", roomID)
		sendDiagOnce()
		time.Sleep(150 * time.Millisecond) // 冲刷残留终端输出
		_ = dc.Close()
	}()

	log.Printf("[WEBTERM] 本地shell就绪: %s -l (room=%s, %d x %d, connect=%dms)",
		shell, roomID, cols, rows, tShellDone)
	return sess
}

// closeLocal 终止本地 shell(关 pty + 杀进程), 幂等可重复调用
func closeLocal(sess *sshSession) {
	if sess == nil || sess.pty == nil {
		return
	}
	ptyFile := sess.pty
	sess.pty = nil
	sess.stdin = nil
	_ = ptyFile.Close()
	if sess.cmd != nil && sess.cmd.Process != nil {
		_ = sess.cmd.Process.Kill()
	}
}

// closeSSHSession 统一回收会话(R6 重连/OnClose): SSH 与 webterm 各自路径
func closeSSHSession(sess *sshSession) {
	if sess == nil {
		return
	}
	if sess.session != nil {
		_ = sess.session.Close()
	}
	if sess.pty != nil {
		closeLocal(sess)
	}
}
