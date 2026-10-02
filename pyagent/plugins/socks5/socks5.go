// Package socks5 SOCKS5 代理插件 —— 自 webrtc/socks5.go 与 tunnel.go "socks5" 分支
// 迁出独立包化(阶段C), 协议处理逻辑零改动: 方法协商/RFC1929 口令认证/CONNECT 解析,
// 动态目标经 Deps.Bridge 交目标 Agent 拨号。
package socks5

import (
	"crypto/subtle"
	"encoding/binary"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"sort"
	"strconv"
	"strings"
	"sync"

	"github.com/ppy-tools/pyagent/plugin"
)

const (
	socks5Version      = 0x05
	socks5AuthNone     = 0x00
	socks5AuthUserPass = 0x02
	socks5NoAcceptable = 0xFF

	socks5CmdConnect = 0x01

	socks5AtypIPv4   = 0x01
	socks5AtypDomain = 0x03
	socks5AtypIPv6   = 0x04

	socks5RepSuccess         = 0x00
	socks5RepFailure         = 0x05
	socks5RepCmdUnsupported  = 0x07
	socks5RepAtypUnsupported = 0x08
)

// tunnelDef 单条 SOCKS5 隧道(与服务端 tunnel 对象字段对齐)
type tunnelDef struct {
	ID            string `json:"id"`
	Name          string `json:"name"`
	Protocol      string `json:"protocol"`
	LocalPort     int    `json:"local_port"`
	TargetAddr    string `json:"target_addr"`
	TargetAgentID string `json:"target_agent_id"`
	SocksUsername string `json:"socks_username"`
	SocksPassword string `json:"socks_password"`
	// Enabled 缺省视为 true(面板可能不下发该字段); 显式 false 才跳过
	Enabled *bool `json:"enabled"`
}

func (t tunnelDef) enabled() bool { return t.Enabled == nil || *t.Enabled }

// configDoc 插件配置文档: plugins.socks5 = {"tunnels":[...]}
type configDoc struct {
	Tunnels []tunnelDef `json:"tunnels"`
}

// instance 运行中的监听实例
type instance struct {
	cfg      tunnelDef
	listener net.Listener
	cancel   chan struct{}
	once     sync.Once
}

func (inst *instance) stop() {
	inst.once.Do(func() {
		close(inst.cancel)
		if inst.listener != nil {
			inst.listener.Close()
		}
	})
}

// Plugin 实现 plugin.Plugin(Name="socks5")
type Plugin struct {
	deps      plugin.Deps
	mu        sync.Mutex
	instances map[string]*instance
	lastKey   string // 最近成功应用的配置指纹(幂等快速跳过)
}

// New 创建 socks5 插件(deps.Bridge 为 nil 时 CONNECT 一律回 0x05)
func New(deps plugin.Deps) *Plugin {
	if deps.Logf == nil {
		deps.Logf = func(format string, args ...any) {
			fmt.Printf(format+"\n", args...)
		}
	}
	return &Plugin{deps: deps, instances: make(map[string]*instance)}
}

// Name 插件键 = 服务端 config.plugins 字典键
func (p *Plugin) Name() string { return "socks5" }

// Reconcile 幂等应用配置: nil/空 → 全部停止; 相同配置快速跳过;
// 端口/口令/目标变更 → 仅重启受影响监听; 其余隧道与核心(终端/webterm)不受影响。
func (p *Plugin) Reconcile(raw json.RawMessage) error {
	p.mu.Lock()
	defer p.mu.Unlock()

	// 禁用路径
	if len(raw) == 0 || string(raw) == "null" {
		if p.lastKey == "" && len(p.instances) == 0 {
			return nil
		}
		p.stopAllLocked()
		p.lastKey = ""
		p.deps.Logf("[SOCKS5] 配置缺失, 全部监听已停止")
		return nil
	}

	var doc configDoc
	if err := json.Unmarshal(raw, &doc); err != nil {
		return fmt.Errorf("socks5配置解析失败: %w", err)
	}

	desired := make(map[string]tunnelDef, len(doc.Tunnels))
	var ids []string
	for _, t := range doc.Tunnels {
		if t.Protocol != "" && t.Protocol != "socks5" {
			continue
		}
		if !t.enabled() {
			continue
		}
		if t.ID == "" {
			return fmt.Errorf("socks5隧道缺 id (port=%d)", t.LocalPort)
		}
		if t.LocalPort <= 0 || t.LocalPort > 65535 {
			return fmt.Errorf("socks5隧道 %s 端口非法: %d", t.ID, t.LocalPort)
		}
		desired[t.ID] = t
		ids = append(ids, t.ID)
	}
	sort.Strings(ids)

	// 幂等快速跳过: 指纹一致且实例数吻合
	key := digest(desired, ids)
	if key == p.lastKey && len(p.instances) == len(desired) {
		return nil
	}

	// 停止已删除/禁用/变更的实例
	for id, inst := range p.instances {
		want, ok := desired[id]
		if !ok || changed(inst.cfg, want) {
			inst.stop()
			delete(p.instances, id)
			p.deps.Logf("[SOCKS5] 监听停止: :%d (%s)", inst.cfg.LocalPort, id)
		}
	}

	// 启动新增
	var errs []string
	for _, id := range ids {
		if _, running := p.instances[id]; running {
			continue
		}
		t := desired[id]
		if err := p.startLocked(t); err != nil {
			errs = append(errs, err.Error())
			continue
		}
	}
	if len(errs) > 0 {
		// 保留指纹为空以便下次 Reconcile 重试失败项
		p.lastKey = ""
		return fmt.Errorf("socks5启动失败: %s", strings.Join(errs, "; "))
	}
	p.lastKey = key
	return nil
}

