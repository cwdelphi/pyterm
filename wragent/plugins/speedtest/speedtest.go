// Package speedtest Agent↔Agent 测速插件（接收端，阶段S）。
//
// DataChannel 帧格式（label "speedtest"）：
//
//	0x60 + JSON ctrl（start/down 等阶段控制）
//	0x61 + 裸数据负载
//
// 本插件实现 plugin.Plugin（Name="speedtest"，非配置驱动，Reconcile 恒空操作）
// 与 plugin.MessageHandler（panic 由 Manager.Dispatch 隔离）：
//   - 阶段1 上行：计字节 + 500ms 限频 progress 上报
//   - 阶段2 下行：按限速档发送（背压 BufferedAmount<64KB），结束后上报 result
package speedtest

import (
	"encoding/json"
	"fmt"
	"math"
	"sync"
	"time"

	"github.com/ppy-tools/wragent/plugin"
)

const (
	frameCtrl      = 0x60
	frameData      = 0x61
	chunkSize      = 16 * 1024
	progressPeriod = 500 * time.Millisecond
)

// Deps 核心注入（signal 层构造）
type Deps struct {
	// SendSignal 上报 speedtest_progress / speedtest_result 到 md（完整 JSON 含 type/room_id）
	SendSignal func(payload []byte) error
	// Logf 日志（nil 用 fmt.Printf）
	Logf func(format string, args ...any)
}

// Plugin 测速接收端状态机
type Plugin struct {
	mu   sync.Mutex
	deps Deps

	roomID   string
	send     func([]byte) error
	buffered func() uint64

	phase     string // ""/"up"/"down"
	mbps      int
	upBytes   uint64
	downBytes uint64
	tickBase  uint64 // 本 phase 上次 tick 时的字节基线
	tickAt    time.Time
	stopTick  chan struct{}

	running bool
}

// New 创建 speedtest 插件
func New(deps Deps) *Plugin {
	if deps.Logf == nil {
		deps.Logf = func(format string, args ...any) {
			fmt.Printf(format+"\n", args...)
		}
	}
	return &Plugin{deps: deps}
}

// Name 插件键（不来自服务端配置，仅作 Dispatch 寻址）
func (p *Plugin) Name() string { return "speedtest" }

// Reconcile 非配置驱动插件：恒空操作（配置热更新不得打断进行中的测速会话）
func (p *Plugin) Reconcile(cfg json.RawMessage) error { return nil }

// Stop 优雅停止（进程退出/会话取消）：停进度 tick 与下行发送
func (p *Plugin) Stop() error {
	p.mu.Lock()
	p.stopLocked()
	p.mu.Unlock()
	return nil
}

// Status 只读状态
func (p *Plugin) Status() plugin.Status {
	p.mu.Lock()
	defer p.mu.Unlock()
	if !p.running {
		return plugin.Status{}
	}
	detail := fmt.Sprintf("room=%s phase=%s", p.roomID, p.phase)
	return plugin.Status{Running: true, Detail: detail}
}

// AttachSender DC 打开时由 signal 层注入发送能力（帧已含 0x60/0x61 前缀由本插件组装）
func (p *Plugin) AttachSender(roomID string, send func([]byte) error, buffered func() uint64) {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.roomID = roomID
	p.send = send
	p.buffered = buffered
}

// Detach DC 关闭：若仍是当前会话则停止 tick/发送并复位
func (p *Plugin) Detach(roomID string) {
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.roomID == roomID {
		p.stopLocked()
	}
}

// Reset 取消/停止指令（speedtest_stop）：无论房间直接复位
func (p *Plugin) Reset() {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.stopLocked()
}

// HandleMessage plugin.MessageHandler 实现（Manager.Dispatch 调用，panic 隔离）
func (p *Plugin) HandleMessage(kind string, payload []byte) {
	p.mu.Lock()
	defer p.mu.Unlock()
	switch kind {
	case "ctrl":
		p.handleCtrlLocked(payload)
	case "data":
		if p.phase == "up" {
			p.upBytes += uint64(len(payload))
		}
	}
}

// handleCtrlLocked 解析 0x60 ctrl 帧（持锁调用）
func (p *Plugin) handleCtrlLocked(payload []byte) {
	var ctrl struct {
		T        string `json:"t"`
		Room     string `json:"room"`
		Duration int    `json:"duration"`
		MBPS     int    `json:"mbps"`
	}
	if err := json.Unmarshal(payload, &ctrl); err != nil {
		p.deps.Logf("[SPEEDTEST] ctrl解析失败: %v", err)
		return
	}
	switch ctrl.T {
	case "start":
		p.stopLocked() // 防御：重复 start 先清旧会话
		if ctrl.Room != "" {
			p.roomID = ctrl.Room
		}
		p.phase = "up"
		p.mbps = ctrl.MBPS
		p.upBytes, p.downBytes = 0, 0
		p.tickBase = 0
		p.tickAt = time.Now()
		p.running = true
		p.stopTick = make(chan struct{})
		go p.progressLoop(p.stopTick)
		p.deps.Logf("[SPEEDTEST] 会话开始 room=%s duration=%ds limit=%dMbps", p.roomID, ctrl.Duration, ctrl.MBPS)
	case "down":
		if p.phase != "up" {
			p.deps.Logf("[SPEEDTEST] 忽略非法 down (phase=%s)", p.phase)
			return
		}
		p.phase = "down"
		p.tickBase = 0
		p.tickAt = time.Now()
		if ctrl.Duration <= 0 {
			ctrl.Duration = 10
		}
		if p.send == nil {
			p.deps.Logf("[SPEEDTEST] 下行无发送通道, 上报失败结果")
			p.reportResultLocked(ctrl.Duration)
			return
		}
		go p.runDown(ctrl.Duration, p.mbps)
	}
}

