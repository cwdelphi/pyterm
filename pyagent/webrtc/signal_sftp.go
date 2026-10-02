// signal_sftp.go — 由 signal.go 按域拆分（R1/R2 纯移动，无逻辑变更）。
package webrtc

import (
	"encoding/binary"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log"
	"sync"
	"time"

	"github.com/pion/webrtc/v4"
	"github.com/pkg/sftp"
	gossh "golang.org/x/crypto/ssh"
)

// SFTP 出站背压上限：BufferedAmount 是"已写入未被对端确认"的字节（ack 口径），
// 硬卡 64KB 会把 WAN 吞吐压到 64KB/RTT，故取 256KB（LAN 无感，WAN 256KB/RTT
// ≈ 8.5MB/s@30ms）；关键收益是杜绝 427 片 30ms 内灌出 ~10MB 的病态队列。
const dcSFTPMaxOutstanding = 256 * 1024

// sendSftpFrame SFTP 全帧背压直发（帧须已含 0x11 前缀）：256KB outstanding 软限 + 4 次退避重试。
// 调用方负责 releaseFrameBuf（pion Send 同步拷贝，返回后缓冲即可归还）。
func (h *SignalHandler) sendSftpFrame(dc *webrtc.DataChannel, frame []byte) error {
	deadline := time.Now().Add(30 * time.Second)
	for dc.BufferedAmount() > dcSFTPMaxOutstanding {
		if dc.ReadyState() != webrtc.DataChannelStateOpen {
			return fmt.Errorf("datachannel closed")
		}
		if time.Now().After(deadline) {
			return fmt.Errorf("send backpressure timeout (buffered=%d)", dc.BufferedAmount())
		}
		time.Sleep(500 * time.Microsecond)
	}
	var err error
	for attempt := 0; attempt < 4; attempt++ {
		if dc.ReadyState() != webrtc.DataChannelStateOpen {
			return fmt.Errorf("datachannel closed")
		}
		if err = sendFrameDC(dc, frame); err == nil {
			return nil
		}
		// DC 仍 Open 的失败按瞬时处理（如缓冲满），退避后重试；
		// 真正关闭时 ensureOpen 每轮都会命中，快速上抛
		time.Sleep(time.Duration(attempt+1) * 5 * time.Millisecond)
	}
	log.Printf("[A-BRIDGE] DC send error prefix=0x%02x len=%d: %v", MsgSFTPResponse, len(frame), err)
	return err
}

// ── DC 消息分片 ──────────────────────────────────────────────
// WebRTC DataChannel 单条消息上限 65536 字节（pion 与浏览器 SCTP 一致），
// 超过即被 pion 拒绝（outbound packet larger than maximum message size），
// 因此 SFTP 读写响应/请求超过阈值时按分片发送，接收端按 req_id 重组。
const dcSFTPSingleLimit = 32 * 1024 // 小于该值直接单条发送

const dcSFTPChunkSize = 24 * 1024 // 分片原始字节数（base64 后 32KB + 信封 < 64KB）

// T1.3: SFTP 二进制帧（编号暂沿 0x10/0x11，子类型首字节区分）
// 0x10 请求: [sub=CMD][op:1][reqId:4BE][metaLen:2BE][meta JSON]
//
//	[sub=UPLOAD][reqId:4BE][idx:2BE][cnt:2BE][raw]
//
// 0x11 响应: [sub=META][reqId:4BE][metaLen:2BE][meta JSON]
//
//	[sub=CHUNK][reqId:4BE][idx:2BE][cnt:2BE][raw]
const (
	sftpSubCmd    byte = 0x00
	sftpSubUpload byte = 0x01
)

const (
	sftpSubMeta  byte = 0x00
	sftpSubChunk byte = 0x01
)

const (
	sftpOpList   byte = 0x01
	sftpOpRead   byte = 0x02
	sftpOpWrite  byte = 0x03
	sftpOpMkdir  byte = 0x04
	sftpOpRename byte = 0x05
	sftpOpDelete byte = 0x06
	sftpOpStat   byte = 0x07
)

// 上传分片重组缓冲：uint32 reqId
var (
	sftpUploadMu    sync.Mutex
	sftpUploadBuf   = map[uint32]map[uint16][]byte{}
	sftpUploadN     = map[uint32]uint16{}
	sftpUploadSeen  = map[uint32]time.Time{}
	sftpUploadBytes int64 // 当前重组缓冲总字节（sftpUploadMu 保护）
)

