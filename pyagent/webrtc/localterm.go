package webrtc

// webterm: Agent 本地 shell 控制台 —— 浏览器经 WebRTC DataChannel 直起 Agent
// 主机上的登录 shell(creack/pty), 不监听端口、不走 SSH 协议、无需 sshd。
// 首帧复用 ssh_connect(0x01) JSON, 仅多带 mode:"local" 字段(网关整体重打包自动透传)。
//
// docker 模式(HOST_CONSOLE=1): 容器内运行的 Agent 通过 `nsenter -t 1 -m -u -i -n -p`
// (显式命名空间列表, busybox 与 GNU nsenter 通用; busybox 无 `-a` 选项)
// 进入宿主机命名空间(需容器 pid:host + privileged + 宿主根目录挂载), 打开的是
// **宿主机控制台**而非容器 shell; 裸机(systemd)部署保持原逻辑(即在宿主机本身上起 shell)。

import (
	"encoding/json"
	"log"
	"os"
	"os/exec"
	"strings"
	"sync"
	"time"

	"github.com/creack/pty"
	"github.com/pion/webrtc/v4"
)

const hostConsoleEnv = "HOST_CONSOLE"

// hostConsoleEnabled 是否以 docker 宿主机控制台模式工作(HOST_CONSOLE=1 且容器内有 nsenter)
func hostConsoleEnabled() (bool, string) {
	if os.Getenv(hostConsoleEnv) != "1" {
		return false, ""
	}
	if _, err := exec.LookPath("nsenter"); err == nil {
		return true, "nsenter"
	}
	return false, ""
}

// localShellPath 选择登录 shell: docker 模式固定用宿主机常见 shell,
// 避免容器 SHELL 环境变量指向宿主不存在的路径。
func localShellPath(hostMode bool) string {
	if hostMode {
		// docker 模式: 目标 shell 由 nsenter 在宿主机挂载命名空间内解析, 常见登录 shell 即可
		return "/bin/bash"
	}
	shell := os.Getenv("SHELL")
	if shell == "" {
		shell = "/bin/bash"
	}
	if _, err := exec.LookPath(shell); err != nil {
		shell = "/bin/sh"
	}
	return shell
}

// buildLocalCmd 构造本地登录 shell 命令。
// hostMode: 用 nsenter 进入宿主机 init(PID 1) 的 mnt/uts/ipc/net/pid 命名空间后起 shell。
// 注意: 显式列出命名空间而非 -a(alpine busybox nsenter 不支持 -a), 与 GNU 行为等价。
func buildLocalCmd(hostMode bool, shell string) *exec.Cmd {
	if hostMode {
		args := []string{"-m", "-u", "-i", "-n", "-p", "-t", "1", "--", shell, "-l"}
		return exec.Command("nsenter", args...)
	}
	return exec.Command(shell, "-l")
}

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

	hostMode, _ := hostConsoleEnabled()
	shell := localShellPath(hostMode)
	cmd := buildLocalCmd(hostMode, shell)
	cmd.Env = append(os.Environ(), "TERM=xterm-256color")
	if hostMode {
		// 以宿主机视角的 PATH, 避免容器 PATH 干扰宿主命令解析
		cmd.Env = append([]string{
			"PATH=/usr/local/sbin:/usr/local/bin:/usr/sbin:/usr/bin:/sbin:/bin",
			"HOME=/root",
			"USER=root",
			"LOGNAME=root",
			"TERM=xterm-256color",
		}, os.Environ()...)
	}

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

	modeTag := "容器内"
	if hostMode {
		modeTag = "宿主机(nsenter)"
	}
	cmdStr := strings.Join(cmd.Args, " ")
	log.Printf("[WEBTERM] %s登录shell就绪: %s (room=%s, %d x %d, connect=%dms)",
		modeTag, cmdStr, roomID, cols, rows, tShellDone)
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
