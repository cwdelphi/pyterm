package socks5

// 阶段C 回归: SOCKS5 迁出独立包后与迁出前行为一致 (TC-S01~05 + Reconcile 幂等)

import (
	"encoding/binary"
	"encoding/json"
	"io"
	"net"
	"testing"
	"time"

	"github.com/ppy-tools/wragent/plugin"
)

// ── 测试辅助 ────────────────────────────────────────────────

type bridgeStub struct {
	mu    chan string // 捕获的目标地址(缓冲1)
	ok    bool        // onResult 参数
	calls int
}

func newBridgeStub(ok bool) *bridgeStub {
	return &bridgeStub{mu: make(chan string, 8), ok: ok}
}

func (b *bridgeStub) bridge(conn net.Conn, addr string, onResult func(ok bool)) {
	b.calls++
	b.mu <- addr
	if onResult != nil {
		onResult(b.ok)
	}
}

func freePort(t *testing.T) int {
	t.Helper()
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("freePort: %v", err)
	}
	port := ln.Addr().(*net.TCPAddr).Port
	ln.Close()
	return port
}

// newTestDeps 构造注入: Bridge 走桩, SendConnectTunnel 记录调用
func newTestDeps(b *bridgeStub) plugin.Deps {
	return plugin.Deps{
		Bridge: b.bridge,
		SendConnectTunnel: func(agentID string) error {
			return nil
		},
		Logf: func(string, ...any) {},
	}
}

func dial(t *testing.T, port int) net.Conn {
	t.Helper()
	conn, err := net.DialTimeout("tcp", "127.0.0.1:"+itoa(port), 3*time.Second)
	if err != nil {
		t.Fatalf("dial :%d: %v", port, err)
	}
	_ = conn.SetDeadline(time.Now().Add(5 * time.Second))
	return conn
}

func itoa(v int) string {
	buf := make([]byte, 0, 6)
	for {
		buf = append([]byte{byte('0' + v%10)}, buf...)
		v /= 10
		if v == 0 {
			return string(buf)
		}
	}
}

// negotiate 发起方法协商, 返回所选方法(0xFF=无合适方法)
func negotiate(t *testing.T, conn net.Conn, methods []byte) byte {
	t.Helper()
	req := append([]byte{0x05, byte(len(methods))}, methods...)
	if _, err := conn.Write(req); err != nil {
		t.Fatalf("write negotiate: %v", err)
	}
	resp := make([]byte, 2)
	if _, err := io.ReadFull(conn, resp); err != nil {
		t.Fatalf("read negotiate: %v", err)
	}
	if resp[0] != 0x05 {
		t.Fatalf("bad version in negotiate resp: %v", resp)
	}
	return resp[1]
}

// authUserPass RFC1929 认证, 返回 status(0=成功)
func authUserPass(t *testing.T, conn net.Conn, user, pass string) byte {
	t.Helper()
	req := []byte{0x01, byte(len(user))}
	req = append(req, user...)
	req = append(req, byte(len(pass)))
	req = append(req, pass...)
	if _, err := conn.Write(req); err != nil {
		t.Fatalf("write auth: %v", err)
	}
	resp := make([]byte, 2)
	if _, err := io.ReadFull(conn, resp); err != nil {
		t.Fatalf("read auth: %v", err)
	}
	return resp[1]
}

// connectIPv4 发起 CONNECT 至 IPv4 目标, 返回 REP
func connectIPv4(t *testing.T, conn net.Conn, ip [4]byte, port int) byte {
	t.Helper()
	req := []byte{0x05, 0x01, 0x00, 0x01, ip[0], ip[1], ip[2], ip[3]}
	var pb [2]byte
	binary.BigEndian.PutUint16(pb[:], uint16(port))
	req = append(req, pb[0], pb[1])
	return doConnect(t, conn, req)
}

