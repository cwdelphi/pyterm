package server

import (
	"encoding/json"
	"testing"
	"time"

	"github.com/pion/webrtc/v4"

	"github.com/ppy-tools/pyagent/config"
	"github.com/ppy-tools/pyagent/pathcache"
	"github.com/ppy-tools/pyagent/signaling"
)

func newLadderHandler(t *testing.T) *Handler {
	t.Helper()
	h := NewHandler(&config.Config{
		GatewayID:       "gw-test",
		IceAutoFallback: true,
		IcePathCache:    true,
	})
	h.pathCache = pathcache.NewTTL(t.TempDir(), time.Minute)
	return h
}

// 新会话无缓存 → L2（L1 无从放行）
func TestStartLevelDefaultsToL2WithoutCache(t *testing.T) {
	h := newLadderHandler(t)
	if got := h.startLevel("a", true); got != iceLevelL2 {
		t.Fatalf("want L2, got %d", got)
	}
	// 近期失败(preference=L2) → 即使有缓存也不走 L1
	h.pathCache.Record("a", "eth0", currentIfHash(), "P2P", 0)
	if got := h.startLevel("a", true); got != iceLevelL1 {
		t.Fatalf("want L1 on cache hit with default preference, got %d", got)
	}
	h.ladderFailure("a")
	if got := h.startLevel("a", true); got != iceLevelL2 {
		t.Fatalf("want L2 after failure despite cache, got %d", got)
	}
}

// 缓存命中 → L1
func TestStartLevelL1OnCacheHit(t *testing.T) {
	h := newLadderHandler(t)
	h.pathCache.Record("a", "eth0", currentIfHash(), "P2P", 0)
	if got := h.startLevel("a", true); got != iceLevelL1 {
		t.Fatalf("want L1 on cache hit, got %d", got)
	}
	// path_cache 关闭 → 从不进 L1
	if got := h.startLevel("a", false); got != iceLevelL2 {
		t.Fatalf("want L2 when path_cache off, got %d", got)
	}
}

// L1 过滤器只放行胜出接口；L3 不设过滤；L2 用规则
func TestLevelPredicate(t *testing.T) {
	h := newLadderHandler(t)
	h.pathCache.Record("a", "eth0", currentIfHash(), "P2P", 0)

	p1 := h.levelPredicate(iceLevelL1, "a")
	if p1 == nil || !p1("eth0") || p1("wlan0") {
		t.Fatalf("L1 predicate must pass eth0 only, got=%v", p1("eth0"))
	}

	if p3 := h.levelPredicate(iceLevelL3, "a"); p3 != nil {
		t.Fatal("L3 must not install a filter")
	}

	p2 := h.levelPredicate(iceLevelL2, "a")
	if p2 == nil {
		t.Fatal("L2 must install the rule predicate")
	}
}

// L1 无缓存 → 自动退化成 L2 语义（不会把候选全掐死）
func TestLevelPredicateL1FallsBackWithoutCache(t *testing.T) {
	h := newLadderHandler(t)
	p := h.levelPredicate(iceLevelL1, "unknown-agent")
	if p == nil {
		t.Fatal("L1 without cache must degrade to L2 predicate")
	}
}

// 连 3 次成功 → 收紧回 L1；失败 → 回落 L2（暂停快路径）
func TestLadderStreak(t *testing.T) {
	h := newLadderHandler(t)
	if pref := h.preferredLevel("a"); pref != iceLevelL1 {
		t.Fatalf("want default preferred L1, got %d", pref)
	}
	h.ladderFailure("a")
	if pref := h.preferredLevel("a"); pref != iceLevelL2 {
		t.Fatalf("want preferred L2 after failure, got %d", pref)
	}
	// 失败后连 3 次成功 → 重新允许 L1
	h.ladderSuccess("a")
	h.ladderSuccess("a")
	if pref := h.preferredLevel("a"); pref != iceLevelL2 {
		t.Fatalf("want preferred stay L2 after 2 successes, got %d", pref)
	}
	h.ladderSuccess("a")
	if pref := h.preferredLevel("a"); pref != iceLevelL1 {
		t.Fatalf("want preferred L1 after 3 successes, got %d", pref)
	}
	// 失败只回到 L2，绝不把起始等级抬到 L3（L3 仅会话内失败重建出现）
	h.ladderFailure("a")
	h.ladderFailure("a")
	if pref := h.preferredLevel("a"); pref != iceLevelL2 {
		t.Fatalf("want preferred L2 (never L3), got %d", pref)
	}
	// if_hash 变化 → 回落 L2
	h.ladderReset("a")
	if pref := h.preferredLevel("a"); pref != iceLevelL2 {
		t.Fatalf("want preferred L2 after reset, got %d", pref)
	}
}

