// Package netinfo 负责扫描本机网络接口、自动评分并生成 ICE 过滤规则。
// 方案：docs/传输优化_ICE优化与接口过滤方案_v2.0.md §4.3
package netinfo

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"net"
	"os"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/ppy-tools/pyagent/icefilter"
	"github.com/ppy-tools/pyagent/pathcache"
)

// Options 扫描与评分参数
type Options struct {
	AllowTailscale bool     `json:"allow_tailscale"`
	MaxKeep        int      `json:"max_keep"`  // 最多保留几个接口，默认 3
	MinScore       int      `json:"min_score"` // 入选分数阈值，默认 60
	History        []string `json:"history"`   // 历史胜出接口
}

func (o Options) withDefaults() Options {
	if o.MaxKeep <= 0 {
		o.MaxKeep = 3
	}
	if o.MinScore <= 0 {
		o.MinScore = 60
	}
	return o
}

// Iface 单个接口的扫描与评分结果（直接对应后台接口清单表格）
type Iface struct {
	Name        string   `json:"name"`
	State       string   `json:"state"`
	MTU         int      `json:"mtu"`
	Addrs       []string `json:"addrs"`
	IsDefault   bool     `json:"is_default"`
	IsLoopback  bool     `json:"is_loopback"`
	IsVirtual   bool     `json:"is_virtual"`
	HasGlobalV4 bool     `json:"has_global_v4"`
	HasGlobalV6 bool     `json:"has_global_v6"`
	AddrCount   int      `json:"addr_count"` // 非回环单播地址数 = pion 该接口产出的本端候选数
	Score       int      `json:"score"`
	Keep        bool     `json:"keep"`
	HardDrop    bool     `json:"hard_drop"`
	Reason      string   `json:"reason"`
}

// Report 一次扫描的完整结果（上报到 config_json.net_info）
type Report struct {
	Interfaces  []Iface        `json:"interfaces"`
	IfHash      string         `json:"if_hash"`
	ScannedAt   string         `json:"scanned_at"`
	Rule        icefilter.Rule `json:"rule"`
	Valid       bool           `json:"valid"`
	Reason      string         `json:"reason"`
	HostBefore  int            `json:"host_before"` // 有可枚举地址的接口数（过滤前）
	HostAfter   int            `json:"host_after"`  // 有可枚举地址的接口数（过滤后）
	AddrBefore  int            `json:"addr_before"` // 非回环单播地址数 = 本端 host 候选数（过滤前）
	AddrAfter   int            `json:"addr_after"`  // 本端 host 候选数（过滤后）
	EstCandBef  int            `json:"est_candidates_before"`
	EstCandAft  int            `json:"est_candidates_after"`
	EstPairsBef int            `json:"est_pairs_before"`
	EstPairsAft int            `json:"est_pairs_after"`

	// P2 §4.5: 看板数据（随 network_info 一起落 config_json.net_info）
	LastPair  *LastPair         `json:"last_pair,omitempty"`
	Ladder    *Ladder           `json:"ladder,omitempty"`
	PathCache []pathcache.Entry `json:"path_cache,omitempty"`
}

// LastPair 最近一次 ICE 建连成功时胜出的本端接口
type LastPair struct {
	Iface    string `json:"local_iface"`
	ConnType string `json:"conn_type,omitempty"`
	IfHash   string `json:"if_hash,omitempty"`
	Level    int    `json:"level,omitempty"` // 建连时的等级 1/2/3
	At       int64  `json:"at,omitempty"`
}

// Ladder 三级回退阶梯自统计（Agent 从每次 offer 的 ice_level 观察得出）
type Ladder struct {
	Level     int   `json:"level"`      // 最近一次 offer 的等级
	Fallbacks int   `json:"fallbacks"`  // 回退触发次数（等级升高）
	L3        int   `json:"l3"`         // L3 兜底次数
	CacheHits int   `json:"cache_hits"` // L1 快路径次数
	At        int64 `json:"at,omitempty"`
}

// 非接口因素的候选估计：生产配置带 STUN+TURN 时实测单侧约 3 srflx + 2 relay（v1.0 §3）
const estServerCandidates = 5

// rawIface 扫描输入（可注入，便于单测）
type rawIface struct {
	Name      string
	MTU       int
	Up        bool
	Loopback  bool
	Addrs     []net.Addr
	IsDefault bool
}