// connectDomain 发起 CONNECT 至域名目标, 返回 REP
func connectDomain(t *testing.T, conn net.Conn, host string, port int) byte {
	t.Helper()
	req := []byte{0x05, 0x01, 0x00, 0x03, byte(len(host))}
	req = append(req, host...)
	var pb [2]byte
	binary.BigEndian.PutUint16(pb[:], uint16(port))
	req = append(req, pb[0], pb[1])
	return doConnect(t, conn, req)
}

func doConnect(t *testing.T, conn net.Conn, req []byte) byte {
	t.Helper()
	if _, err := conn.Write(req); err != nil {
		t.Fatalf("write connect: %v", err)
	}
	resp := make([]byte, 10)
	if _, err := io.ReadFull(conn, resp); err != nil {
		t.Fatalf("read connect reply: %v", err)
	}
	if resp[0] != 0x05 {
		t.Fatalf("bad version in reply: %v", resp)
	}
	return resp[1]
}

// runReconcile 执行一次配置下发
func runReconcile(t *testing.T, p *Plugin, tunnels []tunnelDef) error {
	t.Helper()
	doc, _ := json.Marshal(map[string]interface{}{"tunnels": tunnels})
	return p.Reconcile(doc)
}

// ── TC-S01~05 ───────────────────────────────────────────────

func TestTC_S01_WrongPasswordRejected(t *testing.T) {
	b := newBridgeStub(true)
	p := New(newTestDeps(b))
	port := freePort(t)
	if err := runReconcile(t, p, []tunnelDef{{ID: "s1", Protocol: "socks5", LocalPort: port,
		TargetAgentID: "agt", SocksUsername: "suser", SocksPassword: "spass", Enabled: boolPtr(true)}}); err != nil {
		t.Fatalf("reconcile: %v", err)
	}
	defer p.Stop()

	// 只提供 no-auth 方法 → 拒绝(0xFF)
	conn := dial(t, port)
	if m := negotiate(t, conn, []byte{0x00}); m != 0xFF {
		t.Fatalf("want 0xFF when auth required, got %02x", m)
	}
	conn.Close()

	// 错误口令 → 认证失败 status=0x01, 连接关闭
	conn = dial(t, port)
	if m := negotiate(t, conn, []byte{0x02}); m != 0x02 {
		t.Fatalf("want 0x02, got %02x", m)
	}
	if st := authUserPass(t, conn, "suser", "wrong"); st != 0x01 {
		t.Fatalf("want auth status 0x01, got %02x", st)
	}
	if _, err := io.ReadFull(conn, make([]byte, 1)); err == nil {
		t.Fatal("connection should be closed after auth reject")
	}
	conn.Close()

	// 正确口令 → 认证成功, CONNECT 成功
	conn = dial(t, port)
	if m := negotiate(t, conn, []byte{0x02}); m != 0x02 {
		t.Fatalf("want 0x02, got %02x", m)
	}
	if st := authUserPass(t, conn, "suser", "spass"); st != 0x00 {
		t.Fatalf("want auth status 0x00, got %02x", st)
	}
	if rep := connectIPv4(t, conn, [4]byte{1, 2, 3, 4}, 80); rep != 0x00 {
		t.Fatalf("want REP 0x00, got %02x", rep)
	}
	conn.Close()
}

func TestTC_S02_ConnectIPv4NoAuth(t *testing.T) {
	b := newBridgeStub(true)
	p := New(newTestDeps(b))
	port := freePort(t)
	if err := runReconcile(t, p, []tunnelDef{{ID: "s2", Protocol: "socks5", LocalPort: port,
		TargetAgentID: "agt", Enabled: boolPtr(true)}}); err != nil {
		t.Fatalf("reconcile: %v", err)
	}
	defer p.Stop()

	conn := dial(t, port)
	if m := negotiate(t, conn, []byte{0x00}); m != 0x00 {
		t.Fatalf("want 0x00, got %02x", m)
	}
	if rep := connectIPv4(t, conn, [4]byte{10, 0, 0, 1}, 8080); rep != 0x00 {
		t.Fatalf("want REP 0x00, got %02x", rep)
	}
	conn.Close()

	select {
	case addr := <-b.mu:
		if addr != "10.0.0.1:8080" {
			t.Fatalf("bridge addr = %q, want 10.0.0.1:8080", addr)
		}
	default:
		t.Fatal("bridge not called")
	}
}

