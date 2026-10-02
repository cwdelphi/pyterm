// signal_dc.go — 由 signal.go 按域拆分（R1/R2 纯移动，无逻辑变更）。
package webrtc

import (
	"encoding/json"
	"fmt"
	"log"
	"sync"
	"time"

	"github.com/pion/webrtc/v4"
)

const dcBackpressureThreshold = 64 * 1024 // 64KB

const dcPendingMaxBytes = 256 * 1024 // P1: 背压待发队列上限 256KB

// dcWriteState P1: 背压时暂存高频帧，低水位冲刷；队列满则丢最旧并限频日志
// P3: 终端输出帧带4B BE seq（创建时分配，丢帧留下空洞供浏览器检测）；lastAck 供诊断
type dcWriteState struct {
	mu           sync.Mutex
	sendMu       sync.Mutex // P2 #15: per-DC 发送串行（替代全局 dcSendMu，跨会话不再互斥）
	pending      [][]byte
	pendingBytes int
	dropped      uint64
	lastDropLog  time.Time
	flushing     bool
	txSeq        uint32
	lastAckSeq   uint32
	hasAck       bool
}

var dcWriteStates sync.Map // *webrtc.DataChannel -> *dcWriteState

// dcFramePool P2 #10/#11: 终端/VNC/隧道帧缓冲复用。pion dc.Send 同步拷贝入 SCTP 缓冲，
// 调用方缓冲发送后即可归还；池化后稳态发送路径零分配。
// P4 三方案同口径 pprof A/B（8×10MB SFTP+VNC，alloc_space，总量 1.6GB 噪声内）：
// 单池 4KB 换新 18.94MB(1.15%) 采用；单池 16KB 24.10MB(+27%)；三档分池 34.57MB(+83%,
// 档池 New 64KB 被 GC 后 refill 更贵)。结论：勿盲目加大初始 cap,帧池维持 4KB。
var dcFramePool = sync.Pool{New: func() any { return make([]byte, 0, 4096) }}

// takeFrameBuf 取 n 字节帧缓冲（容量不足则换新并把旧缓冲还回池）
func takeFrameBuf(n int) []byte {
	b, _ := dcFramePool.Get().([]byte)
	if cap(b) >= n {
		return b[:n]
	}
	if cap(b) > 0 {
		dcFramePool.Put(b[:0])
	}
	return make([]byte, n)
}

// releaseFrameBuf 归还帧缓冲；恰好一次（谁 build 谁在终态归还，见 sendDCMsg 各分支注释）
func releaseFrameBuf(b []byte) {
	if cap(b) == 0 {
		return
	}
	dcFramePool.Put(b[:0])
}

// ensureDCWriteState attach 时（DC 建立即）建状态；发送路径一律 dcWriteStateOf（Load）。
// 旧实现发送路径 LoadOrStore：OnClose Delete 后并发发送会重建孤儿态（P2 #18）。
func ensureDCWriteState(dc *webrtc.DataChannel) *dcWriteState {
	st, _ := dcWriteStates.LoadOrStore(dc, &dcWriteState{})
	return st.(*dcWriteState)
}

// dcWriteStateOf 取已 attach 的状态；miss = DC 已关/未挂载 → 调用方直接上抛
func dcWriteStateOf(dc *webrtc.DataChannel) (*dcWriteState, bool) {
	v, ok := dcWriteStates.Load(dc)
	if !ok {
		return nil, false
	}
	return v.(*dcWriteState), true
}

