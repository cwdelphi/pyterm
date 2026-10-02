package webrtc

import (
	"bytes"
	"encoding/hex"
	"encoding/json"
	"os"
	"testing"
	"time"
)

// 跨语言向量：与 web/src/__tests__/frame.test.ts 共用同一份
// web/src/__tests__/fixtures/sftp-vectors.json，两侧编解码必须逐字节一致。
type testVector struct {
	Name       string `json:"name"`
	Kind       string `json:"kind"`
	FrameType  int    `json:"frameType"`
	PayloadHex string `json:"payloadHex"`
	Op         int    `json:"op"`
	OpName     string `json:"opName"`
	ReqID      uint32 `json:"reqId"`
	MetaJSON   string `json:"metaJson"`
	Idx        int    `json:"idx"`
	Cnt        int    `json:"cnt"`
	RawHex     string `json:"rawHex"`
}

func loadVectors(t *testing.T) []testVector {
	t.Helper()
	b, err := os.ReadFile("../../web/src/__tests__/fixtures/sftp-vectors.json")
	if err != nil {
		t.Skipf("跨语言向量文件缺失: %v", err)
	}
	var doc struct {
		Version int          `json:"version"`
		Vectors []testVector `json:"vectors"`
	}
	if err := json.Unmarshal(b, &doc); err != nil {
		t.Fatalf("解析向量文件失败: %v", err)
	}
	if len(doc.Vectors) == 0 {
		t.Fatal("向量文件为空")
	}
	return doc.Vectors
}

func TestSftpFrameVectors(t *testing.T) {
	for _, v := range loadVectors(t) {
		payload, err := hex.DecodeString(v.PayloadHex)
		if err != nil {
			t.Fatalf("%s: payloadHex 非法: %v", v.Name, err)
		}
		switch v.Kind {
		case "cmd":
			if v.FrameType != int(MsgSFTPRequest) {
				t.Errorf("%s: frameType=%d want %d", v.Name, v.FrameType, MsgSFTPRequest)
			}
			op, reqID, meta, perr := parseSftpCmdFrame(payload)
			if perr != nil {
				t.Fatalf("%s: parse 失败: %v", v.Name, perr)
			}
			if int(op) != v.Op {
				t.Errorf("%s: op=%#x want %#x", v.Name, op, v.Op)
			}
			if reqID != v.ReqID {
				t.Errorf("%s: reqID=%d want %d", v.Name, reqID, v.ReqID)
			}
			if string(meta) != v.MetaJSON {
				t.Errorf("%s: meta=%q want %q", v.Name, meta, v.MetaJSON)
			}
		case "upload":
			if v.FrameType != int(MsgSFTPRequest) {
				t.Errorf("%s: frameType=%d want %d", v.Name, v.FrameType, MsgSFTPRequest)
			}
			reqID, idx, cnt, raw, ok := parseSftpUploadFrame(payload)
			if !ok {
				t.Fatalf("%s: upload 解析失败", v.Name)
			}
			if reqID != v.ReqID || int(idx) != v.Idx || int(cnt) != v.Cnt {
				t.Errorf("%s: reqID/idx/cnt=%d/%d/%d want %d/%d/%d",
					v.Name, reqID, idx, cnt, v.ReqID, v.Idx, v.Cnt)
			}
			want, _ := hex.DecodeString(v.RawHex)
			if !bytes.Equal(raw, want) {
				t.Errorf("%s: raw=%x want %x", v.Name, raw, want)
			}
		case "meta":
			if v.FrameType != int(MsgSFTPResponse) {
				t.Errorf("%s: frameType=%d want %d", v.Name, v.FrameType, MsgSFTPResponse)
			}
			got := buildSftpMetaFrame(v.ReqID, []byte(v.MetaJSON))
			if !bytes.Equal(got, payload) {
				t.Errorf("%s: META 构造不一致\n got=%x\nwant=%x", v.Name, got, payload)
			}
		case "chunk":
			if v.FrameType != int(MsgSFTPResponse) {
				t.Errorf("%s: frameType=%d want %d", v.Name, v.FrameType, MsgSFTPResponse)
			}
			raw, _ := hex.DecodeString(v.RawHex)
			got := buildSftpChunkFrame(v.ReqID, uint16(v.Idx), uint16(v.Cnt), raw)
			if !bytes.Equal(got, payload) {
				t.Errorf("%s: CHUNK 构造不一致\n got=%x\nwant=%x", v.Name, got, payload)
			}
		default:
			t.Errorf("%s: 未知 kind %q", v.Name, v.Kind)
		}
	}
}

