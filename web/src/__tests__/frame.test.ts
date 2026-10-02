import { describe, it, expect } from 'vitest'
import {
  frame,
  frame1,
  frameType,
  framePayload,
  prefixHex,
  MSG_NAMES,
  HEADER_SIZE,
  readU32BE,
  writeU32BE,
  FrameScratch,
  MSG_SFTP_REQUEST,
  MSG_SFTP_RESPONSE,
  SFTP_SUB_CMD,
  SFTP_SUB_UPLOAD,
  SFTP_SUB_META,
  SFTP_SUB_CHUNK,
  SFTP_OP_CODE,
  SFTP_OP_LIST,
  SFTP_CHUNK_SIZE,
  sftpCmdFrame,
  sftpUploadFrame,
  sftpWireFrame,
  parseSftpResp,
  sftpMetaBytes,
  sftpChunkParts,
  nextSftpReqId,
} from '../utils/frame'
import fixture from './fixtures/sftp-vectors.json'

interface Vector {
  name: string
  kind: string
  frameType: number
  payloadHex: string
  op?: number
  opName?: string
  reqId: number
  metaJson?: string
  idx?: number
  cnt?: number
  rawHex?: string
}

const vecs = fixture.vectors as unknown as Vector[]

const hex = (u8: Uint8Array): string =>
  Array.from(u8, (b) => b.toString(16).padStart(2, '0')).join('')

const bytes = (h: string): Uint8Array =>
  h.length === 0
    ? new Uint8Array(0)
    : new Uint8Array(h.match(/.{2}/g)!.map((x) => parseInt(x, 16)))

const utf8 = (s: string): Uint8Array => new TextEncoder().encode(s)

const byKind = (k: string): Vector[] => vecs.filter((v) => v.kind === k)

describe('frame 编码', () => {
  it('单段 payload：帧头 + payload', () => {
    const f = frame(0x00, utf8('abc'))
    expect(f.byteLength).toBe(4)
    expect(f[0]).toBe(0x00)
    expect(new TextDecoder().decode(f.subarray(1))).toBe('abc')
  })

  it('多段拼接顺序保持，支持 string/Uint8Array/ArrayBuffer', () => {
    const raw = new Uint8Array([0xaa, 0xbb])
    const f = frame(0x10, utf8('xy'), raw, raw.buffer.slice(0))
    expect(hex(f)).toBe('10' + hex(utf8('xy')) + 'aabb' + 'aabb')
  })

  it('UTF-8 中文多字节只编码一次', () => {
    const f = frame(0x01, '中文')
    expect(hex(f)).toBe('01' + 'e4b8ade69687')
    expect(new TextDecoder().decode(f.subarray(1))).toBe('中文')
  })

  it('空 payload 只有帧头', () => {
    expect(frame(0xff).byteLength).toBe(HEADER_SIZE)
    expect(frame(0xff, new Uint8Array(0)).byteLength).toBe(HEADER_SIZE)
  })

  it('frame1 与 frame 结果一致', () => {
    const p = utf8('same')
    expect(hex(frame1(0x21, p))).toBe(hex(frame(0x21, p)))
  })

  it('frameType / framePayload 边界', () => {
    expect(frameType(new Uint8Array(0))).toBeNull()
    expect(frameType(new Uint8Array([0x11]))).toBe(0x11)
    expect(framePayload(new Uint8Array(0)).byteLength).toBe(0)
    expect(framePayload(new Uint8Array([0x11])).byteLength).toBe(0)
    const f = new Uint8Array([0x11, 1, 2, 3])
    expect(framePayload(f)).toBeInstanceOf(Uint8Array)
    expect(hex(framePayload(f))).toBe('010203')
  })

  it('prefixHex 与 MSG_NAMES 覆盖全部已定义帧类型', () => {
    expect(prefixHex(0)).toBe('0x00')
    expect(prefixHex(0x0f)).toBe('0x0f')
    expect(prefixHex(0x11)).toBe('0x11')
    for (const p of [MSG_SFTP_REQUEST, MSG_SFTP_RESPONSE]) {
      expect(MSG_NAMES[p]).toBeTruthy()
    }
    expect(MSG_NAMES[0x99]).toBeUndefined()
  })
})