func (p *Plugin) startLocked(t tunnelDef) error {
	addr := fmt.Sprintf(":%d", t.LocalPort)
	ln, err := net.Listen("tcp", addr)
	if err != nil {
		return fmt.Errorf("listen %s 失败: %w", addr, err)
	}
	inst := &instance{cfg: t, listener: ln, cancel: make(chan struct{})}
	p.instances[t.ID] = inst
	p.deps.Logf("[SOCKS5] listener started: %s (auth=%v, dial_agent=%s, name=%s)",
		addr, t.SocksUsername != "" || t.SocksPassword != "",
		t.TargetAgentID, t.Name)
	go p.serve(inst)

	// 确保到目标 Agent 的隧道通道(幂等, 服务端按目标去重)
	if t.TargetAgentID != "" && p.deps.SendConnectTunnel != nil {
		if err := p.deps.SendConnectTunnel(t.TargetAgentID); err != nil {
			p.deps.Logf("[SOCKS5] connect_tunnel to %s failed: %v", t.TargetAgentID, err)
		}
	}
	return nil
}

// serve Accept 循环(自 startSOCKS5Listener 迁出, cancel 语义不变)
func (p *Plugin) serve(inst *instance) {
	for {
		select {
		case <-inst.cancel:
			return
		default:
			conn, err := inst.listener.Accept()
			if err != nil {
				select {
				case <-inst.cancel:
					return
				default:
					p.deps.Logf("[SOCKS5] accept failed: %v", err)
					continue
				}
			}
			go p.handleClient(conn, inst.cfg)
		}
	}
}

// handleClient 单连接处理: 方法协商 → RFC1929 口令认证 → CONNECT → 隧道桥接
// (自 webrtc.handleSOCKS5 迁出, bridgeTCPConn 调用改为 Deps.Bridge 回调)
func (p *Plugin) handleClient(conn net.Conn, cfg tunnelDef) {
	defer conn.Close()

	// 方法协商
	head := make([]byte, 2)
	if _, err := io.ReadFull(conn, head); err != nil {
		return
	}
	if head[0] != socks5Version {
		return
	}
	methods := make([]byte, int(head[1]))
	if len(methods) == 0 {
		return
	}
	if _, err := io.ReadFull(conn, methods); err != nil {
		return
	}

	needAuth := cfg.SocksUsername != "" || cfg.SocksPassword != ""
	want := byte(socks5AuthNone)
	if needAuth {
		want = socks5AuthUserPass
	}
	if !socks5HasMethod(methods, want) {
		conn.Write([]byte{socks5Version, socks5NoAcceptable})
		p.deps.Logf("[SOCKS5] no acceptable auth method (needAuth=%v, remote=%s)", needAuth, conn.RemoteAddr())
		return
	}
	if _, err := conn.Write([]byte{socks5Version, want}); err != nil {
		return
	}

	// 用户名口令认证（RFC1929）
	if needAuth && !socks5PasswordAuth(conn, cfg) {
		p.deps.Logf("[SOCKS5] auth rejected (remote=%s)", conn.RemoteAddr())
		return
	}

	// 请求
	req := make([]byte, 4)
	if _, err := io.ReadFull(conn, req); err != nil {
		return
	}
	if req[0] != socks5Version {
		return
	}
	if req[1] != socks5CmdConnect {
		writeSocks5Reply(conn, socks5RepCmdUnsupported)
		p.deps.Logf("[SOCKS5] unsupported cmd=%d (remote=%s)", req[1], conn.RemoteAddr())
		return
	}

	var host string
	switch req[3] {
	case socks5AtypIPv4:
		buf := make([]byte, 4)
		if _, err := io.ReadFull(conn, buf); err != nil {
			return
		}
		host = net.IP(buf).String()
	case socks5AtypIPv6:
		buf := make([]byte, 16)
		if _, err := io.ReadFull(conn, buf); err != nil {
			return
		}
		host = net.IP(buf).String()
	case socks5AtypDomain:
		l := make([]byte, 1)
		if _, err := io.ReadFull(conn, l); err != nil {
			return
		}
		if l[0] == 0 {
			writeSocks5Reply(conn, socks5RepFailure)
			return
		}
		dom := make([]byte, int(l[0]))
		if _, err := io.ReadFull(conn, dom); err != nil {
			return
		}
		host = string(dom)
	default:
		writeSocks5Reply(conn, socks5RepAtypUnsupported)
		return
	}
	portBuf := make([]byte, 2)
	if _, err := io.ReadFull(conn, portBuf); err != nil {
		return
	}
	port := binary.BigEndian.Uint16(portBuf)
	target := net.JoinHostPort(host, strconv.Itoa(int(port)))

	// 经隧道拨号：成功/失败由 Bridge 回调后回 SOCKS5 应答，之后进入双向字节流
	if p.deps.Bridge == nil {
		writeSocks5Reply(conn, socks5RepFailure)
		return
	}
	p.deps.Bridge(conn, target, func(ok bool) {
		if ok {
			writeSocks5Reply(conn, socks5RepSuccess)
			p.deps.Logf("[SOCKS5] CONNECT ok → %s (remote=%s)", target, conn.RemoteAddr())
		} else {
			writeSocks5Reply(conn, socks5RepFailure)
			p.deps.Logf("[SOCKS5] CONNECT failed → %s (remote=%s)", target, conn.RemoteAddr())
		}
	})
}