// dropSFTPUploadLocked 立即回收一次上传的全部分片（调用方须已持 sftpUploadMu）
func dropSFTPUploadLocked(reqID uint32) {
	if parts, ok := sftpUploadBuf[reqID]; ok {
		for _, b := range parts {
			sftpUploadBytes -= int64(len(b))
		}
	}
	delete(sftpUploadBuf, reqID)
	delete(sftpUploadN, reqID)
	delete(sftpUploadSeen, reqID)
	if sftpUploadBytes < 0 {
		sftpUploadBytes = 0
	}
}

// sftpUploadTTL 重组缓冲的存活时间（超时未完成的上传即视为已放弃）
const sftpUploadTTL = 60 * time.Second

// sftpUploadSweeper 重组缓冲的独立清扫器。
// 原实现只在“下一分片到达时”顺带扫 TTL：一旦上传中途放弃（断连/取消），
// 就再也不会有新分片，整份文件体积的分片永久驻留 —— 这是确定性的内存泄露。
var sftpUploadSweeperOnce sync.Once

func ensureSFTPUploadSweeper() {
	sftpUploadSweeperOnce.Do(func() {
		go func() {
			t := time.NewTicker(15 * time.Second)
			defer t.Stop()
			for range t.C {
				sweepSFTPUploads(time.Now())
			}
		}()
	})
}

func sweepSFTPUploads(now time.Time) (removed int) {
	sftpUploadMu.Lock()
	defer sftpUploadMu.Unlock()
	return sweepSFTPUploadsLocked(now)
}

func sweepSFTPUploadsLocked(now time.Time) (removed int) {
	for id, ts := range sftpUploadSeen {
		if now.Sub(ts) > sftpUploadTTL {
			dropSFTPUploadLocked(id)
			removed++
		}
	}
	return removed
}

// sftpUploadMaxBytes 全局重组字节上限（软上限）：超限时先强制清扫，
// 仍超则拒绝本分片 —— 让该次上传以“分片不完整”失败，而不是把进程内存打爆。
const sftpUploadMaxBytes = 256 << 20

// storeSFTPUpload 存储上传分片；DC 有序，分片必先于 CMD 到达
func storeSFTPUpload(reqID uint32, idx, cnt uint16, raw []byte) {
	if cnt <= 0 || idx >= cnt {
		return
	}
	ensureSFTPUploadSweeper()
	now := time.Now()
	sftpUploadMu.Lock()
	defer sftpUploadMu.Unlock()
	for id, ts := range sftpUploadSeen {
		if now.Sub(ts) > sftpUploadTTL {
			dropSFTPUploadLocked(id)
		}
	}
	if sftpUploadBytes+int64(len(raw)) > sftpUploadMaxBytes {
		if removed := sweepSFTPUploadsLocked(now); removed > 0 {
			log.Printf("[SFTP] 重组缓冲超限, 清理 %d 个过期上传", removed)
		}
		if sftpUploadBytes+int64(len(raw)) > sftpUploadMaxBytes {
			log.Printf("[SFTP] 重组缓冲仍超 %d 字节, 拒绝分片 reqID=%d idx=%d", sftpUploadBytes, reqID, idx)
			return
		}
	}
	parts, ok := sftpUploadBuf[reqID]
	if !ok {
		parts = map[uint16][]byte{}
		sftpUploadBuf[reqID] = parts
		sftpUploadN[reqID] = cnt
	}
	if _, dup := parts[idx]; dup {
		return
	}
	parts[idx] = raw
	sftpUploadSeen[reqID] = now
	sftpUploadBytes += int64(len(raw))
}

// takeSFTPUpload 取出并拼装完整上传内容
func takeSFTPUpload(reqID uint32, cnt uint16) ([]byte, bool) {
	sftpUploadMu.Lock()
	defer sftpUploadMu.Unlock()
	// 分片不全时只返回 false、不回收：重组的最终回收由 TTL 清扫器负责
	// （独立 15s ticker，不依赖“下一分片到达”），避免中途 take 误删有效数据。
	parts, ok := sftpUploadBuf[reqID]
	if !ok || sftpUploadN[reqID] != cnt || len(parts) != int(cnt) {
		return nil, false
	}
	var total int
	for i := uint16(0); i < cnt; i++ {
		p, ok2 := parts[i]
		if !ok2 {
			return nil, false
		}
		total += len(p)
	}
	out := make([]byte, 0, total)
	for i := uint16(0); i < cnt; i++ {
		out = append(out, parts[i]...)
	}
	dropSFTPUploadLocked(reqID)
	return out, true
}