describe('u32 BE 读写', () => {
  it('写后读一致且为大端', () => {
    const buf = new Uint8Array(8)
    writeU32BE(buf, 0, 0x01020304)
    expect(hex(buf.subarray(0, 4))).toBe('01020304')
    expect(readU32BE(buf, 0)).toBe(0x01020304)
    writeU32BE(buf, 4, 1)
    expect(hex(buf.subarray(4, 8))).toBe('00000001')
    expect(readU32BE(buf, 4)).toBe(1)
  })

  it('Uint8Array 与 DataView 两条路径等价（含非零 byteOffset）', () => {
    const host = new Uint8Array(16)
    const view = host.subarray(4, 12)
    writeU32BE(view, 0, 0xffffffff)
    writeU32BE(view, 4, 1)
    expect(hex(host.subarray(4, 12))).toBe('ffffffff00000001')
    expect(readU32BE(new DataView(host.buffer, 4, 8), 0)).toBe(0xffffffff)
    expect(readU32BE(view, 0)).toBe(0xffffffff)
    expect(readU32BE(view, 4)).toBe(1)
    expect(readU32BE(view, 5)).toBeNull()
  })

  it('越界读返回 null', () => {
    const buf = new Uint8Array(3)
    expect(readU32BE(buf, 0)).toBeNull()
    expect(readU32BE(buf, 1)).toBeNull()
    expect(readU32BE(new Uint8Array(4), 4)).toBeNull()
  })
})

describe('FrameScratch', () => {
  it('prepare 返回 [type][payload] 可写视图', () => {
    const s = new FrameScratch()
    const v = s.prepare(0x23, 4)
    expect(v.byteLength).toBe(5)
    expect(v[0]).toBe(0x23)
    v.set([9, 9, 9, 9], 1)
    expect(hex(v)).toBe('2309090909')
    expect(s.capacity).toBeGreaterThanOrEqual(5)
  })

  it('容量足够时复用同一 buffer', () => {
    const s = new FrameScratch()
    const first = s.prepare(0x03, 8)
    const cap = s.capacity
    const second = s.prepare(0x03, 4)
    expect(s.capacity).toBe(cap)
    expect(second.buffer).toBe(first.buffer)
    expect(second.byteLength).toBe(5)
  })

  it('容量不足时按 1.25 倍增长且满足需求', () => {
    const s = new FrameScratch()
    s.prepare(0x00, 1)
    const cap = s.capacity
    const need = 1 + 10000
    s.prepare(0x00, 10000)
    expect(s.capacity).toBeGreaterThanOrEqual(need)
    expect(s.capacity).toBeGreaterThanOrEqual(Math.ceil(cap * 1.25))
  })

  it('视图长度精确，不暴露多余容量', () => {
    const s = new FrameScratch()
    s.prepare(0x00, 100)
    const v = s.prepare(0x00, 3)
    expect(v.byteLength).toBe(4)
    expect(s.capacity).toBeGreaterThanOrEqual(101)
  })
})