func TestSftpSubTypeSymmetry(t *testing.T) {
	if sftpSubCmd != sftpSubMeta {
		t.Errorf("请求/响应 CMD-META 子类型应对齐: %d != %d", sftpSubCmd, sftpSubMeta)
	}
	if sftpSubUpload != sftpSubChunk {
		t.Errorf("请求/响应 UPLOAD-CHUNK 子类型应对齐: %d != %d", sftpSubUpload, sftpSubChunk)
	}
	if sftpSubCmd == sftpSubUpload {
		t.Error("CMD 与 UPLOAD 子类型必须可区分")
	}
}

func TestParseSftpCmdFrameErrors(t *testing.T) {
	// 帧过短（7 字节 < 8）
	if _, _, _, err := parseSftpCmdFrame(make([]byte, 7)); err == nil {
		t.Error("短帧应报错")
	}
	if _, _, _, err := parseSftpCmdFrame(nil); err == nil {
		t.Error("nil 帧应报错")
	}

	// 子类型错误：sub=0x01（UPLOAD 走错分支）
	bad := []byte{0x01, 0x01, 0, 0, 0, 9, 0, 0}
	_, reqID, _, err := parseSftpCmdFrame(bad)
	if err == nil || err.Error() != "SFTP请求帧子类型错误" {
		t.Errorf("子类型错误提示不对: %v", err)
	}
	if reqID != 0 {
		t.Errorf("子类型错误时 reqID 应为 0, got %d", reqID)
	}

	// meta 越界：metaLen=0x0100 但帧尾已到 —— reqID 必须带出来供错误帧使用
	over := []byte{0x00, 0x02, 0, 0, 0, 5, 0x01, 0x00}
	_, reqID, _, err = parseSftpCmdFrame(over)
	if err == nil || err.Error() != "SFTP请求帧 meta 越界" {
		t.Errorf("meta 越界提示不对: %v", err)
	}
	if reqID != 5 {
		t.Errorf("meta 越界时应带回 reqID=5, got %d", reqID)
	}

	// metaLen 恰好收尾：合法且为零长
	ok := []byte{0x00, 0x01, 0, 0, 0, 7, 0, 0}
	op, reqID, meta, err := parseSftpCmdFrame(ok)
	if err != nil {
		t.Fatalf("零长 meta 应合法: %v", err)
	}
	if op != 0x01 || reqID != 7 || len(meta) != 0 {
		t.Errorf("op/reqID/meta=%#x/%d/%v want 0x01/7/[]", op, reqID, meta)
	}
}

func TestParseSftpUploadFrameErrors(t *testing.T) {
	// 帧过短（8 字节 < 9）
	if _, _, _, _, ok := parseSftpUploadFrame(make([]byte, 8)); ok {
		t.Error("短帧应失败")
	}
	// 子类型是 CMD 而非 UPLOAD
	if _, _, _, _, ok := parseSftpUploadFrame(make([]byte, 9)); ok {
		t.Error("子类型不符应失败")
	}
	// 零长 raw 合法
	good := []byte{0x01, 0, 0, 0, 3, 0, 1, 0, 2}
	reqID, idx, cnt, raw, ok := parseSftpUploadFrame(good)
	if !ok || reqID != 3 || idx != 1 || cnt != 2 || len(raw) != 0 {
		t.Errorf("零长 raw 解析失败: ok=%v reqID=%d idx=%d cnt=%d raw=%v", ok, reqID, idx, cnt, raw)
	}
}

