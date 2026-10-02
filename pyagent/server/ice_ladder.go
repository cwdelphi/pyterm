package server

import (
	"encoding/json"
	"log"
	"net"
	"strings"
	"sync"
	"time"

	"github.com/pion/webrtc/v4"

	"github.com/ppy-tools/pyagent/icefilter"
	wrtc "github.com/ppy-tools/pyagent/webrtc"
)

// ── P2 三级回退阶梯（docs/传输优化_ICE优化与接口过滤方案_v2.0.md §4.5）──────
//
// L1 快路径: 只放行上次胜出的本地接口（缓存 TTL 30min, if_hash 变化即失效）
// L2 规则过滤: icefilter.Predicate()（P0 缺省）
// L3 不过滤  : 兜底（连通性优先，候选对回到全量）
//
// 网关是 offerer，因此阶梯由网关执行；等级随 offer 的 ice_level 下行，
// agent 照做自己的过滤。后台 config_json.ice.auto_fallback=false 时退化为单级，
// 网关配置/环境变量 WRG_ICE_AUTO_FALLBACK 再做一次进程级兜底（决策 D3）。

const (
	iceLevelL1 = 1
	iceLevelL2 = 2
	iceLevelL3 = 3

	// l1Window L1 快路径窗口：answer 生效后 800ms 仍未选出候选对 → 回落 L2
	l1Window = 800 * time.Millisecond
	// maxRebuild 单次会话内最多重建次数（L1→L2→L3 两级）
	maxRebuild = 2
	// successStreakLimit 连续成功阈值：达到后下次会话降一级（收紧）
	successStreakLimit = 3
	// selectDeadline 记录胜出接口时等待选中候选对的时限
	selectDeadline = 3 * time.Second
)

// levelName L1/L2/L3
func levelName(level int) string {
	switch level {
	case iceLevelL1:
		return "L1"
	case iceLevelL2:
		return "L2"
	default:
		return "L3"
	}
}

// ladder 进程级阶梯状态（按 agent 维度，跨会话收敛）
type ladder struct {
	mu        sync.Mutex
	streak    map[string]int // 连续成功次数
	preferred map[string]int // 偏好等级：缺省 L1（允许缓存快路径）；失败回落 L2；连3次成功回到 L1
}

func newLadder() *ladder {
	return &ladder{
		streak:    map[string]int{},
		preferred: map[string]int{},
	}
}

// startLevel 本次会话的起始等级（方案 §4.5）：
//   - L1 需要「缓存命中」+「偏好允许」(preferred==L1)，否则退 L2；
//   - L3 只在会话内失败重建时出现，绝不作为起始等级（避免候选风暴）。
func (h *Handler) startLevel(agentID string, pathCacheOn bool) int {
	h.ladderState.mu.Lock()
	p := h.ladderState.preferred[agentID]
	h.ladderState.mu.Unlock()
	if p == 0 {
		p = iceLevelL1 // 缺省允许快路径（首次连接没有缓存，自然走 L2）
	}
	if p != iceLevelL1 || !pathCacheOn {
		return iceLevelL2
	}
	if _, ok := h.pathCache.Lookup(agentID, currentIfHash()); ok {
		return iceLevelL1
	}
	return iceLevelL2
}

// ladderSuccess 一次建连成功：连 3 次成功 → 下次降一级（收紧回 L1 快路径）
func (h *Handler) ladderSuccess(agentID string) {
	h.ladderState.mu.Lock()
	defer h.ladderState.mu.Unlock()
	h.ladderState.streak[agentID]++
	if h.ladderState.streak[agentID] < successStreakLimit {
		return
	}
	h.ladderState.streak[agentID] = 0
	if h.ladderState.preferred[agentID] != iceLevelL1 {
		h.ladderState.preferred[agentID] = iceLevelL1
		log.Printf("[ICE-LADDER] agent=%s 收紧到%s (连续 %d 次成功)", agentID, levelName(iceLevelL1), successStreakLimit)
	}
}