describe('SFTP 与 pyagent 跨语言向量对齐', () => {
  it('向量集非空且 kind 全部已知', () => {
    expect(vecs.length).toBeGreaterThan(0)
    for (const v of vecs) {
      expect(['cmd', 'upload', 'meta', 'chunk']).toContain(v.kind)
      expect(v.frameType).toBe(v.kind === 'meta' || v.kind === 'chunk'
        ? MSG_SFTP_RESPONSE
        : MSG_SFTP_REQUEST)
    }
  })

  it('请求/响应子类型常量对称（同编号，由方向区分）', () => {
    expect(SFTP_SUB_CMD).toBe(SFTP_SUB_META)
    expect(SFTP_SUB_UPLOAD).toBe(SFTP_SUB_CHUNK)
    expect(SFTP_SUB_CMD).not.toBe(SFTP_SUB_UPLOAD)
  })

  it('sftpWireFrame 补齐 0x10 前缀且 body 逐字节保留', () => {
    // 回归：裸 body 直发 DC 时首字节是 sub=0x00（MsgTerminal），整帧被对端当终端输入吞掉
    const cmd = sftpCmdFrame(SFTP_OP_LIST, 1, utf8('{"path":"/"}'))
    const wireCmd = sftpWireFrame(cmd)
    expect(wireCmd.byteLength).toBe(cmd.byteLength + 1)
    expect(wireCmd[0]).toBe(MSG_SFTP_REQUEST)
    expect(hex(wireCmd)).toBe('10' + hex(cmd))

    const upload = sftpUploadFrame(7, 0, 2, bytes('deadbeef'))
    const wireUpload = sftpWireFrame(upload)
    expect(wireUpload[0]).toBe(MSG_SFTP_REQUEST)
    expect(hex(wireUpload)).toBe('10' + hex(upload))
    // 剥掉前缀后仍是可解析的 CMD/UPLOAD 帧体
    expect(hex(wireCmd.subarray(1))).toBe(hex(cmd))
  })

  for (const v of byKind('cmd')) {
    it(`CMD 编码 = 向量 ${v.name}`, () => {
      const op = v.op
      const metaJson = v.metaJson
      expect(op).toBeTypeOf('number')
      expect(metaJson).toBeTypeOf('string')
      if (op === undefined || metaJson === undefined) return
      expect(SFTP_OP_CODE[v.opName ?? '']).toBe(op)
      const body = sftpCmdFrame(op, v.reqId, utf8(metaJson))
      expect(hex(body)).toBe(v.payloadHex)
      expect(hex(frame(MSG_SFTP_REQUEST, body))).toBe('10' + v.payloadHex)
      expect(hex(frame1(MSG_SFTP_REQUEST, body))).toBe('10' + v.payloadHex)
    })
  }

  for (const v of byKind('upload')) {
    it(`UPLOAD 编码 = 向量 ${v.name}`, () => {
      const idx = v.idx
      const cnt = v.cnt
      const rawHex = v.rawHex
      expect(idx).toBeTypeOf('number')
      expect(cnt).toBeTypeOf('number')
      expect(rawHex).toBeTypeOf('string')
      if (idx === undefined || cnt === undefined || rawHex === undefined) return
      const body = sftpUploadFrame(v.reqId, idx, cnt, bytes(rawHex))
      expect(hex(body)).toBe(v.payloadHex)
      expect(hex(frame(MSG_SFTP_REQUEST, body))).toBe('10' + v.payloadHex)
    })
  }

  for (const v of byKind('meta')) {
    it(`META 响应可解析 = 向量 ${v.name}`, () => {
      const metaJson = v.metaJson
      expect(metaJson).toBeTypeOf('string')
      if (metaJson === undefined) return
      const payload = bytes(v.payloadHex)
      const head = parseSftpResp(payload)
      expect(head).not.toBeNull()
      if (!head) return
      expect(head.sub).toBe(SFTP_SUB_META)
      expect(head.reqId).toBe(v.reqId)
      expect(head.off).toBe(5)
      const meta = sftpMetaBytes(payload, head.off)
      expect(meta).not.toBeNull()
      if (!meta) return
      expect(new TextDecoder().decode(meta)).toBe(metaJson)
      expect(hex(frame(MSG_SFTP_RESPONSE, payload))).toBe('11' + v.payloadHex)
    })
  }

  for (const v of byKind('chunk')) {
    it(`CHUNK 响应可解析 = 向量 ${v.name}`, () => {
      const idx = v.idx
      const cnt = v.cnt
      const rawHex = v.rawHex
      expect(idx).toBeTypeOf('number')
      expect(cnt).toBeTypeOf('number')
      expect(rawHex).toBeTypeOf('string')
      if (idx === undefined || cnt === undefined || rawHex === undefined) return
      const payload = bytes(v.payloadHex)
      const head = parseSftpResp(payload)
      expect(head).not.toBeNull()
      if (!head) return
      expect(head.sub).toBe(SFTP_SUB_CHUNK)
      expect(head.reqId).toBe(v.reqId)
      const parts = sftpChunkParts(payload, head.off)
      expect(parts).not.toBeNull()
      if (!parts) return
      expect(parts.idx).toBe(idx)
      expect(parts.cnt).toBe(cnt)
      expect(hex(parts.raw)).toBe(rawHex)
    })
  }
})

describe('SFTP 响应解析容错', () => {
  it('帧头不足 5 字节返回 null', () => {
    expect(parseSftpResp(new Uint8Array(0))).toBeNull()
    expect(parseSftpResp(new Uint8Array(4))).toBeNull()
    expect(parseSftpResp(new Uint8Array(5))).not.toBeNull()
  })

  it('META metaLen 越界返回 null', () => {
    // sub=0, reqId=1, metaLen=0x00ff，但实际只剩 1 字节
    const p = bytes('000000000100ff78')
    expect(sftpMetaBytes(p, 5)).toBeNull()
    expect(sftpMetaBytes(p, 6)).toBeNull()
    expect(sftpMetaBytes(p, 99)).toBeNull()
  })

  it('META metaLen 恰好收尾返回零长视图', () => {
    const p = bytes('00000000010000')
    const m = sftpMetaBytes(p, 5)
    expect(m).not.toBeNull()
    expect(m?.byteLength).toBe(0)
  })

  it('CHUNK 偏移越界返回 null', () => {
    const p = new Uint8Array(8)
    expect(sftpChunkParts(p, 5)).toBeNull()
    expect(sftpChunkParts(p, 99)).toBeNull()
    expect(sftpChunkParts(new Uint8Array(9), 5)).not.toBeNull()
  })

  it('CHUNK raw 为零拷贝视图', () => {
    const p = bytes('010000000100000001aabb')
    const parts = sftpChunkParts(p, 5)
    expect(parts).not.toBeNull()
    if (!parts) return
    expect(parts.raw.buffer).toBe(p.buffer)
    expect(hex(parts.raw)).toBe('aabb')
  })
})