// Scan 扫描真实网络接口并生成规则
func Scan(opt Options) (*Report, error) {
	def, _ := DefaultIface()
	list, err := net.Interfaces()
	if err != nil {
		return nil, err
	}
	raw := make([]rawIface, 0, len(list))
	for _, ni := range list {
		addrs, _ := ni.Addrs()
		raw = append(raw, rawIface{
			Name:      ni.Name,
			MTU:       ni.MTU,
			Up:        ni.Flags&net.FlagUp != 0,
			Loopback:  ni.Flags&net.FlagLoopback != 0,
			Addrs:     addrs,
			IsDefault: ni.Name == def,
		})
	}
	rep := evaluate(raw, opt.withDefaults())
	attachStats(rep)
	return rep, nil
}

// ── P2: 看板数据（last_pair / ladder / path_cache）────────────────────
//
// 这些字段与接口扫描无关，但要随 network_info 一起落 config_json.net_info；
// 由 webrtc 侧在建连成功/等级变化时写入，再用 PushStats 立即补发一次
// （即使 if_hash 没变也发，否则 UI 要等 5min 周期重扫才看到新数据）。

var (
	statsMu       sync.Mutex
	statsLastPair *LastPair
	statsLadder   *Ladder
)

// SetLastPair 记录最近一次胜出接口（webrtc 建连成功时调用）
func SetLastPair(p *LastPair) {
	statsMu.Lock()
	statsLastPair = p
	statsMu.Unlock()
}

// GetLastPair 当前胜出接口（无则 nil）
func GetLastPair() *LastPair {
	statsMu.Lock()
	defer statsMu.Unlock()
	return statsLastPair
}

// SetLadder 写入阶梯自统计（读-改-写在 webrtc 侧完成）
func SetLadder(l *Ladder) {
	statsMu.Lock()
	statsLadder = l
	statsMu.Unlock()
}

// GetLadder 当前阶梯自统计的副本（无则 nil）
func GetLadder() *Ladder {
	statsMu.Lock()
	defer statsMu.Unlock()
	if statsLadder == nil {
		return nil
	}
	cp := *statsLadder
	return &cp
}

// ClearStats 清空看板数据（ice_cache_clear 时调用）
func ClearStats() {
	statsMu.Lock()
	statsLastPair = nil
	statsLadder = nil
	statsMu.Unlock()
}

// attachStats 把看板数据挂到报告上（只在新建报告/克隆上做，避免共享对象竞态）
func attachStats(r *Report) {
	if r == nil {
		return
	}
	statsMu.Lock()
	r.LastPair = statsLastPair
	r.Ladder = statsLadder
	statsMu.Unlock()
	r.PathCache = pathcache.Default().Entries()
}

// statsFingerprint 看板数据指纹；ReportIfChanged 用它判断「接口没变但看板变了」
func statsFingerprint(r *Report) string {
	if r == nil || (r.LastPair == nil && r.Ladder == nil && len(r.PathCache) == 0) {
		return ""
	}
	b, err := json.Marshal([3]interface{}{r.LastPair, r.Ladder, r.PathCache})
	if err != nil {
		return ""
	}
	return string(b)
}

// PushStats 看板数据变化后立即补发一次 network_info（if_hash 未变也会发）
func PushStats() {
	ReportIfChanged(LastReportWithStats(), false)
}

// LastReportWithStats 返回带看板数据的最近报告的克隆（无扫描报告时返回 nil）
func LastReportWithStats() *Report {
	reportMu.RLock()
	last := lastRep
	reportMu.RUnlock()
	if last == nil {
		return nil
	}
	clone := *last
	attachStats(&clone)
	return &clone
}

// Refresh 扫描并把规则应用到进程级 icefilter，返回报告
func Refresh(opt Options) (*Report, error) {
	rep, err := Scan(opt)
	if err != nil {
		return nil, err
	}
	icefilter.Configure(rep.Rule)
	emit(rep)
	return rep, nil
}

// ── 服务端参数覆盖与上报钩子（P1）──────────────────────────────
//
// 后台 config_json.ice.mode=auto 时由 netinfo 自扫驱动，但 allow_tailscale/
// max_keep 等参数由后台下发 → SetOptions 覆盖本次进程的扫描参数。
// Reporter 用于把 Report 推给服务端（config_json.net_info），
// 若 if_hash 未变化则跳过，避免周期重扫产生的无变化上报。