// ladderFailure 一次建连失败：清零连续计数并回落 L2（下次会话不再走缓存快路径）
func (h *Handler) ladderFailure(agentID string) {
	h.ladderState.mu.Lock()
	defer h.ladderState.mu.Unlock()
	h.ladderState.streak[agentID] = 0
	// 未设置(0)与 L1 都视为「允许快路径」，失败一律显式回落 L2
	if h.ladderState.preferred[agentID] != iceLevelL2 {
		h.ladderState.preferred[agentID] = iceLevelL2
		log.Printf("[ICE-LADDER] agent=%s 回落%s (ICE失败, 暂停缓存快路径)", agentID, levelName(iceLevelL2))
	}
}

// ladderReset 接口集合变化（if_hash 变化）→ 立即回落 L2 并清缓存
func (h *Handler) ladderReset(agentID string) {
	h.ladderState.mu.Lock()
	h.ladderState.streak[agentID] = 0
	h.ladderState.preferred[agentID] = iceLevelL2
	h.ladderState.mu.Unlock()
}

// levelPredicate 按等级生成本端接口过滤器；L3 返回 nil（不过滤）
func (h *Handler) levelPredicate(level int, agentID string) func(string) bool {
	switch level {
	case iceLevelL1:
		e, ok := h.pathCache.Lookup(agentID, currentIfHash())
		if !ok {
			// 缓存失效（过期/if_hash 变化）→ 退回 L2 语义
			return icefilter.Predicate()
		}
		if !icefilter.Current().Match(e.LocalIface) {
			// 胜出接口已被现行规则丢弃 → 不可能连上，直接按 L2
			log.Printf("[ICE-LADDER] 缓存接口 %s 已被规则丢弃, 回落 L2 (agent=%s)", e.LocalIface, agentID)
			return icefilter.Predicate()
		}
		return func(name string) bool { return name == e.LocalIface }
	case iceLevelL3:
		return nil
	default:
		return icefilter.Predicate()
	}
}

var (
	ifHashMu   sync.RWMutex
	lastIfHash string
)

// currentIfHash 当前接口集合指纹（由 OnNetInfoChanged 随周期重扫更新；空表示尚未扫描）
func currentIfHash() string {
	ifHashMu.RLock()
	defer ifHashMu.RUnlock()
	return lastIfHash
}

// ifaceForIP 用本机接口地址反查接口名（胜出接口的缓存键）
func ifaceForIP(ip string) string {
	if ip == "" {
		return ""
	}
	list, err := net.Interfaces()
	if err != nil {
		return ""
	}
	for _, ni := range list {
		addrs, _ := ni.Addrs()
		for _, a := range addrs {
			host := strings.Split(a.String(), "/")[0]
			if host == ip {
				return ni.Name
			}
		}
	}
	return ""
}

// recordPath 等待选中候选对出现后记录本端胜出接口（路径缓存 + 阶梯统计）
func (h *Handler) recordPath(session *Session, gen int, roomID, agentID string, level int) {
	deadline := time.Now().Add(selectDeadline)
	var pair *webrtc.ICECandidatePair
	for time.Now().Before(deadline) {
		if !session.isGen(gen) {
			return
		}
		session.mu.Lock()
		pc := session.PeerConn
		session.mu.Unlock()
		if pc == nil || pc.ConnectionState() == webrtc.PeerConnectionStateClosed {
			return
		}
		if p := selectedCandidatePair(pc); p != nil {
			pair = p
			break
		}
		time.Sleep(100 * time.Millisecond)
	}
	if pair == nil || pair.Local == nil {
		return
	}
	iface := ifaceForIP(pair.Local.Address)
	if iface == "" {
		log.Printf("[ICE-LADDER] 无法映射胜出接口 %s, 跳过缓存", pair.Local.Address)
		return
	}
	connType := wrtc.ClassifyConnType(pair.Local.Typ, pair.Remote.Typ)
	if h.pathCache != nil {
		h.pathCache.Record(agentID, iface, currentIfHash(), connType, 0)
	}
	log.Printf("[ICE-LADDER] room=%s %s 命中本地接口 %s (conn_type=%s, 缓存 %d 条)",
		roomID, levelName(level), iface, connType, h.pathCache.Len())
}