describe('SFTP 边界与重组（F03/F06/F07/F09/F10）', () => {
  it('F03: 上传分片 uint32/uint16 边界字段原样落帧', () => {
    const raw = new Uint8Array(64 * 1024)
    const body = sftpUploadFrame(0xffffffff, 0xffff, 0xffff, raw)
    expect(body.byteLength).toBe(1 + 4 + 2 + 2 + 65536)
    expect(body[0]).toBe(SFTP_SUB_UPLOAD)
    expect(readU32BE(body, 1)).toBe(0xffffffff)
    expect((body[5] << 8) | body[6]).toBe(0xffff)
    expect((body[7] << 8) | body[8]).toBe(0xffff)
    expect(body.subarray(9).byteLength).toBe(65536)
  })

  it('F06: reqId uint32 回绕 0xFFFFFFFF → 0，编解码两侧无损', () => {
    expect(nextSftpReqId(0xffffffff)).toBe(0)
    expect(nextSftpReqId(0)).toBe(1)
    expect(nextSftpReqId(0x7ffffffe)).toBe(0x7fffffff)

    const hi = sftpCmdFrame(SFTP_OP_LIST, 0xffffffff, new Uint8Array(0))
    const lo = sftpCmdFrame(SFTP_OP_LIST, 0, new Uint8Array(0))
    expect(readU32BE(hi, 2)).toBe(0xffffffff)
    expect(readU32BE(lo, 2)).toBe(0)

    // 响应侧 reqId 解码用 *0x1000000 而非 <<24，避免高位溢出为负数
    const resp = new Uint8Array(5)
    resp[0] = SFTP_SUB_META
    writeU32BE(resp, 1, 0xffffffff)
    expect(parseSftpResp(resp)?.reqId).toBe(0xffffffff)
    const resp0 = new Uint8Array(5)
    resp0[0] = SFTP_SUB_META
    writeU32BE(resp0, 1, 0)
    expect(parseSftpResp(resp0)?.reqId).toBe(0)
  })

  it('F07: SFTP_OP_CODE 恰好 6 个 op 且数值固定', () => {
    const expected: Record<string, number> = {
      list: 0x01,
      read: 0x02,
      write: 0x03,
      mkdir: 0x04,
      rename: 0x05,
      delete: 0x06,
    }
    expect(Object.keys(SFTP_OP_CODE).sort()).toEqual(Object.keys(expected).sort())
    for (const [name, op] of Object.entries(expected)) {
      expect(SFTP_OP_CODE[name]).toBe(op)
    }
    expect(SFTP_OP_CODE['stat']).toBeUndefined()
  })

  it('F09: 0 字节 UPLOAD 与零长 CHUNK 不炸', () => {
    const body = sftpUploadFrame(3, 0, 1, new Uint8Array(0))
    expect(body.byteLength).toBe(9)
    expect(hex(body)).toBe('010000000300000001')

    const payload = bytes('010000000100000001')
    const parts = sftpChunkParts(payload, 5)
    expect(parts).not.toBeNull()
    if (!parts) return
    expect(parts.idx).toBe(0)
    expect(parts.cnt).toBe(1)
    expect(parts.raw.byteLength).toBe(0)
  })

  it('F10: 1MB 按 SFTP_CHUNK_SIZE 分片后可无损重组', () => {
    const SIZE = 1024 * 1024
    const content = new Uint8Array(SIZE)
    for (let i = 0; i < SIZE; i++) content[i] = (i * 31 + 7) & 0xff

    const cnt = Math.ceil(SIZE / SFTP_CHUNK_SIZE)
    expect(SFTP_CHUNK_SIZE).toBe(24 * 1024)
    expect(cnt).toBe(43)

    const parts: Uint8Array[] = []
    for (let i = 0; i < cnt; i++) {
      const s = i * SFTP_CHUNK_SIZE
      const raw = content.subarray(s, Math.min(s + SFTP_CHUNK_SIZE, SIZE))
      const body = sftpUploadFrame(9, i, cnt, raw)
      expect(body.byteLength).toBe(9 + raw.byteLength)
      expect(readU32BE(body, 1)).toBe(9)
      expect((body[5] << 8) | body[6]).toBe(i)
      expect((body[7] << 8) | body[8]).toBe(cnt)
      parts.push(body.subarray(9))
    }

    const merged = new Uint8Array(SIZE)
    let off = 0
    for (const p of parts) {
      merged.set(p, off)
      off += p.byteLength
    }
    expect(off).toBe(SIZE)
    expect(hex(merged.subarray(0, 16))).toBe(hex(content.subarray(0, 16)))
    expect(hex(merged.subarray(SIZE - 16))).toBe(hex(content.subarray(SIZE - 16)))
    expect(merged.every((b, i) => b === content[i])).toBe(true)
  })
})
