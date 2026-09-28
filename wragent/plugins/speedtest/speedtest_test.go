package speedtest

// 阶段S: 测速接收端插件单测 (TC-ST01 Go 层等价)

import (
	"encoding/json"
	"sync"
	"testing"
	"time"
)

type capture struct {
	mu       sync.Mutex
	payloads []map[string]any
	sent     [][]byte
}

func (c *capture) sendSignal(payload []byte) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	var m map[string]any
	if err := json.Unmarshal(payload, &m); err == nil {
		c.payloads = append(c.payloads, m)
	}
	return nil
}

func (c *capture) ofTypes(typ string) []map[string]any {
	c.mu.Lock()
	defer c.mu.Unlock()
	var out []map[string]any
	for _, m := range c.payloads {
		if m["type"] == typ {
			out = append(out, m)
		}
	}
	return out
}

func newTestPlugin(c *capture) *Plugin {
	p := New(Deps{SendSignal: c.sendSignal, Logf: func(string, ...any) {}})
	p.AttachSender("speedtest_a_b_1", func(b []byte) error {
		c.mu.Lock()
		c.sent = append(c.sent, append([]byte(nil), b...))
		c.mu.Unlock()
		return nil
	}, func() uint64 { return 0 })
	return p
}

func ctrl(t *testing.T, p *Plugin, doc string) {
	t.Helper()
	p.HandleMessage("ctrl", []byte(doc))
}

func TestSessionUpThenDownResult(t *testing.T) {
	c := &capture{}
	p := newTestPlugin(c)
	defer p.Stop()

	ctrl(t, p, `{"t":"start","room":"speedtest_a_b_1","duration":1,"mbps":0}`)
	// 上行喂 4 个 16KB 数据帧
	for i := 0; i < 4; i++ {
		p.HandleMessage("data", make([]byte, 16384))
	}
	st := p.Status()
	if !st.Running || st.Detail == "" {
		t.Fatalf("status after start = %+v", st)
	}

	ctrl(t, p, `{"t":"down","duration":1,"mbps":0}`)
	deadline := time.Now().Add(5 * time.Second)
	var results []map[string]any
	for time.Now().Before(deadline) {
		results = c.ofTypes("speedtest_result")
		if len(results) > 0 {
			break
		}
		time.Sleep(50 * time.Millisecond)
	}
	if len(results) != 1 {
		t.Fatalf("result count = %d, want 1", len(results))
	}
	r := results[0]
	if r["room_id"] != "speedtest_a_b_1" {
		t.Fatalf("room_id = %v", r["room_id"])
	}
	if r["up_bytes"].(float64) != 4*16384 {
		t.Fatalf("up_bytes = %v, want %d", r["up_bytes"], 4*16384)
	}
	if r["down_bytes"].(float64) <= 0 {
		t.Fatalf("down_bytes = %v, want >0", r["down_bytes"])
	}
	if r["up_mbps"].(float64) <= 0 || r["down_mbps"].(float64) <= 0 {
		t.Fatalf("mbps values: up=%v down=%v", r["up_mbps"], r["down_mbps"])
	}
	// 会话结束后状态归零
	if st := p.Status(); st.Running {
		t.Fatalf("status after result = %+v, want idle", st)
	}
	// 下行发送帧均为 0x61 前缀
	c.mu.Lock()
	for _, f := range c.sent {
		if f[0] != frameData {
			t.Fatalf("frame prefix = %#x, want 0x61", f[0])
		}
		if len(f) != 1+chunkSize {
			t.Fatalf("frame len = %d, want %d", len(f), 1+chunkSize)
		}
	}
	c.mu.Unlock()
}