// 三级重建阶梯：L1→L2→L3，L3 后回原错误路径
func TestEscalateLockedLadder(t *testing.T) {
	h := newLadderHandler(t)
	s := &Session{
		ID:            "t",
		RoomID:        "room-x",
		AgentID:       "a",
		SendCh:        make(chan []byte, 16),
		Done:          make(chan struct{}),
		autoFallback:  true,
		pathCacheOn:   true,
		iceLevel:      iceLevelL1,
		answerApplied: false,
	}
	s.mu.Lock()
	gen := s.currentGen() // 0：尚未建过 peer，这里手动起 L1 起点
	if !h.escalateLocked(s, gen, s.RoomID, s.AgentID, "test") {
		s.mu.Unlock()
		t.Fatal("L1 should escalate to L2")
	}
	if s.iceLevel != iceLevelL2 || s.iceRebuilds != 1 {
		s.mu.Unlock()
		t.Fatalf("want L2 rebuilds=1, got L%d rebuilds=%d", s.iceLevel, s.iceRebuilds)
	}
	if s.currentGen() != gen+1 {
		s.mu.Unlock()
		t.Fatalf("want gen bumped to %d, got %d", gen+1, s.currentGen())
	}
	// L2 → L3
	if !h.escalateLocked(s, gen+1, s.RoomID, s.AgentID, "test") {
		s.mu.Unlock()
		t.Fatal("L2 should escalate to L3")
	}
	if s.iceLevel != iceLevelL3 || s.iceRebuilds != 2 {
		s.mu.Unlock()
		t.Fatalf("want L3 rebuilds=2, got L%d rebuilds=%d", s.iceLevel, s.iceRebuilds)
	}
	// L3 不再重建 → 走原错误路径
	if h.escalateLocked(s, gen+2, s.RoomID, s.AgentID, "test") {
		s.mu.Unlock()
		t.Fatal("L3 must not rebuild")
	}
	s.mu.Unlock()
	// 会话已用尽重建额度：即使 level 更低也不再重建
	s2 := &Session{
		ID:           "t2",
		RoomID:       "room-y",
		AgentID:      "a",
		SendCh:       make(chan []byte, 16),
		Done:         make(chan struct{}),
		autoFallback: true,
		pathCacheOn:  true,
		iceLevel:     iceLevelL2,
		iceRebuilds:  maxRebuild,
	}
	s2.mu.Lock()
	if h.escalateLocked(s2, s2.currentGen(), s2.RoomID, s2.AgentID, "test") {
		s2.mu.Unlock()
		t.Fatal("rebuild budget exhausted must not rebuild")
	}
	s2.mu.Unlock()
}