// l1WindowExpired L1 窗口到期仍未选出候选对 → 回落 L2 重建
func (h *Handler) l1WindowExpired(session *Session, gen int, roomID, agentID string) {
	session.mu.Lock()
	defer session.mu.Unlock()
	if session.gen.Load() != int32(gen) || session.connected || session.iceLevel != iceLevelL1 {
		return
	}
	select {
	case <-session.Done:
		return
	default:
	}
	session.iceL1Timer = nil
	log.Printf("[ICE-LADDER] room=%s L1 窗口 %v 无选中对 → 回落 L2", roomID, l1Window)
	h.escalateLocked(session, gen, roomID, agentID, "l1_window_timeout")
}

// escalateLocked 阶梯升级重建；返回 false 表示已到 L3 或策略关闭，走原错误路径。
// 调用方必须持有 session.mu。
func (h *Handler) escalateLocked(session *Session, gen int, roomID, agentID, cause string) bool {
	if session.currentGen() != gen {
		return true // 已被更晚的世代接管
	}
	select {
	case <-session.Done:
		return true // 浏览器已断开，不重建也不报错
	default:
	}
	level := session.iceLevel
	if !session.autoFallback || level >= iceLevelL3 || session.iceRebuilds >= maxRebuild {
		return false
	}
	next := level + 1
	session.iceRebuilds++
	h.startPeerLocked(session, roomID, agentID, next, cause)
	return true
}

// preferredLevel 某 agent 下次会话的偏好等级（单测/看板用；未设置=允许 L1）
func (h *Handler) preferredLevel(agentID string) int {
	h.ladderState.mu.Lock()
	defer h.ladderState.mu.Unlock()
	p := h.ladderState.preferred[agentID]
	if p == 0 {
		return iceLevelL1
	}
	return p
}

// sendConnectError 走原错误路径（关 PC + 通知浏览器）
func (h *Handler) sendConnectError(session *Session, roomID, detail string) {
	if session.PeerConn != nil {
		// gather 卡 STUN/TURN 时 Close 会阻塞（持 session.mu 调用方会挂）→ 异步关
		pc := session.PeerConn
		session.PeerConn = nil
		go pc.Close()
	}
	j, _ := json.Marshal(map[string]interface{}{
		"type":    "error",
		"room_id": roomID,
		"detail":  detail,
	})
	session.enqueue(j, true)
}

// OnNetInfoChanged 本机接口集合变化（if_hash 变化）→ 清理失效路径缓存并回落 L2（§4.5）。
// 由 main 的 netinfo 周期重扫回调调用。
func (h *Handler) OnNetInfoChanged(ifHash string) {
	if ifHash == "" || h.pathCache == nil {
		return
	}
	ifHashMu.Lock()
	lastIfHash = ifHash
	ifHashMu.Unlock()
	if n := h.pathCache.InvalidateIfHash(ifHash); n > 0 {
		log.Printf("[ICE-LADDER] if_hash 变化 → 清理 %d 条路径缓存, 回落到L2", n)
	}
	h.ladderState.mu.Lock()
	for k := range h.ladderState.preferred {
		h.ladderState.streak[k] = 0
		h.ladderState.preferred[k] = iceLevelL2
	}
	h.ladderState.mu.Unlock()
}

// isGen 判断 gen 是否仍是当前世代（原子读：旧 PeerConnection 的回调据此静默退出，
// 不能拿 session.mu 判断——重建时可能正持有该锁）
func (s *Session) isGen(gen int) bool {
	return s.currentGen() == gen
}

// currentGen 当前世代号
func (s *Session) currentGen() int {
	return int(s.gen.Load())
}
