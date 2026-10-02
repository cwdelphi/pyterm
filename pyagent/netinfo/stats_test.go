package netinfo

import "testing"

// P2 §7.2: 看板数据（last_pair/ladder/path_cache）变化必须触发上报，
// 否则 UI 要等 5 分钟周期重扫才能看到新数据；相同指纹仍要去重。
func TestReportIfChangedStatsFingerprint(t *testing.T) {
	defer SetReporter(nil)
	defer ClearStats()

	var got []*Report
	SetReporter(func(r *Report) { got = append(got, r) })

	rep := &Report{IfHash: "stats-hash-xyz", Valid: true}
	if !ReportIfChanged(rep, false) {
		t.Fatalf("首次上报应触发")
	}

	// 指纹为空且接口未变 → 去重
	if ReportIfChanged(rep, false) {
		t.Errorf("无看板数据变化时应去重")
	}

	// 加入阶梯统计 → 指纹变化 → 触发上报
	rep2 := &Report{IfHash: "stats-hash-xyz", Valid: true}
	SetLadder(&Ladder{Level: 2, Fallbacks: 1})
	attachStats(rep2)
	if !ReportIfChanged(rep2, false) {
		t.Errorf("看板数据变化应触发上报")
	}

	// 指纹相同 → 再次去重
	rep3 := &Report{IfHash: "stats-hash-xyz", Valid: true}
	attachStats(rep3)
	if ReportIfChanged(rep3, false) {
		t.Errorf("看板数据未变时应去重")
	}

	// 清空看板数据（ice_cache_clear）→ 指纹回到空 → 触发上报
	ClearStats()
	rep4 := &Report{IfHash: "stats-hash-xyz", Valid: true}
	attachStats(rep4)
	if !ReportIfChanged(rep4, false) {
		t.Errorf("看板数据清空也应触发上报")
	}

	if len(got) != 3 {
		t.Errorf("期望 3 次上报, got %d", len(got))
	}
}

// attachStats 应把统计挂到报告上，且 PathCache 来自路径缓存
func TestAttachStats(t *testing.T) {
	defer ClearStats()
	ClearStats()

	r := &Report{}
	attachStats(r)
	if r.LastPair != nil || r.Ladder != nil || len(r.PathCache) != 0 {
		t.Errorf("无统计时应保持空, got %+v %+v %v", r.LastPair, r.Ladder, r.PathCache)
	}

	SetLastPair(&LastPair{Iface: "eth0", ConnType: "P2P", Level: 2})
	SetLadder(&Ladder{Level: 2, Fallbacks: 3})
	r2 := &Report{}
	attachStats(r2)
	if r2.LastPair == nil || r2.LastPair.Iface != "eth0" {
		t.Errorf("last_pair 未挂载: %+v", r2.LastPair)
	}
	if r2.Ladder == nil || r2.Ladder.Fallbacks != 3 {
		t.Errorf("ladder 未挂载: %+v", r2.Ladder)
	}

	// GetLadder 返回副本，改它不应影响内部状态
	l := GetLadder()
	if l == nil {
		t.Fatalf("GetLadder 不应为 nil")
	}
	l.Fallbacks = 999
	if GetLadder().Fallbacks != 3 {
		t.Errorf("GetLadder 应返回副本")
	}
}