var (
	optMu    sync.RWMutex
	srvOpt   *Options
	reportMu sync.RWMutex
	reporter func(*Report)
	lastHash string
	// P2: 看板数据指纹（接口没变但 last_pair/ladder 变了也要发）
	lastStats string
	lastRep   *Report
)

// SetOptions 用服务端下发参数覆盖后续扫描参数（nil 表示取消覆盖）
func SetOptions(o *Options) {
	optMu.Lock()
	srvOpt = o
	optMu.Unlock()
}

// CurrentOptions 返回生效扫描参数：服务端覆盖优先，其次 base
func CurrentOptions(base Options) Options {
	optMu.RLock()
	o := srvOpt
	optMu.RUnlock()
	if o != nil {
		return o.withDefaults()
	}
	return base.withDefaults()
}

// SetReporter 注册 Report 上报回调（nil 解除）；线程安全
func SetReporter(fn func(*Report)) {
	reportMu.Lock()
	reporter = fn
	reportMu.Unlock()
}

// LastReport 最近一次扫描产生的报告（无则 nil）
func LastReport() *Report {
	reportMu.RLock()
	defer reportMu.RUnlock()
	return lastRep
}

// ReportIfChanged 上报报告；if_hash 与上次相同则跳过并返回 false。
// force=true 时忽略 hash 去重（注册成功与显式扫描请求）。
func ReportIfChanged(r *Report, force bool) bool {
	if r == nil {
		return false
	}
	reportMu.Lock()
	if lastRep == nil || r.Valid {
		lastRep = r
	}
	fn := reporter
	// P2: 看板数据(last_pair/ladder/path_cache)变化也要发，否则要等 5min 周期重扫
	stats := statsFingerprint(r)
	if !force && r.IfHash == lastHash && stats == lastStats {
		reportMu.Unlock()
		return false
	}
	if fn != nil {
		lastHash = r.IfHash
		lastStats = stats
	}
	reportMu.Unlock()
	if fn == nil {
		return false
	}
	fn(r)
	return true
}

func emit(r *Report) {
	ReportIfChanged(r, false)
}

// StartRefresher 启动周期性重扫（网关/Agent 本地自扫描；后台配置下发时会被覆盖）。
// 首次扫描同步完成后再进入周期循环，保证第一个 PeerConnection 建立时规则已就绪。
// onReport 每次扫描回调一次；扫描失败时传入 Valid=false 的占位报告。
func StartRefresher(stop <-chan struct{}, opt Options, every time.Duration, onReport func(*Report)) {
	if every <= 0 {
		every = 5 * time.Minute
	}
	run := func() {
		rep, err := Refresh(CurrentOptions(opt))
		if onReport == nil {
			return
		}
		if err != nil {
			// 扫描失败时保留现有规则（不回退到无过滤）
			rep = &Report{Valid: false, Reason: "扫描失败: " + err.Error()}
		}
		onReport(rep)
	}
	run()
	go func() {
		t := time.NewTicker(every)
		defer t.Stop()
		for {
			select {
			case <-stop:
				return
			case <-t.C:
				run()
			}
		}
	}()
}

// EnvBool 读取布尔环境变量（1/true/yes/on 为真），未设置或非法时返回 def
func EnvBool(name string, def bool) bool {
	v := strings.TrimSpace(os.Getenv(name))
	if v == "" {
		return def
	}
	switch strings.ToLower(v) {
	case "1", "true", "yes", "y", "on":
		return true
	case "0", "false", "no", "n", "off":
		return false
	}
	return def
}