// ── SFTP 二进制帧编解码（本文件内单一事实源，单测见 sftp_frame_test.go）──
// web 侧同构实现见 web/src/utils/frame.ts；两侧以
// web/src/__tests__/fixtures/sftp-vectors.json 逐字节交叉校验。

// parseSftpCmdFrame 解析 0x10 CMD 请求帧体：[sub][op:1][reqId:4BE][metaLen:2BE][meta JSON]。
// 出错时一并返回已解析出的 reqID（帧过短/子类型错误时为 0），供上层回错误帧。
func parseSftpCmdFrame(payload []byte) (op byte, reqID uint32, meta []byte, err error) {
	if len(payload) < 1+1+4+2 {
		return 0, 0, nil, errors.New("SFTP请求帧过短")
	}
	if payload[0] != sftpSubCmd {
		return 0, 0, nil, errors.New("SFTP请求帧子类型错误")
	}
	op = payload[1]
	reqID = binary.BigEndian.Uint32(payload[2:])
	metaLen := int(binary.BigEndian.Uint16(payload[6:]))
	if 8+metaLen > len(payload) {
		return op, reqID, nil, errors.New("SFTP请求帧 meta 越界")
	}
	return op, reqID, payload[8 : 8+metaLen], nil
}

// parseSftpUploadFrame 解析 0x10 UPLOAD 分片帧体：[sub][reqId:4BE][idx:2BE][cnt:2BE][raw]
func parseSftpUploadFrame(payload []byte) (reqID uint32, idx, cnt uint16, raw []byte, ok bool) {
	if len(payload) < 1+4+2+2 || payload[0] != sftpSubUpload {
		return 0, 0, 0, nil, false
	}
	return binary.BigEndian.Uint32(payload[1:]),
		binary.BigEndian.Uint16(payload[5:]),
		binary.BigEndian.Uint16(payload[7:]),
		payload[9:], true
}

// sftpMetaFrameSize / sftpChunkFrameSize 含 0x11 前缀的全帧长
func sftpMetaFrameSize(metaLen int) int { return 1 + 1 + 4 + 2 + metaLen }

func sftpChunkFrameSize(rawLen int) int { return 1 + 1 + 4 + 2 + 2 + rawLen }

// buildSftpMetaFrameInto 在 dst 上构造含 0x11 前缀的 META 全帧（dst cap 须 ≥ sftpMetaFrameSize）
// 线上字节与旧「帧体+sendDCMsg 补前缀」完全一致，仅省一次 make+copy。
func buildSftpMetaFrameInto(dst []byte, reqID uint32, meta []byte) []byte {
	dst = dst[:sftpMetaFrameSize(len(meta))]
	dst[0] = MsgSFTPResponse
	dst[1] = sftpSubMeta
	binary.BigEndian.PutUint32(dst[2:], reqID)
	binary.BigEndian.PutUint16(dst[6:], uint16(len(meta)))
	copy(dst[8:], meta)
	return dst
}

// buildSftpChunkFrameInto 在 dst 上构造含 0x11 前缀的 CHUNK 全帧（dst cap 须 ≥ sftpChunkFrameSize）
func buildSftpChunkFrameInto(dst []byte, reqID uint32, idx, cnt uint16, raw []byte) []byte {
	dst = dst[:sftpChunkFrameSize(len(raw))]
	dst[0] = MsgSFTPResponse
	dst[1] = sftpSubChunk
	binary.BigEndian.PutUint32(dst[2:], reqID)
	binary.BigEndian.PutUint16(dst[6:], idx)
	binary.BigEndian.PutUint16(dst[8:], cnt)
	copy(dst[10:], raw)
	return dst
}

// buildSftpMetaFrame 构造 0x11 META 响应帧体（不含前缀，测试/跨语言向量比对用）：[sub][reqId:4BE][metaLen:2BE][meta JSON]
func buildSftpMetaFrame(reqID uint32, meta []byte) []byte {
	return buildSftpMetaFrameInto(make([]byte, 0, sftpMetaFrameSize(len(meta))), reqID, meta)[1:]
}

// buildSftpChunkFrame 构造 0x11 CHUNK 响应帧体（不含前缀）：[sub][reqId:4BE][idx:2BE][cnt:2BE][raw]
func buildSftpChunkFrame(reqID uint32, idx, cnt uint16, raw []byte) []byte {
	return buildSftpChunkFrameInto(make([]byte, 0, sftpChunkFrameSize(len(raw))), reqID, idx, cnt, raw)[1:]
}