func TestSFTPUploadReassembly(t *testing.T) {
	const reqID uint32 = 0x7f000001
	t.Cleanup(func() {
		sftpUploadMu.Lock()
		delete(sftpUploadBuf, reqID)
		delete(sftpUploadN, reqID)
		delete(sftpUploadSeen, reqID)
		sftpUploadMu.Unlock()
	})

	// 只有第 2 片：不应取出
	storeSFTPUpload(reqID, 1, 2, []byte("bbb"))
	if _, ok := takeSFTPUpload(reqID, 2); ok {
		t.Fatal("缺首片不应取出")
	}

	// 补齐第 1 片：乱序入、按序拼
	storeSFTPUpload(reqID, 0, 2, []byte("aaa"))
	got, ok := takeSFTPUpload(reqID, 2)
	if !ok || string(got) != "aaabbb" {
		t.Fatalf("重组结果=%q ok=%v want aaabbb", got, ok)
	}

	// 取出后状态已清
	if _, ok := takeSFTPUpload(reqID, 2); ok {
		t.Error("重复取出应失败")
	}
}

func TestSFTPUploadReassemblyThreeParts(t *testing.T) {
	const reqID uint32 = 0x7f000002
	t.Cleanup(func() {
		sftpUploadMu.Lock()
		delete(sftpUploadBuf, reqID)
		delete(sftpUploadN, reqID)
		delete(sftpUploadSeen, reqID)
		sftpUploadMu.Unlock()
	})

	storeSFTPUpload(reqID, 2, 3, []byte("3"))
	storeSFTPUpload(reqID, 0, 3, []byte("1"))
	// 重复分片：不覆盖已存内容
	storeSFTPUpload(reqID, 0, 3, []byte("X"))
	if _, ok := takeSFTPUpload(reqID, 3); ok {
		t.Fatal("缺第 2 片不应取出")
	}
	storeSFTPUpload(reqID, 1, 3, []byte("2"))
	got, ok := takeSFTPUpload(reqID, 3)
	if !ok || string(got) != "123" {
		t.Fatalf("重组结果=%q ok=%v want 123", got, ok)
	}
}

func TestSFTPUploadRejectsInvalid(t *testing.T) {
	const reqID uint32 = 0x7f000003
	t.Cleanup(func() {
		sftpUploadMu.Lock()
		delete(sftpUploadBuf, reqID)
		delete(sftpUploadN, reqID)
		delete(sftpUploadSeen, reqID)
		sftpUploadMu.Unlock()
	})

	// cnt=0 / idx>=cnt：直接丢弃，不建缓冲
	storeSFTPUpload(reqID, 0, 0, []byte("a"))
	storeSFTPUpload(reqID, 2, 2, []byte("a"))
	storeSFTPUpload(reqID, 1, 1, []byte("a"))
	if _, ok := takeSFTPUpload(reqID, 1); ok {
		t.Error("非法分片不应建立缓冲")
	}
}

// 构造/解析往返：同一组字段造帧再解析，语义必须守恒
func TestSftpFrameRoundTrip(t *testing.T) {
	meta := []byte(`{"path":"/tmp/x","chunks":3}`)
	op, reqID, got, err := parseSftpCmdFrame(buildTestCmdFrame(0x03, 0xdeadbeef, meta))
	if err != nil {
		t.Fatalf("往返解析失败: %v", err)
	}
	if op != 0x03 || reqID != 0xdeadbeef || !bytes.Equal(got, meta) {
		t.Fatalf("往返不守恒: op=%#x reqID=%#x meta=%q", op, reqID, got)
	}
}

func buildTestCmdFrame(op byte, reqID uint32, meta []byte) []byte {
	b := make([]byte, 1+1+4+2+len(meta))
	b[0] = sftpSubCmd
	b[1] = op
	b[2] = byte(reqID >> 24)
	b[3] = byte(reqID >> 16)
	b[4] = byte(reqID >> 8)
	b[5] = byte(reqID)
	b[6] = byte(len(meta) >> 8)
	b[7] = byte(len(meta))
	copy(b[8:], meta)
	return b
}