// evaluate 纯函数：接口清单 → 评分 → 保留集 → 规则
func evaluate(raw []rawIface, opt Options) *Report {
	opt = opt.withDefaults()
	out := make([]Iface, 0, len(raw))
	hostBefore := 0
	addrBefore := 0
	var defaultName string

	for _, ri := range raw {
		f := Iface{
			Name: ri.Name, MTU: ri.MTU, IsDefault: ri.IsDefault, IsLoopback: ri.Loopback,
			IsVirtual: isVirtual(ri.Name),
		}
		if ri.Up {
			f.State = "UP"
		} else {
			f.State = "DOWN"
		}
		for _, a := range ri.Addrs {
			ip := addrIP(a)
			// pion 对每个非回环单播地址产出一个 host 候选（实测含 v6 link-local）
			if ip == nil || ip.IsLoopback() || ip.IsUnspecified() || ip.IsMulticast() {
				continue
			}
			f.Addrs = append(f.Addrs, a.String())
			if ip.To4() != nil {
				if ip.IsGlobalUnicast() {
					f.HasGlobalV4 = true
				}
			} else if ip.IsGlobalUnicast() {
				f.HasGlobalV6 = true
			}
		}
		f.AddrCount = len(f.Addrs)
		if ri.IsDefault {
			defaultName = ri.Name
		}
		if !f.IsLoopback && f.AddrCount > 0 {
			hostBefore++
			addrBefore += f.AddrCount
		}
		f.Score, f.HardDrop, f.Reason = score(f, opt)
		f.Keep = !f.HardDrop && f.Score >= opt.MinScore
		if f.Keep && f.Reason == "" {
			f.Reason = "score"
		}
		out = append(out, f)
	}

	// 选取：默认路由接口强制保留（若未被硬否决），其余按分数取前 MaxKeep
	selected := selectKeep(out, defaultName, opt)

	keepNames := make([]string, 0, len(selected))
	hostAfter := 0
	addrAfter := 0
	sel := make(map[string]bool, len(selected))
	for _, n := range selected {
		keepNames = append(keepNames, n)
		sel[n] = true
	}
	for i := range out {
		f := &out[i]
		if sel[f.Name] {
			if !f.Keep {
				f.Keep = true
				f.Reason = "默认路由强制保留"
			}
			if !f.IsLoopback && f.AddrCount > 0 {
				hostAfter++
				addrAfter += f.AddrCount
			}
		} else if f.Keep {
			f.Keep = false // 超出 MaxKeep
			if f.Reason == "score" {
				f.Reason = "分数够但超出 max_keep"
			}
		}
	}

	rule := icefilter.Rule{
		Enabled:        true,
		Mode:           icefilter.ModeAuto,
		Keep:           keepNames,
		DropPrefix:     icefilter.DefaultDropPrefixes,
		AllowTailscale: opt.AllowTailscale,
	}
	rule, degraded := rule.Normalize()

	rep := &Report{
		Interfaces:  out,
		IfHash:      hashIfaces(out),
		ScannedAt:   time.Now().Format(time.RFC3339),
		Rule:        rule,
		Valid:       !degraded,
		Reason:      "ok",
		HostBefore:  hostBefore,
		HostAfter:   hostAfter,
		AddrBefore:  addrBefore,
		AddrAfter:   addrAfter,
		EstCandBef:  addrBefore + estServerCandidates,
		EstCandAft:  addrAfter + estServerCandidates,
		EstPairsBef: (addrBefore + estServerCandidates) * (addrBefore + estServerCandidates),
		EstPairsAft: (addrAfter + estServerCandidates) * (addrAfter + estServerCandidates),
	}
	if degraded {
		rep.Reason = "keep 集为空，已降级为仅黑名单过滤（rule_invalid）"
	}
	if defaultName != "" && !sel[defaultName] {
		rep.Reason = "警告：默认路由接口 " + defaultName + " 未入选，已降级评估"
	}
	return rep
}

// score 单接口评分（方案 §4.3）
func score(f Iface, opt Options) (int, bool, string) {
	if f.IsLoopback {
		return 0, true, "loopback"
	}
	if f.State != "UP" {
		return 0, true, "down"
	}
	if matchPrefix(f.Name, icefilter.DefaultDropPrefixes) {
		return 0, true, "硬黑名单"
	}
	if matchName(f.Name, icefilter.DefaultDropNames) {
		return 0, true, "loopback/隧道设备"
	}
	if !opt.AllowTailscale && matchPrefix(f.Name, []string{icefilter.TailscalePrefix}) {
		return 0, true, "决策 D2：缺省不允许 Tailscale"
	}
	if !f.HasGlobalV4 && !f.HasGlobalV6 {
		return 0, false, "无全局地址"
	}

	s := 0
	var why []string
	if f.IsDefault {
		s += 50
		why = append(why, "默认路由+50")
	}
	if f.HasGlobalV4 {
		s += 20
		why = append(why, "IPv4+20")
	}
	if f.HasGlobalV6 {
		s += 10
		why = append(why, "IPv6+10")
	}
	if !f.IsVirtual {
		s += 15
		why = append(why, "物理+15")
	}
	if f.MTU >= 1400 {
		s += 5
		why = append(why, "MTU+5")
	}
	for _, h := range opt.History {
		if h == f.Name {
			s += 30
			why = append(why, "历史胜出+30")
			break
		}
	}
	return s, false, strings.Join(why, " ")
}