// runDown 阶段2 下行发送（持锁外运行，结束上报 result）
func (p *Plugin) runDown(durationSec, mbps int) {
	deadline := time.Now().Add(time.Duration(durationSec) * time.Second)
	frame := make([]byte, 1+chunkSize)
	frame[0] = frameData

	for time.Now().Before(deadline) {
		p.mu.Lock()
		send, buffered, active := p.send, p.buffered, p.phase == "down"
		p.mu.Unlock()
		if !active || send == nil {
			return
		}
		// 背压：缓冲堆积则等待
		if buffered != nil {
			for buffered() > 64*1024 && time.Now().Before(deadline) {
				time.Sleep(10 * time.Millisecond)
			}
		}
		if err := send(frame); err != nil {
			p.deps.Logf("[SPEEDTEST] 下行发送失败: %v", err)
			return
		}
		p.mu.Lock()
		p.downBytes += uint64(chunkSize)
		p.mu.Unlock()
		// 限速档：0=无限制；按帧大小换算发送间隔
		if mbps > 0 {
			interval := time.Duration(float64(chunkSize) / (float64(mbps) * 1e6 / 8) * float64(time.Second))
			if interval > 0 {
				time.Sleep(interval)
			}
		}
	}

	p.mu.Lock()
	if p.phase == "down" { // 取消/复位后不再上报
		p.reportResultLocked(durationSec)
	}
	p.mu.Unlock()
}

// reportResultLocked 计算并上报 result（持锁）
func (p *Plugin) reportResultLocked(durationSec int) {
	if durationSec <= 0 {
		durationSec = 10
	}
	upMbps := float64(p.upBytes) * 8 / float64(durationSec) / 1e6
	downMbps := float64(p.downBytes) * 8 / float64(durationSec) / 1e6
	payload := map[string]any{
		"type":       "speedtest_result",
		"room_id":    p.roomID,
		"up_mbps":    math.Round(upMbps*100) / 100,
		"down_mbps":  math.Round(downMbps*100) / 100,
		"up_bytes":   p.upBytes,
		"down_bytes": p.downBytes,
		"duration":   durationSec,
	}
	p.emitLocked(payload)
	p.deps.Logf("[SPEEDTEST] 完成 room=%s up=%.1fMbps down=%.1fMbps", p.roomID, upMbps, downMbps)
	// 会话结束：停 tick，保留 running=false
	p.stopTickLocked()
	p.phase = ""
	p.running = false
}

// progressLoop 500ms 聚合上报（≤2Hz）
func (p *Plugin) progressLoop(stop <-chan struct{}) {
	ticker := time.NewTicker(progressPeriod)
	defer ticker.Stop()
	for {
		select {
		case <-stop:
			return
		case <-ticker.C:
			p.mu.Lock()
			if p.phase == "" {
				p.mu.Unlock()
				return
			}
			var cur uint64
			if p.phase == "up" {
				cur = p.upBytes
			} else {
				cur = p.downBytes
			}
			delta := cur - p.tickBase
			p.tickBase = cur
			elapsed := time.Since(p.tickAt).Seconds()
			if elapsed <= 0 {
				elapsed = 0.5
			}
			p.tickAt = time.Now()
			mbps := float64(delta) * 8 / elapsed / 1e6
			payload := map[string]any{
				"type":    "speedtest_progress",
				"room_id": p.roomID,
				"phase":   p.phase,
				"mbps":    math.Round(mbps*100) / 100,
				"bytes":   cur,
			}
			p.emitLocked(payload)
			p.mu.Unlock()
		}
	}
}

// emitLocked 经 SendSignal 发送（持锁，SendSignal nil 则仅日志）
func (p *Plugin) emitLocked(payload map[string]any) {
	raw, err := json.Marshal(payload)
	if err != nil {
		return
	}
	if p.deps.SendSignal == nil {
		return
	}
	if err := p.deps.SendSignal(raw); err != nil {
		p.deps.Logf("[SPEEDTEST] 上报失败: %v", err)
	}
}

// stopLocked 停 tick/发送并复位会话（持锁）
func (p *Plugin) stopLocked() {
	p.stopTickLocked()
	p.phase = ""
	p.running = false
}

func (p *Plugin) stopTickLocked() {
	if p.stopTick != nil {
		close(p.stopTick)
		p.stopTick = nil
	}
}
