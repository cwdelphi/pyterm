// signal_ssh.go — 由 signal.go 按域拆分（R1/R2 纯移动，无逻辑变更）。
package webrtc

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
	"net"
	"os"
	"os/exec"
	"sync"
	"time"

	"github.com/creack/pty"
	"github.com/pion/webrtc/v4"
	"github.com/ppy-tools/pyagent/logger"
	gossh "golang.org/x/crypto/ssh"
)

// SSH connection pool
type sshPoolEntry struct {
	client   *gossh.Client
	lastUsed time.Time
	mu       sync.Mutex
}

var sshPool = struct {
	sync.RWMutex
	entries map[string]*sshPoolEntry
}{entries: make(map[string]*sshPoolEntry)}

func getSSHClient(user, addr, password string) (*gossh.Client, error) {
	// P5: key 含密码哈希，避免换密码后复用旧连接
	key := user + "@" + addr + "#" + shortHash(password)

	// Try existing connection
	sshPool.RLock()
	if entry, ok := sshPool.entries[key]; ok {
		sshPool.RUnlock()
		entry.mu.Lock()
		if entry.client.Conn != nil {
			entry.lastUsed = time.Now()
			client := entry.client
			entry.mu.Unlock()
			return client, nil
		}
		entry.mu.Unlock()
		// Connection dead, remove and reconnect
		sshPool.Lock()
		delete(sshPool.entries, key)
		sshPool.Unlock()
	} else {
		sshPool.RUnlock()
	}

	// Create new connection — P5: TCP keepalive 探测半开连接
	config := &gossh.ClientConfig{
		User: user,
		Auth: []gossh.AuthMethod{
			gossh.Password(password),
		},
		HostKeyCallback: HostKeyCallback(),
		Timeout:         10 * time.Second,
	}
	dialer := &net.Dialer{Timeout: 10 * time.Second, KeepAlive: 30 * time.Second}
	rawConn, err := dialer.Dial("tcp", addr)
	if err != nil {
		return nil, err
	}
	ncc, chans, reqs, err := gossh.NewClientConn(rawConn, addr, config)
	if err != nil {
		rawConn.Close()
		return nil, err
	}
	client := gossh.NewClient(ncc, chans, reqs)

	sshPool.Lock()
	sshPool.entries[key] = &sshPoolEntry{
		client:   client,
		lastUsed: time.Now(),
	}
	sshPool.Unlock()
	return client, nil
}

// cleanupIdleSSH cleans up SSH connections idle for > 5 minutes
func cleanupIdleSSH() {
	for {
		time.Sleep(60 * time.Second)
		sshPool.Lock()
		for key, entry := range sshPool.entries {
			entry.mu.Lock()
			if time.Since(entry.lastUsed) > 5*time.Minute {
				entry.client.Close()
				delete(sshPool.entries, key)
				log.Printf("[SSH-POOL] closed idle connection: %s", key)
			}
			entry.mu.Unlock()
		}
		sshPool.Unlock()
	}
}

type SSHConnectMsg struct {
	Type     string `json:"type"`
	Host     string `json:"host"`
	Port     int    `json:"port"`
	Username string `json:"username"`
	AuthType string `json:"auth_type"`
	Password string `json:"password"`
	Cols     int    `json:"cols"`
	Rows     int    `json:"rows"`
	Mode     string `json:"mode"` // "local"=webterm 本地shell(免SSH服务)
}

// sshSession 保存 SSH 会话状态(webterm: sshConn/session 为 nil, pty/cmd 生效)
type sshSession struct {
	dc      *webrtc.DataChannel
	sshConn *gossh.Client
	session *gossh.Session
	stdin   interface{ Write([]byte) (int, error) }
	pty     *os.File  // webterm 本地 PTY(creack/pty)
	cmd     *exec.Cmd // webterm shell 进程
}