func TestTC_S03_BridgeFailureRep0x05(t *testing.T) {
	b := newBridgeStub(false) // 模拟目标 Agent 拨号失败/无隧道
	p := New(newTestDeps(b))
	port := freePort(t)
	if err := runReconcile(t, p, []tunnelDef{{ID: "s3", Protocol: "socks5", LocalPort: port,
		TargetAgentID: "agt", Enabled: boolPtr(true)}}); err != nil {
		t.Fatalf("reconcile: %v", err)
	}
	defer p.Stop()

	conn := dial(t, port)
	if m := negotiate(t, conn, []byte{0x00}); m != 0x00 {
		t.Fatalf("want 0x00, got %02x", m)
	}
	if rep := connectIPv4(t, conn, [4]byte{8, 8, 8, 8}, 53); rep != 0x05 {
		t.Fatalf("want REP 0x05, got %02x", rep)
	}
	conn.Close()
}

func TestTC_S04_ConnectDomain(t *testing.T) {
	b := newBridgeStub(true)
	p := New(newTestDeps(b))
	port := freePort(t)
	if err := runReconcile(t, p, []tunnelDef{{ID: "s4", Protocol: "socks5", LocalPort: port,
		TargetAgentID: "agt", Enabled: boolPtr(true)}}); err != nil {
		t.Fatalf("reconcile: %v", err)
	}
	defer p.Stop()

	conn := dial(t, port)
	if m := negotiate(t, conn, []byte{0x00}); m != 0x00 {
		t.Fatalf("want 0x00, got %02x", m)
	}
	if rep := connectDomain(t, conn, "example.com", 443); rep != 0x00 {
		t.Fatalf("want REP 0x00, got %02x", rep)
	}
	conn.Close()

	select {
	case addr := <-b.mu:
		if addr != "example.com:443" {
			t.Fatalf("bridge addr = %q, want example.com:443", addr)
		}
	default:
		t.Fatal("bridge not called")
	}
}

func TestTC_S05_TwoTunnelsSameTarget(t *testing.T) {
	b := newBridgeStub(true)
	p := New(newTestDeps(b))
	p1, p2 := freePort(t), freePort(t)
	if err := runReconcile(t, p, []tunnelDef{
		{ID: "a", Protocol: "socks5", LocalPort: p1, TargetAgentID: "agt", Enabled: boolPtr(true)},
		{ID: "b", Protocol: "socks5", LocalPort: p2, TargetAgentID: "agt", Enabled: boolPtr(true)},
	}); err != nil {
		t.Fatalf("reconcile: %v", err)
	}
	defer p.Stop()

	st := p.Status()
	if !st.Running {
		t.Fatal("want running")
	}
	// 两个监听均可 CONNECT; connect_tunnel 由服务端按目标去重(此处断言插件已发起)
	for _, port := range []int{p1, p2} {
		conn := dial(t, port)
		if m := negotiate(t, conn, []byte{0x00}); m != 0x00 {
			t.Fatalf("port %d: want 0x00, got %02x", port, m)
		}
		if rep := connectIPv4(t, conn, [4]byte{1, 1, 1, 1}, 80); rep != 0x00 {
			t.Fatalf("port %d: want REP 0x00, got %02x", port, rep)
		}
		conn.Close()
	}
	if b.calls != 2 {
		t.Fatalf("bridge calls = %d, want 2", b.calls)
	}
}

// ── Reconcile 行为 ─────────────────────────────────────────

func TestReconcile_IdempotentNoRestart(t *testing.T) {
	b := newBridgeStub(true)
	p := New(newTestDeps(b))
	port := freePort(t)
	tunnels := []tunnelDef{{ID: "x", Protocol: "socks5", LocalPort: port, TargetAgentID: "agt", Enabled: boolPtr(true)}}
	if err := runReconcile(t, p, tunnels); err != nil {
		t.Fatalf("reconcile#1: %v", err)
	}
	p.mu.Lock()
	first := p.instances["x"]
	p.mu.Unlock()

	if err := runReconcile(t, p, tunnels); err != nil {
		t.Fatalf("reconcile#2: %v", err)
	}
	p.mu.Lock()
	second := p.instances["x"]
	p.mu.Unlock()
	if first != second {
		t.Fatal("identical config must not restart listener")
	}
	p.Stop()
}