// Stop 停止全部监听(进程退出/Manager.StopAll 调用)
func (p *Plugin) Stop() error {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.stopAllLocked()
	p.lastKey = ""
	return nil
}

func (p *Plugin) stopAllLocked() {
	for id, inst := range p.instances {
		inst.stop()
		delete(p.instances, id)
	}
}

// Status 只读运行状态
func (p *Plugin) Status() plugin.Status {
	p.mu.Lock()
	defer p.mu.Unlock()
	if len(p.instances) == 0 {
		return plugin.Status{Running: false}
	}
	ids := make([]string, 0, len(p.instances))
	for id := range p.instances {
		ids = append(ids, id)
	}
	sort.Strings(ids)
	var ports []string
	for _, id := range ids {
		ports = append(ports, fmt.Sprintf(":%d", p.instances[id].cfg.LocalPort))
	}
	return plugin.Status{
		Running: true,
		Detail:  fmt.Sprintf("listeners %s", strings.Join(ports, ",")),
	}
}

// ── 协议辅助(逐字迁出) ──────────────────────────────────────

// socks5PasswordAuth RFC1929用户名口令认证
func socks5PasswordAuth(conn net.Conn, cfg tunnelDef) bool {
	head := make([]byte, 2)
	if _, err := io.ReadFull(conn, head); err != nil {
		return false
	}
	if head[0] != 0x01 {
		return false
	}
	user := make([]byte, int(head[1]))
	if _, err := io.ReadFull(conn, user); err != nil {
		return false
	}
	pl := make([]byte, 1)
	if _, err := io.ReadFull(conn, pl); err != nil {
		return false
	}
	pass := make([]byte, int(pl[0]))
	if _, err := io.ReadFull(conn, pass); err != nil {
		return false
	}
	okUser := subtle.ConstantTimeCompare(user, []byte(cfg.SocksUsername)) == 1
	okPass := subtle.ConstantTimeCompare(pass, []byte(cfg.SocksPassword)) == 1
	status := byte(0x01)
	if okUser && okPass {
		status = 0x00
	}
	conn.Write([]byte{0x01, status})
	return status == 0x00
}

// writeSocks5Reply 回SOCKS5应答（BND地址固定填0.0.0.0:0）
func writeSocks5Reply(conn net.Conn, rep byte) {
	resp := []byte{socks5Version, rep, 0x00, socks5AtypIPv4, 0, 0, 0, 0, 0, 0}
	conn.Write(resp)
}

func socks5HasMethod(methods []byte, want byte) bool {
	for _, m := range methods {
		if m == want {
			return true
		}
	}
	return false
}

// digest 期望配置指纹(按 id 排序), 用于幂等快速跳过; 不含 Name(改名不重启, 同迁出前)
func digest(desired map[string]tunnelDef, sortedIDs []string) string {
	var b strings.Builder
	for _, id := range sortedIDs {
		t := desired[id]
		fmt.Fprintf(&b, "%s|%d|%s|%s|%s|%s\n",
			t.ID, t.LocalPort, t.TargetAgentID, t.TargetAddr,
			t.SocksUsername, t.SocksPassword)
	}
	return b.String()
}

// changed 配置是否需要重启监听(与 tunnel.go ConfigChanged 同字段集, Name 不参与)
func changed(old, newCfg tunnelDef) bool {
	return old.LocalPort != newCfg.LocalPort ||
		old.TargetAddr != newCfg.TargetAddr ||
		old.TargetAgentID != newCfg.TargetAgentID ||
		old.SocksUsername != newCfg.SocksUsername ||
		old.SocksPassword != newCfg.SocksPassword
}