// G04: 超过 60s 未更新的 reqId 在下一次写入时被清理
func TestSFTPUploadExpiryCleanup(t *testing.T) {
	const staleID uint32 = 0x7f000010
	const freshID uint32 = 0x7f000011
	t.Cleanup(func() {
		sftpUploadMu.Lock()
		for _, id := range []uint32{staleID, freshID} {
			delete(sftpUploadBuf, id)
			delete(sftpUploadN, id)
			delete(sftpUploadSeen, id)
		}
		sftpUploadMu.Unlock()
	})

	// 直接写入过期时间戳，避免 sleep
	sftpUploadMu.Lock()
	sftpUploadBuf[staleID] = map[uint16][]byte{0: []byte("old")}
	sftpUploadN[staleID] = 1
	sftpUploadSeen[staleID] = time.Now().Add(-61 * time.Second)
	sftpUploadMu.Unlock()

	// 任意一次写入都会触发 GC 扫描
	storeSFTPUpload(freshID, 0, 1, []byte("new"))

	sftpUploadMu.Lock()
	_, staleExists := sftpUploadBuf[staleID]
	_, freshExists := sftpUploadBuf[freshID]
	sftpUploadMu.Unlock()

	if staleExists {
		t.Error("过期 60s 的 reqId 应被清理")
	}
	if !freshExists {
		t.Error("新写入的 reqId 不应被清理")
	}
}

// G07: meta/content 按 dcSFTPChunkSize 切分，顺序与 cnt 正确且可无损拼回
func TestSplitSftpChunks(t *testing.T) {
	if got := splitSftpChunks(nil); len(got) != 0 {
		t.Errorf("空输入应返回空序列, got %d", len(got))
	}
	if got := splitSftpChunks([]byte{}); len(got) != 0 {
		t.Errorf("空切片应返回空序列, got %d", len(got))
	}

	cases := []int{1, dcSFTPChunkSize - 1, dcSFTPChunkSize, dcSFTPChunkSize + 1, 70 * 1024, 1024 * 1024}
	for _, size := range cases {
		buf := make([]byte, size)
		for i := range buf {
			buf[i] = byte(i*7 + 3)
		}
		chunks := splitSftpChunks(buf)
		wantCnt := (size + dcSFTPChunkSize - 1) / dcSFTPChunkSize
		if len(chunks) != wantCnt {
			t.Fatalf("size=%d 分片数=%d want %d", size, len(chunks), wantCnt)
		}
		var merged []byte
		for i, c := range chunks {
			if len(c) == 0 || len(c) > dcSFTPChunkSize {
				t.Fatalf("size=%d 第 %d 片长度非法: %d", size, i, len(c))
			}
			merged = append(merged, c...)
			// 每片都必须能按 CHUNK 帧原样回读
			frame := buildSftpChunkFrame(0x0eadbeef, uint16(i), uint16(len(chunks)), c)
			reqID, idx, cnt, raw, ok := parseSftpUploadFrame(frame)
			if !ok || reqID != 0x0eadbeef || int(idx) != i || int(cnt) != len(chunks) || !bytes.Equal(raw, c) {
				t.Fatalf("size=%d 第 %d 片帧回读不一致: ok=%v reqID=%#x idx=%d cnt=%d", size, i, ok, reqID, idx, cnt)
			}
		}
		if !bytes.Equal(merged, buf) {
			t.Fatalf("size=%d 重组结果与原文不等", size)
		}
	}

	// 恰好超 dcSFTPSingleLimit 一倍余：2 片
	meta := make([]byte, dcSFTPSingleLimit+1)
	if got := len(splitSftpChunks(meta)); got != 2 {
		t.Errorf("size=%d 分片数=%d want 2", len(meta), got)
	}
}
