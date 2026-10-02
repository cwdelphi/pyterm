package webrtc

import (
	"testing"
	"time"
)

// D4: ICE 失败后的重试节流（ice_cooldown）
func TestICECooldownThrottle(t *testing.T) {
	t.Cleanup(func() {
		resetICECooldown()
		setCooldown(defaultICECooldown)
	})

	// 未发生过失败 → 直通
	setCooldown(150 * time.Millisecond)
	resetICECooldown()
	start := time.Now()
	waitICECooldown()
	if d := time.Since(start); d > 50*time.Millisecond {
		t.Fatalf("无失败记录时不应等待, waited %v", d)
	}

	// 失败后立刻重试 → 等满剩余窗口
	markICEFailure()
	start = time.Now()
	waitICECooldown()
	if d := time.Since(start); d < 80*time.Millisecond {
		t.Fatalf("节流未生效, waited %v", d)
	}

	// 窗口内第二次调用 → 已过期，直通
	start = time.Now()
	waitICECooldown()
	if d := time.Since(start); d > 50*time.Millisecond {
		t.Fatalf("窗口已过期不应再等, waited %v", d)
	}

	// 窗口=0 → 关闭节流
	setCooldown(0)
	markICEFailure()
	start = time.Now()
	waitICECooldown()
	if d := time.Since(start); d > 50*time.Millisecond {
		t.Fatalf("ice_cooldown=0 时不应等待, waited %v", d)
	}
}

func TestSetICECooldownClamp(t *testing.T) {
	t.Cleanup(func() { setCooldown(defaultICECooldown) })

	SetICECooldown(-3)
	if ICECooldown() != 0 {
		t.Fatalf("负数应归 0, got %v", ICECooldown())
	}
	SetICECooldown(999)
	if ICECooldown() != 30*time.Second {
		t.Fatalf("上限应为 30s, got %v", ICECooldown())
	}
	SetICECooldown(7)
	if ICECooldown() != 7*time.Second {
		t.Fatalf("7s 应原样生效, got %v", ICECooldown())
	}
}