func (h *SignalHandler) bridgeSSHToDataChannel(dc *webrtc.DataChannel, roomID string) {
	var sess *sshSession
	var vnc *vncSession
	// sess/vnc 由 OnMessage/OnClose 并发访问，需互斥
	var stateMu sync.Mutex
	// P2 #18: 状态在 attach 时建立（本函数无早退路径，函数尾 OnClose 注册 Delete）
	ensureDCWriteState(dc)

	if dc.ReadyState() == webrtc.DataChannelStateOpen {
		log.Printf("DataChannel %s 已打开 (API模式)", dc.Label())
	}

	dc.OnMessage(func(msg webrtc.DataChannelMessage) {
		data := msg.Data
		if len(data) < 2 {
			return
		}
		prefix := data[0]
		payload := data[1:]

		stateMu.Lock()
		defer stateMu.Unlock()

		// ── VNC 消息路由 ──
		if prefix == MsgVNCConnect || prefix == MsgVNCInput || prefix == MsgVNCDisconnect {
			// 断开: 浏览器关闭 VNC 时通知 agent
			if prefix == MsgVNCDisconnect {
				if vnc != nil {
					log.Printf("[A-BRIDGE] VNC disconnect requested (room=%s)", roomID)
					vnc.cancel()
					vnc = nil
				}
				return
			}
			// 新连接: 若已有活跃会话先关掉旧的
			if prefix == MsgVNCConnect {
				if vnc != nil {
					log.Printf("[A-BRIDGE] VNC reconnect: closing old session (room=%s)", roomID)
					vnc.cancel()
					vnc = nil
				}
				ctx, cancel := context.WithCancel(context.Background())
				vncInputCh := make(chan []byte, 64)
				vnc = &vncSession{cancel: cancel, inputCh: vncInputCh}
				go func() {
					h.bridgeVNC(ctx, dc, roomID, payload, vncInputCh)
					log.Printf("[A-BRIDGE] VNC bridge ended (room=%s)", roomID)
				}()
				return
			}
			// 输入: 转发到当前活跃桥接
			if prefix == MsgVNCInput && vnc != nil && vnc.inputCh != nil {
				// 数据面 per-frame 日志：级别判断前移到构造 hexStr 之前，
				// Info 级（默认）下零开销 —— 鼠标事件每秒可达上百条
				if logger.Enabled(logger.LevelDebug) {
					if len(data) <= 80 {
						logger.Debugf("[A-BRIDGE] VNC input %d bytes: %s (room=%s)", len(data), hexDump(data), roomID)
					} else {
						logger.Debugf("[A-BRIDGE] VNC input %d bytes (room=%s)", len(data), roomID)
					}
				}
				select {
				case vnc.inputCh <- data:
				default:
				}
				return
			}
			return
		}

		// ── SSH 消息路由 ──
		if sess == nil {
			// 首帧必须是 SSH 连接；失败后允许浏览器重发 0x01 恢复 (R6)
			if prefix != MsgSSHConnect && prefix != MsgAck {
				h.sendDCErrorMsg(dc, "首条消息必须是SSH连接指令 (0x01)")
				return
			}
			if prefix == MsgAck {
				h.handleTerminalAck(dc, payload)
				return
			}
			var connectMsg SSHConnectMsg
			if err := json.Unmarshal(payload, &connectMsg); err != nil {
				h.sendDCErrorMsg(dc, "SSH连接指令解析失败: "+err.Error())
				return
			}
			sess = h.connectSSH(dc, &connectMsg, roomID)
			// 失败时 sess 仍为 nil，下一条 0x01 可重试
			return
		}

		// 后续消息: 分发处理
		switch prefix {
		case MsgAck:
			h.handleTerminalAck(dc, payload)
		case MsgSSHConnect:
			// 已有会话时收到新连接指令 → 关闭旧会话后重建 (R6 幂等重连, SSH/webterm 通用)
			closeSSHSession(sess)
			var connectMsg SSHConnectMsg
			if err := json.Unmarshal(payload, &connectMsg); err != nil {
				h.sendDCErrorMsg(dc, "SSH连接指令解析失败: "+err.Error())
				return
			}
			sess = h.connectSSH(dc, &connectMsg, roomID)
		case MsgTerminal:
			if sess.stdin != nil {
				sess.stdin.Write(payload)
			}
		case MsgResize:
			// P9: window-change 必须发到带 PTY 的原会话，不能新开 Session
			var resize ResizeMsg
			if json.Unmarshal(payload, &resize) != nil {
				break
			}
			if sess.session != nil {
				if err := sess.session.WindowChange(resize.Rows, resize.Cols); err != nil {
					log.Printf("WindowChange failed room=%s: %v", roomID, err)
				}
			} else if sess.pty != nil {
				// webterm: TIOCSWINSZ 直改本地 PTY
				if err := pty.Setsize(sess.pty, &pty.Winsize{Rows: uint16(resize.Rows), Cols: uint16(resize.Cols)}); err != nil {
					log.Printf("[WEBTERM] Setsize failed room=%s: %v", roomID, err)
				}
			}
		case MsgSFTPRequest:
			if len(payload) >= 1 {
				// 上传分片：同步入缓冲（与请求 goroutine 并发时分片可能晚于请求落盘）
				if payload[0] == sftpSubUpload {
					if reqID, idx, cnt, raw, ok := parseSftpUploadFrame(payload); ok {
						storeSFTPUpload(reqID, idx, cnt, raw)
					}
					break
				}
				if sess.sshConn != nil {
					sshConnRef := sess.sshConn
					go h.handleSFTPData(dc, payload, sshConnRef)
				}
			}
		default:
			// 未知前缀，当作终端数据
			if sess.stdin != nil {
				sess.stdin.Write(payload)
			}
		}
	})

	// R8: DC 关闭时回收 SSH 会话，避免目标机残留孤儿 shell
	dc.OnClose(func() {
		log.Printf("DataChannel closed, cleaning SSH session (room=%s)", roomID)
		dcWriteStates.Delete(dc)
		stateMu.Lock()
		defer stateMu.Unlock()
		if vnc != nil {
			vnc.cancel()
			vnc = nil
		}
		if sess != nil {
			closeSSHSession(sess)
			sess = nil
		}
	})
}