// sendSftpMeta 发送 META 响应帧（0x11）；帧缓冲池化，send 返回后归还
func (h *SignalHandler) sendSftpMeta(dc *webrtc.DataChannel, reqID uint32, meta []byte) error {
	frame := buildSftpMetaFrameInto(takeFrameBuf(sftpMetaFrameSize(len(meta))), reqID, meta)
	err := h.sendSftpFrame(dc, frame)
	releaseFrameBuf(frame)
	return err
}

// sendSftpChunk 发送 CHUNK 响应帧（0x11）；帧缓冲池化，send 返回后归还
func (h *SignalHandler) sendSftpChunk(dc *webrtc.DataChannel, reqID uint32, idx, cnt uint16, raw []byte) error {
	frame := buildSftpChunkFrameInto(takeFrameBuf(sftpChunkFrameSize(len(raw))), reqID, idx, cnt, raw)
	err := h.sendSftpFrame(dc, frame)
	releaseFrameBuf(frame)
	return err
}

// sendSftpResponse 发送响应：content==nil → 单 META（超限按 CHUNK 分片）；否则 CHUNK+收尾 META
func (h *SignalHandler) sendSftpResponse(dc *webrtc.DataChannel, reqID uint32, resp map[string]interface{}, content []byte) {
	if content == nil {
		meta, _ := json.Marshal(resp)
		if len(meta) > dcSFTPSingleLimit {
			h.sendChunkedMeta(dc, reqID, meta)
			return
		}
		h.sendSftpMeta(dc, reqID, meta)
		return
	}
	chunks := splitSftpChunks(content)
	cnt := uint16(len(chunks))
	start := time.Now()
	for i, part := range chunks {
		if err := h.sendSftpChunk(dc, reqID, uint16(i), cnt, part); err != nil {
			log.Printf("[A-BRIDGE] SFTP chunk %d/%d send error req=%d: %v", i+1, cnt, reqID, err)
			// 发错误收尾 META：前端 finishSftp 见 ok:false 立即 reject，
			// 否则会等满 30s 超时（探针视角是 60s 无下载事件的"挂死"）
			resp["ok"] = false
			resp["detail"] = fmt.Sprintf("传输中断于 %d/%d 片: %v", i+1, cnt, err)
			if m, e := json.Marshal(resp); e == nil {
				h.sendSftpMeta(dc, reqID, m)
			}
			return
		}
	}
	resp["chunks"] = int(cnt)
	meta, _ := json.Marshal(resp)
	h.sendSftpMeta(dc, reqID, meta)
	log.Printf("[A-BRIDGE] SFTP content sent req=%d size=%d chunks=%d elapsed=%dms buffered=%d",
		reqID, len(content), cnt, time.Since(start).Milliseconds(), dc.BufferedAmount())
}

// sendSftpFileStream read 流式下发（P2 #7）：按 stat size 预分片，边读边发，
// 全程复用一块 24KB 读缓冲（旧 io.ReadAll 让 RSS 与文件同大）。
// 前端 finishSftp 收到 META 即收尾，片数不足会整单返回空内容 → 实际长度短于
// stat size 时剩余片发空帧补足 cnt（空 Uint8Array 计入 got，拼接结果=实际字节）。
func (h *SignalHandler) sendSftpFileStream(dc *webrtc.DataChannel, reqID uint32, r io.Reader, size int64, resp map[string]interface{}) {
	const maxCnt = int64(65535)
	cnt64 := (size + dcSFTPChunkSize - 1) / dcSFTPChunkSize
	if cnt64 > maxCnt || cnt64 <= 0 {
		// 旧实现 uint16(cnt) 对 >1.5GB 静默回绕 → 前端按 0 片收尾得空文件；改为显式报错
		if m, e := json.Marshal(map[string]interface{}{
			"type": "sftp", "op": "read", "path": resp["path"], "ok": false,
			"detail": fmt.Sprintf("文件超出流式传输上限 (size=%d)", size),
		}); e == nil {
			h.sendSftpMeta(dc, reqID, m)
		}
		return
	}
	cnt := uint16(cnt64)
	start := time.Now()
	buf := make([]byte, dcSFTPChunkSize)
	var sent int
	failAt := func(pos int, cause error) {
		resp["ok"] = false
		resp["detail"] = fmt.Sprintf("传输中断于 %d/%d 片: %v", pos, cnt, cause)
		if m, e := json.Marshal(resp); e == nil {
			h.sendSftpMeta(dc, reqID, m)
		}
	}
	for i := uint16(0); i < cnt; i++ {
		n, rerr := io.ReadFull(r, buf)
		if rerr != nil && rerr != io.EOF && rerr != io.ErrUnexpectedEOF {
			failAt(int(i)+1, rerr)
			return
		}
		if err := h.sendSftpChunk(dc, reqID, i, cnt, buf[:n]); err != nil {
			failAt(int(i)+1, err)
			return
		}
		sent += n
		if rerr == io.EOF || rerr == io.ErrUnexpectedEOF {
			// 文件短于 stat size：补空片收齐 cnt，防止前端 got<cnt 整单丢弃
			for j := i + 1; j < cnt; j++ {
				if err := h.sendSftpChunk(dc, reqID, j, cnt, nil); err != nil {
					failAt(int(j)+1, err)
					return
				}
			}
			break
		}
	}
	resp["chunks"] = int(cnt)
	meta, _ := json.Marshal(resp)
	h.sendSftpMeta(dc, reqID, meta)
	log.Printf("[A-BRIDGE] SFTP content streamed req=%d size=%d chunks=%d sent=%d elapsed=%dms buffered=%d",
		reqID, size, cnt, sent, time.Since(start).Milliseconds(), dc.BufferedAmount())
}

