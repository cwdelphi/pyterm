package webrtc

// webterm 本地 PTY 单测: 启动/输入输出/resize/回收 (TC-W01/W02 核心路径)

import (
	"bufio"
	"os/exec"
	"strings"
	"testing"
	"time"

	"github.com/creack/pty"
)

// readLines 单读者goroutine, 行经 channel 交付(避免并发读同一 fd/bufio 抢缓冲)
func readLines(f interface{ Read([]byte) (int, error) }) <-chan string {
	ch := make(chan string, 64)
	go func() {
		sc := bufio.NewScanner(f)
		for sc.Scan() {
			ch <- sc.Text()
		}
		close(ch)
	}()
	return ch
}

func waitLine(t *testing.T, ch <-chan string, match func(string) bool, timeout time.Duration) string {
	t.Helper()
	deadline := time.After(timeout)
	for {
		select {
		case line, ok := <-ch:
			if !ok {
				t.Fatal("pty reader closed before match")
			}
			if match(line) {
				return line
			}
		case <-deadline:
			t.Fatal("timeout waiting for pty output")
		}
	}
}

func TestLocalPtyStartResizeAndIO(t *testing.T) {
	cmd := exec.Command("/bin/sh")
	f, err := pty.StartWithSize(cmd, &pty.Winsize{Rows: 40, Cols: 120})
	if err != nil {
		t.Fatalf("pty start: %v", err)
	}
	defer func() {
		f.Close()
		if cmd.Process != nil {
			cmd.Process.Kill()
		}
		cmd.Wait()
	}()
	lines := readLines(f)

	// IO: 写命令, 读回显 (TC-W01 核心)
	if _, err := f.Write([]byte("echo webterm_ok\n")); err != nil {
		t.Fatalf("write: %v", err)
	}
	waitLine(t, lines, func(l string) bool { return strings.Contains(l, "webterm_ok") }, 5*time.Second)

	// resize: TIOCSWINSZ 后 shell 内 stty size 反映新值 (TC-W02)
	if err := pty.Setsize(f, &pty.Winsize{Rows: 55, Cols: 132}); err != nil {
		t.Fatalf("setsize: %v", err)
	}
	if _, err := f.Write([]byte("stty size\n")); err != nil {
		t.Fatalf("write stty: %v", err)
	}
	got := waitLine(t, lines, func(l string) bool {
		return strings.Contains(l, "55 132") // 行首可能带 bracketed-paste/ANSI 序列
	}, 5*time.Second)
	if !strings.Contains(got, "55 132") {
		t.Fatalf("resize not applied, line=%q", got)
	}
}

func TestCloseSSHSessionIdempotent(t *testing.T) {
	// 空会话/nil: 不 panic, 可重复调用 (R6 重连与 OnClose 双路径)
	empty := &sshSession{}
	closeSSHSession(empty)
	closeSSHSession(empty)
	closeSSHSession(nil)

	// webterm 会话: pty+cmd 回收
	cmd := exec.Command("/bin/sh")
	f, err := pty.Start(cmd)
	if err != nil {
		t.Fatalf("pty start: %v", err)
	}
	sess := &sshSession{stdin: f, pty: f, cmd: cmd}
	closeSSHSession(sess)
	if sess.pty != nil {
		t.Fatal("pty not released")
	}
	if sess.stdin != nil {
		t.Fatal("stdin not released")
	}
	// 再次回收幂等
	closeSSHSession(sess)
	// 进程已被 Kill, Wait 收尸
	done := make(chan struct{})
	go func() { cmd.Wait(); close(done) }()
	select {
	case <-done:
	case <-time.After(3 * time.Second):
		t.Fatal("cmd.Wait timeout after kill")
	}
}