// connectSSH 建立 SSH 连接并启动桥接
func (h *SignalHandler) connectSSH(dc *webrtc.DataChannel, connectMsg *SSHConnectMsg, roomID string) *sshSession {
	// webterm: mode=local → Agent 本地 shell, 不拨号 SSH(内嵌SSH服务已移除)
	if connectMsg.Mode == "local" {
		return h.connectLocal(dc, connectMsg, roomID)
	}
	sSHStart := time.Now()
	addr := fmt.Sprintf("%s:%d", connectMsg.Host, connectMsg.Port)
	log.Printf("收到SSH连接指令: %s@%s (room=%s)", connectMsg.Username, addr, roomID)

	sshConn, err := getSSHClient(connectMsg.Username, addr, connectMsg.Password)
	tCPSSHMs := time.Since(sSHStart).Milliseconds()
	if err != nil {
		h.reportAgentError(dc, "agent_ssh", err.Error(), tCPSSHMs, 0, 0, connectMsg.Host, connectMsg.Port)
		h.sendDCErrorMsg(dc, "SSH连接失败: "+err.Error())
		return nil
	}

	session, err := sshConn.NewSession()
	if err != nil {
		h.sendDCErrorMsg(dc, "创建SSH会话失败: "+err.Error())
		return nil
	}

	modes := gossh.TerminalModes{
		gossh.ECHO:          1,
		gossh.TTY_OP_ISPEED: 14400,
		gossh.TTY_OP_OSPEED: 14400,
	}
	cols := connectMsg.Cols
	if cols <= 0 {
		cols = 120
	}
	rows := connectMsg.Rows
	if rows <= 0 {
		rows = 40
	}
	if err := session.RequestPty("xterm", rows, cols, modes); err != nil {
		tSessionMs := time.Since(sSHStart).Milliseconds()
		h.reportAgentError(dc, "agent_shell", err.Error(), tCPSSHMs, 0, tSessionMs-tCPSSHMs, connectMsg.Host, connectMsg.Port)
		h.sendDCErrorMsg(dc, "请求PTY失败: "+err.Error())
		session.Close()
		return nil
	}

	stdin, err := session.StdinPipe()
	if err != nil {
		h.sendDCErrorMsg(dc, "获取stdin失败: "+err.Error())
		session.Close()
		return nil
	}

	stdout, err := session.StdoutPipe()
	if err != nil {
		h.sendDCErrorMsg(dc, "获取stdout失败: "+err.Error())
		session.Close()
		return nil
	}

	stderr, err := session.StderrPipe()
	if err != nil {
		h.sendDCErrorMsg(dc, "获取stderr失败: "+err.Error())
		session.Close()
		return nil
	}

	if err := session.Shell(); err != nil {
		tShellMs := time.Since(sSHStart).Milliseconds()
		h.reportAgentError(dc, "agent_shell", err.Error(), tCPSSHMs, 0, tShellMs-tCPSSHMs, connectMsg.Host, connectMsg.Port)
		h.sendDCErrorMsg(dc, "启动shell失败: "+err.Error())
		session.Close()
		return nil
	}

	log.Printf("SSH会话建立成功，开始桥接: %s@%s", connectMsg.Username, addr)

	// 发送诊断消息(细粒度时间) — 准备好数据，但延迟到首帧终端数据之后再发送
	tShellDone := time.Since(sSHStart).Milliseconds()
	tSessionSetupMs := tShellDone - tCPSSHMs
	diagMsg, _ := json.Marshal(map[string]interface{}{
		"_type":            "diagnostics",
		"agent_connect_ms": tShellDone,
		"agent_tcp_ms":     tCPSSHMs,
		"agent_ssh_ms":     tSessionSetupMs,
		"agent_shell_ms":   0,
		"agent_ssh_host":   connectMsg.Host,
		"agent_ssh_port":   connectMsg.Port,
	})
	var diagSent sync.Once
	sendDiagOnce := func() {
		diagSent.Do(func() {
			if err := h.sendDCMsg(dc, MsgDiagnostics, diagMsg); err != nil {
				log.Printf("DC Send 0xFE failed, fallback to WS: room=%s err=%v", roomID, err)
				h.sendWSDiagnostics(roomID, diagMsg)
			}
		})
	}

	// SSH stdout → DataChannel (通过 dc.Send)
	go func() {
		buf := make([]byte, 32*1024)
		for {
			n, err := stdout.Read(buf)
			if n > 0 {
				h.sendDCTerminalData(dc, buf[:n])
				// 首帧终端数据发送后，再发送0xFE诊断，确保浏览器先收到终端数据
				sendDiagOnce()
			}
			if err != nil {
				break
			}
		}
		// stdout结束时兜底发送0xFE（如果没有任何终端数据）
		sendDiagOnce()
	}()

	// SSH stderr → DataChannel (通过 dc.Send)
	go func() {
		buf := make([]byte, 32*1024)
		for {
			n, err := stderr.Read(buf)
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

	// 等待 SSH 会话结束
	go func() {
		session.Wait()
		log.Printf("SSH会话结束: %s@%s", connectMsg.Username, addr)
		session.Close()
		// 会话结束时兜底发送0xFE（如果stdout/stderr都无数据）
		sendDiagOnce()
	}()

	log.Printf("SSH连接诊断: %s (room=%s, connect=%dms)", addr, roomID, tShellDone)

	return &sshSession{dc: dc, sshConn: sshConn, session: session, stdin: stdin}
}