// splitSftpChunks 按 dcSFTPChunkSize 顺序切分，返回与发送序号一一对应的分片。
// 空输入返回空序列（对应 cnt=0，调用方不发任何 CHUNK）。单测见 sftp_frame_test.go。
func splitSftpChunks(buf []byte) [][]byte {
	if len(buf) == 0 {
		return nil
	}
	cnt := (len(buf) + dcSFTPChunkSize - 1) / dcSFTPChunkSize
	out := make([][]byte, 0, cnt)
	for i := 0; i < cnt; i++ {
		s := i * dcSFTPChunkSize
		e := s + dcSFTPChunkSize
		if e > len(buf) {
			e = len(buf)
		}
		out = append(out, buf[s:e])
	}
	return out
}

// sendChunkedMeta 超大 META（罕见，如巨型目录列表）：meta 字节按 CHUNK 发送 + 收尾 META 带 chunks
func (h *SignalHandler) sendChunkedMeta(dc *webrtc.DataChannel, reqID uint32, meta []byte) {
	chunks := splitSftpChunks(meta)
	cnt := uint16(len(chunks))
	for i, part := range chunks {
		if err := h.sendSftpChunk(dc, reqID, uint16(i), cnt, part); err != nil {
			log.Printf("[A-BRIDGE] SFTP meta chunk send error req=%d: %v", reqID, err)
			// 同 sendSftpResponse：错误收尾 META 防前端挂等到超时
			if m, e := json.Marshal(map[string]interface{}{
				"type": "sftp", "ok": false,
				"detail": fmt.Sprintf("目录列表传输中断于 %d/%d 片: %v", i+1, cnt, err),
			}); e == nil {
				h.sendSftpMeta(dc, reqID, m)
			}
			return
		}
	}
	finalMeta, _ := json.Marshal(map[string]interface{}{"ok": true, "chunks": int(cnt)})
	h.sendSftpMeta(dc, reqID, finalMeta)
}

type SftpItem struct {
	Name  string `json:"name"`
	Size  int64  `json:"size"`
	IsDir bool   `json:"is_dir"`
	Mode  string `json:"mode"`
	Mtime string `json:"mtime"`
}

func (h *SignalHandler) sendDCSftpResponse(dc *webrtc.DataChannel, reqID uint32, op, path string, items []SftpItem, content []byte) {
	resp := map[string]interface{}{
		"type": "sftp",
		"op":   op,
		"path": path,
		"ok":   true,
	}
	if items != nil {
		resp["items"] = items
	}
	h.sendSftpResponse(dc, reqID, resp, content)
}

func (h *SignalHandler) sendDCSftpOk(dc *webrtc.DataChannel, reqID uint32, op string) {
	resp := map[string]interface{}{
		"type": "sftp",
		"op":   op,
		"ok":   true,
	}
	h.sendSftpResponse(dc, reqID, resp, nil)
}

func (h *SignalHandler) sendDCSftpError(dc *webrtc.DataChannel, reqID uint32, detail string) {
	resp := map[string]interface{}{
		"type":   "sftp",
		"ok":     false,
		"detail": detail,
	}
	h.sendSftpResponse(dc, reqID, resp, nil)
}