func TestProgressThrottle2Hz(t *testing.T) {
	c := &capture{}
	p := newTestPlugin(c)
	defer p.Stop()

	ctrl(t, p, `{"t":"start","room":"speedtest_a_b_1","duration":1,"mbps":0}`)
	start := time.Now()
	// 模拟上行数据 1.3s
	for time.Since(start) < 1300*time.Millisecond {
		p.HandleMessage("data", make([]byte, 8192))
		time.Sleep(50 * time.Millisecond)
	}
	progs := c.ofTypes("speedtest_progress")
	// 1.3s @ 500ms → 期望 2~3 条; >4 条说明超 2Hz
	if len(progs) == 0 {
		t.Fatal("no progress emitted")
	}
	if len(progs) > 4 {
		t.Fatalf("progress count = %d in 1.3s, want <=4 (2Hz)", len(progs))
	}
	for _, m := range progs {
		if m["room_id"] != "speedtest_a_b_1" || m["phase"] != "up" {
			t.Fatalf("progress payload = %v", m)
		}
	}
}

func TestRateLimit(t *testing.T) {
	c := &capture{}
	p := newTestPlugin(c)
	defer p.Stop()

	ctrl(t, p, `{"t":"start","room":"speedtest_a_b_1","duration":1,"mbps":10}`)
	ctrl(t, p, `{"t":"down","duration":1,"mbps":10}`)
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		if len(c.ofTypes("speedtest_result")) > 0 {
			break
		}
		time.Sleep(50 * time.Millisecond)
	}
	res := c.ofTypes("speedtest_result")
	if len(res) != 1 {
		t.Fatalf("no result")
	}
	down := res[0]["down_bytes"].(float64)
	// 10Mbps × 1s = 1.25MB; 容差 ±50%
	if down < 0.6e6 || down > 1.9e6 {
		t.Fatalf("down_bytes = %v, want ~1.25MB ±50%%", down)
	}
}

func TestResetStopsSession(t *testing.T) {
	c := &capture{}
	p := newTestPlugin(c)
	defer p.Stop()

	ctrl(t, p, `{"t":"start","room":"speedtest_a_b_1","duration":1,"mbps":0}`)
	p.Reset()
	if st := p.Status(); st.Running {
		t.Fatalf("status after reset = %+v", st)
	}
	// 取消后 down 不再上报 result
	ctrl(t, p, `{"t":"down","duration":1,"mbps":0}`)
	time.Sleep(200 * time.Millisecond)
	if n := len(c.ofTypes("speedtest_result")); n != 0 {
		t.Fatalf("result after reset = %d, want 0", n)
	}
}

func TestReconcileNoopDuringSession(t *testing.T) {
	c := &capture{}
	p := newTestPlugin(c)
	defer p.Stop()

	ctrl(t, p, `{"t":"start","room":"speedtest_a_b_1","duration":1,"mbps":0}`)
	if err := p.Reconcile(nil); err != nil {
		t.Fatalf("reconcile: %v", err)
	}
	if st := p.Status(); !st.Running {
		t.Fatalf("reconcile(nil) must not stop session, status=%+v", st)
	}
}

func TestInvalidCtrlNoPanic(t *testing.T) {
	p := newTestPlugin(&capture{})
	defer p.Stop()
	p.HandleMessage("ctrl", []byte(`{bad`))
	p.HandleMessage("ctrl", []byte(`{"t":"down"}`)) // 无 start 的 down 被忽略
	p.HandleMessage("data", []byte("x"))
	if st := p.Status(); st.Running {
		t.Fatalf("status = %+v, want idle", st)
	}
}

func TestDetachCurrentRoom(t *testing.T) {
	c := &capture{}
	p := newTestPlugin(c)
	defer p.Stop()

	ctrl(t, p, `{"t":"start","room":"speedtest_a_b_1","duration":1,"mbps":0}`)
	p.Detach("other_room")
	if st := p.Status(); !st.Running {
		t.Fatal("detach of other room must not stop session")
	}
	p.Detach("speedtest_a_b_1")
	if st := p.Status(); st.Running {
		t.Fatalf("detach current room should stop, status=%+v", st)
	}
}