func TestReconcile_PortChangeRestarts(t *testing.T) {
	b := newBridgeStub(true)
	p := New(newTestDeps(b))
	oldPort, newPort := freePort(t), freePort(t)
	if err := runReconcile(t, p, []tunnelDef{{ID: "x", Protocol: "socks5", LocalPort: oldPort, Enabled: boolPtr(true)}}); err != nil {
		t.Fatalf("reconcile#1: %v", err)
	}
	if err := runReconcile(t, p, []tunnelDef{{ID: "x", Protocol: "socks5", LocalPort: newPort, Enabled: boolPtr(true)}}); err != nil {
		t.Fatalf("reconcile#2: %v", err)
	}
	defer p.Stop()

	// 旧端口已关
	if c, err := net.DialTimeout("tcp", "127.0.0.1:"+itoa(oldPort), time.Second); err == nil {
		c.Close()
		t.Fatal("old port should be closed after change")
	}
	// 新端口可用
	conn := dial(t, newPort)
	if m := negotiate(t, conn, []byte{0x00}); m != 0x00 {
		t.Fatalf("new port: want 0x00, got %02x", m)
	}
	conn.Close()
}

func TestReconcile_NilDisables(t *testing.T) {
	b := newBridgeStub(true)
	p := New(newTestDeps(b))
	port := freePort(t)
	if err := runReconcile(t, p, []tunnelDef{{ID: "x", Protocol: "socks5", LocalPort: port, Enabled: boolPtr(true)}}); err != nil {
		t.Fatalf("reconcile: %v", err)
	}
	if err := p.Reconcile(nil); err != nil {
		t.Fatalf("reconcile nil: %v", err)
	}
	if p.Status().Running {
		t.Fatal("want stopped after nil config")
	}
	if c, err := net.DialTimeout("tcp", "127.0.0.1:"+itoa(port), time.Second); err == nil {
		c.Close()
		t.Fatal("port should be released after nil config")
	}
}

func TestReconcile_InvalidPortReturnsError(t *testing.T) {
	p := New(newTestDeps(newBridgeStub(true)))
	if err := runReconcile(t, p, []tunnelDef{{ID: "bad", Protocol: "socks5", LocalPort: 0, Enabled: boolPtr(true)}}); err == nil {
		t.Fatal("want error for port 0")
	}
	p.Stop()
}

func TestUnsupportedCmdRep0x07(t *testing.T) {
	b := newBridgeStub(true)
	p := New(newTestDeps(b))
	port := freePort(t)
	if err := runReconcile(t, p, []tunnelDef{{ID: "x", Protocol: "socks5", LocalPort: port, Enabled: boolPtr(true)}}); err != nil {
		t.Fatalf("reconcile: %v", err)
	}
	defer p.Stop()

	conn := dial(t, port)
	if m := negotiate(t, conn, []byte{0x00}); m != 0x00 {
		t.Fatalf("want 0x00, got %02x", m)
	}
	// cmd=0x02( BIND) → 0x07
	req := []byte{0x05, 0x02, 0x00, 0x01, 1, 2, 3, 4, 0, 80}
	if _, err := conn.Write(req); err != nil {
		t.Fatalf("write: %v", err)
	}
	resp := make([]byte, 10)
	if _, err := io.ReadFull(conn, resp); err != nil {
		t.Fatalf("read reply: %v", err)
	}
	if resp[1] != 0x07 {
		t.Fatalf("want REP 0x07, got %02x", resp[1])
	}
	conn.Close()
}

// ── 内部辅助 ────────────────────────────────────────────────

func boolPtr(v bool) *bool { return &v }