// selectKeep 选出最终保留接口名
func selectKeep(list []Iface, defaultName string, opt Options) []string {
	type cand struct {
		name  string
		score int
	}
	var cands []cand
	for _, f := range list {
		if f.Keep {
			cands = append(cands, cand{f.Name, f.Score})
		}
	}
	sort.SliceStable(cands, func(i, j int) bool { return cands[i].score > cands[j].score })

	var out []string
	seen := map[string]bool{}
	add := func(n string) {
		if n == "" || seen[n] {
			return
		}
		seen[n] = true
		out = append(out, n)
	}

	// ① 默认路由接口强制保留
	if defaultName != "" {
		for _, f := range list {
			if f.Name == defaultName && !f.HardDrop {
				add(defaultName)
				break
			}
		}
	}
	// ② 按分数补齐到 MaxKeep
	for _, c := range cands {
		if len(out) >= opt.MaxKeep {
			break
		}
		add(c.name)
	}
	return out
}

// hashIfaces 接口指纹：接口清单变化 → 缓存失效、回退阶梯回落
func hashIfaces(list []Iface) string {
	lines := make([]string, 0, len(list))
	for _, f := range list {
		lines = append(lines, strings.Join([]string{
			f.Name, f.State, itoa(f.MTU), boolStr(f.IsDefault),
			strings.Join(f.Addrs, ";"),
		}, "|"))
	}
	sort.Strings(lines)
	sum := sha256.Sum256([]byte(strings.Join(lines, "\n")))
	return hex.EncodeToString(sum[:8])
}

// DefaultIface 返回默认路由出口接口名（Linux /proc/net/route）
func DefaultIface() (string, error) {
	data, err := os.ReadFile("/proc/net/route")
	if err != nil {
		return "", err
	}
	name := parseDefaultIface(string(data))
	if name == "" {
		return "", os.ErrNotExist
	}
	return name, nil
}

// parseDefaultIface 解析 /proc/net/route，取 Destination=00000000 的接口
func parseDefaultIface(content string) string {
	lines := strings.Split(content, "\n")
	for i, ln := range lines {
		if i == 0 || strings.TrimSpace(ln) == "" {
			continue // 表头
		}
		f := strings.Fields(ln)
		if len(f) < 4 {
			continue
		}
		if f[1] != "00000000" {
			continue
		}
		return f[0]
	}
	return ""
}

// isVirtual 判定虚拟接口（用于 +15 物理加分的排除）
func isVirtual(name string) bool {
	virtual := []string{
		"docker", "br-", "veth", "virbr", "vbox", "vmnet", "zt",
		"cni", "flannel", "cali", "kube", "weave",
		icefilter.TailscalePrefix,
		"lo", "sit", "ip6tnl", "ip6_vti", "teql", "tunl",
		"tun", "tap", "wg", "ppp", "bond", "dummy", "ifb", "nhi",
	}
	// "br0" 属网桥但为实际承载链路，按物理计（避免漏掉唯一可用口）
	if name == "br0" {
		return false
	}
	return matchPrefix(name, virtual)
}

func matchPrefix(name string, prefixes []string) bool {
	for _, p := range prefixes {
		if p != "" && strings.HasPrefix(name, p) {
			return true
		}
	}
	return false
}

func matchName(name string, names []string) bool {
	for _, n := range names {
		if n == name {
			return true
		}
	}
	return false
}

func addrIP(a net.Addr) net.IP {
	switch v := a.(type) {
	case *net.IPNet:
		return v.IP
	case *net.IPAddr:
		return v.IP
	}
	return nil
}

func boolStr(b bool) string {
	if b {
		return "1"
	}
	return "0"
}

func itoa(v int) string {
	if v == 0 {
		return "0"
	}
	neg := v < 0
	if neg {
		v = -v
	}
	var b [20]byte
	i := len(b)
	for v > 0 {
		i--
		b[i] = byte('0' + v%10)
		v /= 10
	}
	if neg {
		i--
		b[i] = '-'
	}
	return string(b[i:])
}