// sendDCMsg 通过 DataChannel 发送带前缀的消息 (API 模式)
// 终端输出 (0x00) 帧格式: [0x00][4B BE seq][payload]
// VNC (0x21) 是连续 RFB 字节流：任何丢帧/乱序都会导致 noVNC zlib 解码失败，
// 因此 VNC 只入队保序、绝不丢弃；背压时由 bridgeVNC 暂停读 TCP 做反压。
func (h *SignalHandler) sendDCMsg(dc *webrtc.DataChannel, prefix byte, data []byte) error {
	var msg []byte
	var st *dcWriteState
	if prefix == MsgTerminal || prefix == MsgVNCData {
		var ok bool
		st, ok = dcWriteStateOf(dc)
		if !ok {
			return fmt.Errorf("datachannel closed")
		}
	}
	if prefix == MsgTerminal {
		st.mu.Lock()
		seq := st.txSeq
		st.txSeq++
		st.mu.Unlock()
		msg = takeFrameBuf(5 + len(data))
		msg[0] = prefix
		msg[1] = byte(seq >> 24)
		msg[2] = byte(seq >> 16)
		msg[3] = byte(seq >> 8)
		msg[4] = byte(seq)
		copy(msg[5:], data)
	} else {
		msg = takeFrameBuf(1 + len(data))
		msg[0] = prefix
		copy(msg[1:], data)
	}
	// 归还约定：直发/丢弃分支本函数内 releaseFrameBuf；入队成功 → flusher 在发送/丢弃/清空时归还。

	if st != nil {
		st.mu.Lock()
		// 队列非空/flush 中/缓冲高：必须入队，禁止直发越过 pending（防乱序）
		needQueue := len(st.pending) > 0 || st.flushing || dc.BufferedAmount() > dcBackpressureThreshold
		if needQueue {
			if prefix == MsgVNCData {
				// VNC 绝不丢：等空间（配合 bridgeVNC 停读 TCP，由 SCTP/TCP 窗口反压）
				for st.pendingBytes+len(msg) > dcPendingMaxBytes {
					st.mu.Unlock()
					if dc.ReadyState() != webrtc.DataChannelStateOpen {
						releaseFrameBuf(msg)
						return fmt.Errorf("datachannel closed")
					}
					time.Sleep(5 * time.Millisecond)
					st.mu.Lock()
				}
				st.pending = append(st.pending, msg)
				st.pendingBytes += len(msg)
				st.mu.Unlock()
				h.scheduleDCFlush(dc, st)
				return nil
			}
			// 终端可丢（有 seq 空洞检测）
			for st.pendingBytes+len(msg) > dcPendingMaxBytes && len(st.pending) > 0 {
				old := st.pending[0]
				st.pending = st.pending[1:]
				st.pendingBytes -= len(old)
				st.dropped++
				releaseFrameBuf(old)
				if time.Since(st.lastDropLog) > 5*time.Second {
					st.lastDropLog = time.Now()
					log.Printf("[A-BRIDGE] backpressure drop prefix=0x%02x total=%d pendingBytes=%d buffered=%d",
						prefix, st.dropped, st.pendingBytes, dc.BufferedAmount())
				}
			}
			if len(msg) > dcPendingMaxBytes {
				st.dropped++
				st.mu.Unlock()
				releaseFrameBuf(msg)
				return nil
			}
			st.pending = append(st.pending, msg)
			st.pendingBytes += len(msg)
			st.mu.Unlock()
			h.scheduleDCFlush(dc, st)
			return nil
		}
		// 空闲直发：持 st.mu，避免与 flush 取批竞态导致乱序；per-DC sendMu 防跨路径并发 Send
		st.sendMu.Lock()
		err := dc.Send(msg)
		st.sendMu.Unlock()
		st.mu.Unlock()
		releaseFrameBuf(msg)
		if err != nil {
			log.Printf("[A-BRIDGE] DC send error prefix=0x%02x len=%d: %v", prefix, len(data), err)
		}
		return err
	}

	// SFTP 响应 (0x11)：批量顺序分片必须背压直发——不进共享 pending 队列
	// （各前缀在前端独立解复用，跨前缀顺序无关，也避免与 VNC/终端丢帧逻辑纠缠）。
	// 实测 427 片在 ~30ms 内全部 dc.Send 会让 BufferedAmount 冲到 ~10MB，
	// 一旦对端 rwnd/UDP 缓冲跟不上即停摆：浏览器收到 5–9MB 后 60s 等不到收尾，
	// 且积压会拖累同连接后续传输（第 2、3 次下载只收到十几帧）。
	if prefix == MsgSFTPResponse {
		err := h.sendSftpFrame(dc, msg)
		releaseFrameBuf(msg)
		return err
	}

	err := sendFrameDC(dc, msg)
	releaseFrameBuf(msg)
	if err != nil {
		log.Printf("[A-BRIDGE] DC send error prefix=0x%02x len=%d: %v", prefix, len(data), err)
	}
	return err
}

// sendFrameDC per-DC sendMu 串行化直发（替代旧全局 dcSendMu）；
// 状态缺失（DC 未挂载/已关）退化为直发，由 pion ensureOpen 返回错误。
func sendFrameDC(dc *webrtc.DataChannel, msg []byte) error {
	if st, ok := dcWriteStateOf(dc); ok {
		st.sendMu.Lock()
		defer st.sendMu.Unlock()
	}
	return dc.Send(msg)
}