func (h *SignalHandler) handleSFTPData(dc *webrtc.DataChannel, payload []byte, sshConn *gossh.Client) {
	op, reqID, meta, err := parseSftpCmdFrame(payload)
	if err != nil {
		h.sendDCSftpError(dc, reqID, err.Error())
		return
	}

	var req struct {
		Path    string `json:"path"`
		OldPath string `json:"old_path"`
		NewName string `json:"new_name"`
		Chunks  int    `json:"chunks"`
	}
	if err := json.Unmarshal(meta, &req); err != nil {
		h.sendDCSftpError(dc, reqID, "SFTP请求meta解析失败: "+err.Error())
		return
	}

	// 写入内容：上传分片（[sub=UPLOAD]）已先于 CMD 到达，按 reqID 重组
	var content []byte
	if op == sftpOpWrite && req.Chunks > 0 {
		raw, ok := takeSFTPUpload(reqID, uint16(req.Chunks))
		if !ok {
			h.sendDCSftpError(dc, reqID, fmt.Sprintf("上传分片不完整 (期望 %d 片)", req.Chunks))
			return
		}
		content = raw
	}

	client, err := sftp.NewClient(sshConn)
	if err != nil {
		h.sendDCSftpError(dc, reqID, "SFTP连接失败: "+err.Error())
		return
	}
	defer client.Close()

	switch op {
	case sftpOpList:
		entries, err := client.ReadDir(req.Path)
		if err != nil {
			h.sendDCSftpError(dc, reqID, "读取目录失败: "+err.Error())
			return
		}
		items := make([]SftpItem, 0, len(entries))
		for _, e := range entries {
			items = append(items, SftpItem{
				Name:  e.Name(),
				Size:  e.Size(),
				IsDir: e.IsDir(),
				Mode:  e.Mode().String(),
				Mtime: e.ModTime().Format("2006-01-02 15:04:05"),
			})
		}
		h.sendDCSftpResponse(dc, reqID, "list", req.Path, items, nil)

	case sftpOpRead:
		f, err := client.Open(req.Path)
		if err != nil {
			h.sendDCSftpError(dc, reqID, "读取文件失败: "+err.Error())
			return
		}
		fi, serr := f.Stat()
		if serr == nil && fi.Size() > 0 {
			// P2 #7: 流式下发（边读边发，内存峰值 1×24KB）；旧实现 io.ReadAll 全量驻留。
			// stat 不可靠/空文件（procfs 等 size=0）回落整读，行为与旧版一致。
			resp := map[string]interface{}{"type": "sftp", "op": "read", "path": req.Path, "ok": true}
			h.sendSftpFileStream(dc, reqID, f, fi.Size(), resp)
			f.Close()
			return
		}
		data, err := io.ReadAll(f)
		f.Close()
		if err != nil {
			h.sendDCSftpError(dc, reqID, "读取内容失败: "+err.Error())
			return
		}
		h.sendDCSftpResponse(dc, reqID, "read", req.Path, nil, data)

	case sftpOpWrite:
		f, err := client.Create(req.Path)
		if err != nil {
			h.sendDCSftpError(dc, reqID, "创建文件失败: "+err.Error())
			return
		}
		_, err = f.Write(content)
		f.Close()
		if err != nil {
			h.sendDCSftpError(dc, reqID, "写入文件失败: "+err.Error())
			return
		}
		h.sendDCSftpOk(dc, reqID, "write")

	case sftpOpMkdir:
		if err := client.MkdirAll(req.Path); err != nil {
			h.sendDCSftpError(dc, reqID, "创建目录失败: "+err.Error())
			return
		}
		h.sendDCSftpOk(dc, reqID, "mkdir")

	case sftpOpDelete:
		if err := client.Remove(req.Path); err != nil {
			h.sendDCSftpError(dc, reqID, "删除失败: "+err.Error())
			return
		}
		h.sendDCSftpOk(dc, reqID, "delete")

	case sftpOpRename:
		if err := client.Rename(req.OldPath, req.NewName); err != nil {
			h.sendDCSftpError(dc, reqID, "重命名失败: "+err.Error())
			return
		}
		h.sendDCSftpOk(dc, reqID, "rename")

	case sftpOpStat:
		_, err := client.Stat(req.Path)
		if err != nil {
			h.sendDCSftpError(dc, reqID, "获取状态失败: "+err.Error())
			return
		}
		h.sendDCSftpOk(dc, reqID, "stat")

	default:
		h.sendDCSftpError(dc, reqID, "未知操作")
	}
}
