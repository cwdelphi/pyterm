package webrtc

import (
	"sync"
	"time"
)

// D4：ice_cooldown 落地为「ICE 失败后的重试节流窗口」。
// 方案 §6 决策 D4：默认 2s，取值 0–30s（0 = 关闭节流）。
// 挂在 NewPeer 入口：距上次 ICE 失败不足窗口时，等窗口走完再建连，
// 避免失败后立刻重试把同样的候选对再打满一次。
const defaultICECooldown = 2 * time.Second

var (
	cdMu   sync.Mutex
	cdWait = defaultICECooldown
	cdFail time.Time
	cdSkip bool
)

// SetICECooldown 设置节流窗口（秒，负数归 0，>30 归 30）
func SetICECooldown(sec int) {
	setCooldown(iceCooldownDuration(sec))
}

func iceCooldownDuration(sec int) time.Duration {
	if sec < 0 {
		sec = 0
	}
	if sec > 30 {
		sec = 30
	}
	return time.Duration(sec) * time.Second
}

func setCooldown(d time.Duration) {
	cdMu.Lock()
	cdWait = d
	cdMu.Unlock()
}

// ICECooldown 当前节流窗口（测试/日志用）
func ICECooldown() time.Duration {
	cdMu.Lock()
	defer cdMu.Unlock()
	return cdWait
}

// markICEFailure 记录一次 ICE 失败（OnICEConnectionStateChange=Failed）
func markICEFailure() {
	cdMu.Lock()
	cdFail = time.Now()
	cdMu.Unlock()
}

// skipICECooldown 下一次 NewPeer 跳过节流（P2 阶梯：网关指令性升级重建不算失败重试，
// 否则 L1→L2/L2→L3 每级都要白等一个 D4 窗口，阶梯预算直接爆掉）
func skipICECooldown() {
	cdMu.Lock()
	cdSkip = true
	cdMu.Unlock()
}

// waitICECooldown 节流：距上次失败不足窗口时阻塞剩余时间；窗口为 0 或无失败则直通
func waitICECooldown() {
	cdMu.Lock()
	if cdSkip {
		cdSkip = false
		cdMu.Unlock()
		return
	}
	d := cdWait
	last := cdFail
	cdMu.Unlock()
	if d <= 0 || last.IsZero() {
		return
	}
	remain := time.Until(last.Add(d))
	if remain <= 0 {
		return
	}
	time.Sleep(remain)
}

// resetICECooldown 测试用：清空失败时间戳
func resetICECooldown() {
	cdMu.Lock()
	cdFail = time.Time{}
	cdMu.Unlock()
}