// enqueueDCFrame 保留给非 sendDCMsg 路径；VNC 不走丢帧逻辑。
// 接管 msg 所有权：入队成功由 flusher 归还，丢弃/关闭路径当场归还。
func (h *SignalHandler) enqueueDCFrame(dc *webrtc.DataChannel, prefix byte, msg []byte) error {
	st, ok := dcWriteStateOf(dc)
	if !ok {
		releaseFrameBuf(msg)
		return fmt.Errorf("datachannel closed")
	}
	if prefix == MsgVNCData {
		st.mu.Lock()
		for st.pendingBytes+len(msg) > dcPendingMaxBytes {
			st.mu.Unlock()
			if dc.ReadyState() != webrtc.DataChannelStateOpen {
				releaseFrameBuf(msg)
				return fmt.Errorf("datachannel closed")
			}
			time.Sleep(5 * time.Millisecond)
			st.mu.Lock()
		}
		st.pending = append(st.pending, msg)
		st.pendingBytes += len(msg)
		st.mu.Unlock()
		h.scheduleDCFlush(dc, st)
		return nil
	}
	st.mu.Lock()
	defer st.mu.Unlock()
	for st.pendingBytes+len(msg) > dcPendingMaxBytes && len(st.pending) > 0 {
		old := st.pending[0]
		st.pending = st.pending[1:]
		st.pendingBytes -= len(old)
		st.dropped++
		releaseFrameBuf(old)
		if time.Since(st.lastDropLog) > 5*time.Second {
			st.lastDropLog = time.Now()
			log.Printf("[A-BRIDGE] backpressure drop prefix=0x%02x total=%d pendingBytes=%d buffered=%d",
				prefix, st.dropped, st.pendingBytes, dc.BufferedAmount())
		}
	}
	if len(msg) > dcPendingMaxBytes {
		st.dropped++
		releaseFrameBuf(msg)
		return nil
	}
	st.pending = append(st.pending, msg)
	st.pendingBytes += len(msg)
	h.scheduleDCFlush(dc, st)
	return nil
}

// scheduleDCFlush 低水位/短延迟后冲刷待发队列；持续到清空或 DC 关闭（防 40 次后停刷死锁）
func (h *SignalHandler) scheduleDCFlush(dc *webrtc.DataChannel, st *dcWriteState) {
	if st.flushing {
		return
	}
	st.flushing = true
	go func() {
		for {
			time.Sleep(25 * time.Millisecond)
			st.mu.Lock()
			if len(st.pending) == 0 {
				st.flushing = false
				st.mu.Unlock()
				return
			}
			if dc.ReadyState() != webrtc.DataChannelStateOpen {
				for _, x := range st.pending {
					releaseFrameBuf(x)
				}
				st.pending = nil
				st.pendingBytes = 0
				st.flushing = false
				st.mu.Unlock()
				return
			}
			// 缓冲仍高：继续等
			if dc.BufferedAmount() > dcBackpressureThreshold/2 {
				st.mu.Unlock()
				continue
			}
			// 冲刷一批（flushing=true 期间新帧只入队，保序）
			batch := st.pending
			st.pending = nil
			st.pendingBytes = 0
			st.mu.Unlock()
			for i, m := range batch {
				if dc.BufferedAmount() > dcBackpressureThreshold {
					// 又满了：剩余重新入队头部；VNC 帧绝不丢弃
					st.mu.Lock()
					rest := append([][]byte{m}, st.pending...)
					var kept [][]byte
					var bytes int
					for _, x := range rest {
						isVNC := len(x) > 0 && x[0] == MsgVNCData
						if isVNC {
							kept = append(kept, x)
							bytes += len(x)
							continue
						}
						if bytes+len(x) > dcPendingMaxBytes {
							st.dropped++
							releaseFrameBuf(x)
							continue
						}
						kept = append(kept, x)
						bytes += len(x)
					}
					st.pending = kept
					st.pendingBytes = bytes
					st.mu.Unlock()
					// 批内 m 之后未处理的帧同旧实现一样丢弃（保序优先），归还池
					for _, x := range batch[i+1:] {
						releaseFrameBuf(x)
					}
					break
				}
				st.sendMu.Lock()
				_ = dc.Send(m)
				st.sendMu.Unlock()
				releaseFrameBuf(m)
			}
		}
	}()
}

func (h *SignalHandler) sendDCErrorMsg(dc *webrtc.DataChannel, detail string) {
	data, _ := json.Marshal(map[string]interface{}{"type": "error", "detail": detail})
	h.sendDCMsg(dc, MsgError, data)
}

// handleTerminalAck P3: 浏览器回传已收到的最大终端 seq（4B BE）
func (h *SignalHandler) handleTerminalAck(dc *webrtc.DataChannel, payload []byte) {
	if len(payload) < 4 {
		return
	}
	seq := uint32(payload[0])<<24 | uint32(payload[1])<<16 | uint32(payload[2])<<8 | uint32(payload[3])
	st, ok := dcWriteStateOf(dc)
	if !ok {
		return
	}
	st.mu.Lock()
	if !st.hasAck || seq > st.lastAckSeq {
		st.lastAckSeq = seq
		st.hasAck = true
	}
	dropped := st.dropped
	tx := st.txSeq
	st.mu.Unlock()
	// 诊断: ack 落后于 tx 说明有丢帧未被浏览器见过
	if tx > seq+1 && dropped > 0 {
		log.Printf("[A-BRIDGE] terminal seq ack=%d tx=%d dropped=%d (gaps expected if backpressure)", seq, tx, dropped)
	}
}

func (h *SignalHandler) sendDCTerminalData(dc *webrtc.DataChannel, data []byte) {
	h.sendDCMsg(dc, MsgTerminal, data)
}