// auto_fallback 关闭 → 单级，不重建
func TestEscalateLockedDisabled(t *testing.T) {
	h := newLadderHandler(t)
	s := &Session{
		ID:           "t",
		RoomID:       "room-z",
		AgentID:      "a",
		SendCh:       make(chan []byte, 16),
		Done:         make(chan struct{}),
		autoFallback: false,
		iceLevel:     iceLevelL1,
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if h.escalateLocked(s, s.currentGen(), s.RoomID, s.AgentID, "test") {
		t.Fatal("auto_fallback=false must not rebuild")
	}
}

// 世代号：旧 gen 的回调不触发重建
func TestEscalateLockedStaleGen(t *testing.T) {
	h := newLadderHandler(t)
	s := &Session{
		ID:           "t",
		RoomID:       "room-s",
		AgentID:      "a",
		SendCh:       make(chan []byte, 16),
		Done:         make(chan struct{}),
		autoFallback: true,
		iceLevel:     iceLevelL1,
	}
	s.gen.Store(5)
	s.mu.Lock()
	defer s.mu.Unlock()
	if !h.escalateLocked(s, 4, s.RoomID, s.AgentID, "stale") {
		t.Fatal("stale gen must be treated as superseded (return true, no rebuild)")
	}
	if s.iceLevel != iceLevelL1 || s.iceRebuilds != 0 {
		t.Fatalf("stale gen must not touch ladder, got L%d rebuilds=%d", s.iceLevel, s.iceRebuilds)
	}
}

// 浏览器已断开 → 不再重建
func TestEscalateLockedSessionDone(t *testing.T) {
	h := newLadderHandler(t)
	s := &Session{
		ID:           "t",
		RoomID:       "room-d",
		AgentID:      "a",
		SendCh:       make(chan []byte, 16),
		Done:         make(chan struct{}),
		autoFallback: true,
		iceLevel:     iceLevelL1,
	}
	close(s.Done)
	s.mu.Lock()
	defer s.mu.Unlock()
	h.escalateLocked(s, s.currentGen(), s.RoomID, s.AgentID, "test")
	if s.iceLevel != iceLevelL1 || s.iceRebuilds != 0 {
		t.Fatalf("closed session must not rebuild, got L%d rebuilds=%d", s.iceLevel, s.iceRebuilds)
	}
}

// md 下发的 ice_policy 解析 + 网关配置兜底
func TestParseIcePolicy(t *testing.T) {
	h := newLadderHandler(t)

	// 无 policy → 默认全开（网关配置也开）
	if af, pc := h.parseIcePolicy(&signaling.Message{}); !af || !pc {
		t.Fatalf("default policy want both true, got af=%v pc=%v", af, pc)
	}

	// 后台关掉 auto_fallback
	raw, _ := json.Marshal(map[string]bool{"auto_fallback": false, "path_cache": false})
	af, pc := h.parseIcePolicy(&signaling.Message{IcePolicy: raw})
	if af || pc {
		t.Fatalf("want both false, got af=%v pc=%v", af, pc)
	}

	// 网关配置兜底：配置关掉 → 即使后台开也关
	h.cfg.IceAutoFallback = false
	raw2, _ := json.Marshal(map[string]bool{"auto_fallback": true, "path_cache": true})
	af, _ = h.parseIcePolicy(&signaling.Message{IcePolicy: raw2})
	if af {
		t.Fatal("gateway config must veto auto_fallback")
	}
}

// 会话状态迁移：startPeerLocked 重置 answer/connected 并清候选缓冲
func TestStartPeerResetsGenerationState(t *testing.T) {
	h := newLadderHandler(t)
	s := &Session{
		ID:                "t",
		RoomID:            "room-r",
		AgentID:           "a",
		SendCh:            make(chan []byte, 16),
		Done:              make(chan struct{}),
		autoFallback:      true,
		iceLevel:          iceLevelL1,
		answerApplied:     true,
		connected:         true,
		iceRebuilds:       1,
		pendingCandidates: []webrtc.ICECandidateInit{{}},
	}
	s.gen.Store(3)
	s.mu.Lock()
	h.startPeerLocked(s, s.RoomID, s.AgentID, iceLevelL2, "test")
	defer s.mu.Unlock()

	if s.currentGen() != 4 {
		t.Fatalf("want gen 4, got %d", s.currentGen())
	}
	if s.answerApplied || s.connected {
		t.Fatal("answerApplied/connected must reset on rebuild")
	}
	if len(s.pendingCandidates) != 0 {
		t.Fatal("stale candidates must be cleared on rebuild")
	}
	if s.iceLevel != iceLevelL2 {
		t.Fatalf("want L2, got %d", s.iceLevel)
	}
	if s.PeerConn == nil {
		t.Fatal("peer must be created")
	}
	if s.DataChannel == nil {
		t.Fatal("datachannel must be created")
	}
	s.PeerConn.Close()
}
