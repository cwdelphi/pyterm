/**
 * 协议 v2.0 帧编解码器（单一事实源）
 *
 * 线格式：[1B type][payload...]。type 常量、名称表、编码器集中在此，
 * 其余模块只 import，不再各自 new Uint8Array(1 + n) 手搓帧头。
 */

// ── 帧类型 ────────────────────────────────────────────────
export const MSG_TERMINAL = 0x00
export const MSG_SSH_CONNECT = 0x01
export const MSG_RESIZE = 0x02
export const MSG_ACK = 0x03
export const MSG_SFTP_REQUEST = 0x10
export const MSG_SFTP_RESPONSE = 0x11
export const MSG_VNC_CONNECT = 0x20
export const MSG_VNC_DATA = 0x21
export const MSG_VNC_DISCONNECT = 0x22
export const MSG_VNC_INPUT = 0x23
export const MSG_VNC_RESIZE = 0x24
export const MSG_VNC_CLIPBOARD = 0x25
export const MSG_VNC_ERROR = 0x2f
export const MSG_ERROR = 0xff

/** 帧前缀名称表：模块级常量，避免每条 DC 消息都新建一个对象 */
export const MSG_NAMES: Record<number, string> = {
  [MSG_TERMINAL]: 'TERMINAL',
  [MSG_SSH_CONNECT]: 'SSH_CONNECT',
  [MSG_RESIZE]: 'RESIZE',
  [MSG_ACK]: 'ACK',
  [MSG_SFTP_REQUEST]: 'SFTP_REQ',
  [MSG_SFTP_RESPONSE]: 'SFTP_RESP',
  [MSG_VNC_CONNECT]: 'VNC_CONNECT',
  [MSG_VNC_DATA]: 'VNC_DATA',
  [MSG_VNC_DISCONNECT]: 'VNC_DISCONNECT',
  [MSG_VNC_INPUT]: 'VNC_INPUT',
  [MSG_VNC_RESIZE]: 'VNC_RESIZE',
  [MSG_VNC_CLIPBOARD]: 'VNC_CLIPBOARD',
  [MSG_VNC_ERROR]: 'VNC_ERROR',
  [MSG_ERROR]: 'ERROR',
}

/** 0x00 -> "0x00"，帧类型转十六进制短串 */
export function prefixHex(p: number): string {
  return '0x' + p.toString(16).padStart(2, '0')
}

// ── 编码 ──────────────────────────────────────────────────
export type FramePart = Uint8Array | ArrayBuffer | string

const _enc = new TextEncoder()
const EMPTY = new Uint8Array(0)

/**
 * 构造 [type][payload...] 帧。每次调用分配新 buffer，调用方拥有所有权。
 * 多段拼接（如 [reqId][data]）请用本函数，避免调用点自己两次 new + copy。
 *
 * 字符串按 UTF-8 编码一次（先编码取长度，再一次 memcpy 到目标帧，
 * 不做「先算长度再编码」——那样要为每个字符串各走一遍 TextEncoder 的语义）。
 */
export function frame(type: number, ...parts: FramePart[]): Uint8Array<ArrayBuffer> {
  const n = parts.length
  const u8s: Uint8Array[] = new Array(n)
  let total = 1
  for (let i = 0; i < n; i++) {
    const p = parts[i]
    const u8 = typeof p === 'string' ? _enc.encode(p) : p instanceof Uint8Array ? p : new Uint8Array(p)
    u8s[i] = u8
    total += u8.byteLength
  }
  const out = new Uint8Array(total)
  out[0] = type
  let off = 1
  for (let i = 0; i < n; i++) {
    out.set(u8s[i], off)
    off += u8s[i].byteLength
  }
  return out
}

/**
 * 构造 [type][payload] 帧的单 payload 快捷路径（最热的调用形态）。
 * 与 frame() 语义一致，省掉变长参数的遍历与中间数组。
 */
export function frame1(type: number, payload: Uint8Array): Uint8Array<ArrayBuffer> {
  const out = new Uint8Array(1 + payload.byteLength)
  out[0] = type
  out.set(payload, 1)
  return out
}

/**
 * 复用式 scratch buffer：只在「拿到就同步 send、不跨 await 持有」的热路径使用。
 * 容量不足时按 1.25 倍增长，避免逐帧分配。
 *
 * 典型用例：VNC 键鼠输入、终端 ACK。
 * 禁止用例：需要排队/异步等待发送的帧——那类帧必须是 frame()/frame1() 的独立副本。
 */
export class FrameScratch {
  private buf: Uint8Array<ArrayBuffer> = new Uint8Array(0)

  /** 取 [type][payload] 的可写视图，payload 区可直接写入 */
  prepare(type: number, payloadLen: number): Uint8Array<ArrayBuffer> {
    const need = 1 + payloadLen
    if (this.buf.byteLength < need) {
      this.buf = new Uint8Array(Math.max(need, Math.ceil(need * 1.25)))
    }
    const view = this.buf.subarray(0, need)
    view[0] = type
    return view
  }

  get capacity(): number {
    return this.buf.byteLength
  }
}

// ── 解码 ──────────────────────────────────────────────────
export const HEADER_SIZE = 1

/** 从帧字节读 1B type；长度为 0 时返回 null */
export function frameType(buf: Uint8Array): number | null {
  return buf.byteLength > 0 ? buf[0] : null
}

/** 帧 payload 视图（不拷贝） */
export function framePayload(buf: Uint8Array): Uint8Array {
  return buf.byteLength > 1 ? buf.subarray(1) : EMPTY
}

/** 按 offset 读 4B big-endian 无符号整数；越界返回 null */
export function readU32BE(v: Uint8Array | DataView, offset: number): number | null {
  const dv = v instanceof DataView ? v : new DataView(v.buffer, v.byteOffset, v.byteLength)
  if (offset + 4 > dv.byteLength) return null
  return dv.getUint32(offset, false)
}

/** 按 offset 写 4B big-endian 无符号整数；越界时 DataView 抛错（调用方须先分配好长度） */
export function writeU32BE(v: Uint8Array | DataView, offset: number, value: number): void {
  const dv = v instanceof DataView ? v : new DataView(v.buffer, v.byteOffset, v.byteLength)
  dv.setUint32(offset, value >>> 0, false)
}

/** 安全地为 Uint8Array 建 DataView（不拷贝） */
export function view(u8: Uint8Array): DataView {
  return new DataView(u8.buffer, u8.byteOffset, u8.byteLength)
}

// ── SFTP 二进制帧（0x10 请求 / 0x11 响应，子类型字节 + 4B reqId 计数器） ──
// 编号暂沿旧值 0x10/0x11，子类型由首字节区分；T1 收尾统一重编号时去掉子类型字节即可。

// 分片传输：DataChannel 单条消息上限 65536 字节（pion 与浏览器 SCTP 一致），
// 超限会被 pion 拒绝（outbound packet larger than maximum message size），
// 因此读写请求/响应按 24KB 分片发送，按 req_id 重组（分片帧 [0x10][sub][reqId][idx][cnt][raw]）。
// agent 侧同值常量为 wragent/webrtc/signal.go 的 dcSFTPChunkSize。
export const SFTP_CHUNK_SIZE = 24 * 1024
export const SFTP_SUB_CMD = 0x00 // 0x10 请求：命令
export const SFTP_SUB_UPLOAD = 0x01 // 0x10 请求：上传分片
export const SFTP_SUB_META = 0x00 // 0x11 响应：最终元数据
export const SFTP_SUB_CHUNK = 0x01 // 0x11 响应：内容分片

export const SFTP_OP_LIST = 0x01
export const SFTP_OP_READ = 0x02
export const SFTP_OP_WRITE = 0x03
export const SFTP_OP_MKDIR = 0x04
export const SFTP_OP_RENAME = 0x05
export const SFTP_OP_DELETE = 0x06

export const SFTP_OP_CODE: Record<string, number> = {
  list: SFTP_OP_LIST,
  read: SFTP_OP_READ,
  write: SFTP_OP_WRITE,
  mkdir: SFTP_OP_MKDIR,
  rename: SFTP_OP_RENAME,
  delete: SFTP_OP_DELETE,
}

/** reqId 计数器自增：uint32 回绕（0xFFFFFFFF → 0），4B BE 写入帧内 */
export const nextSftpReqId = (n: number): number => (n + 1) >>> 0

/** [sub=CMD][op:1][reqId:4BE][metaLen:2BE][meta JSON] */
export function sftpCmdFrame(op: number, reqId: number, meta: Uint8Array): Uint8Array<ArrayBuffer> {
  const out = new Uint8Array(1 + 1 + 4 + 2 + meta.byteLength)
  out[0] = SFTP_SUB_CMD
  out[1] = op
  writeU32BE(out, 2, reqId)
  out[6] = (meta.byteLength >>> 8) & 0xff
  out[7] = meta.byteLength & 0xff
  out.set(meta, 8)
  return out
}

/** [sub=UPLOAD][reqId:4BE][idx:2BE][cnt:2BE][raw] */
export function sftpUploadFrame(reqId: number, idx: number, cnt: number, raw: Uint8Array): Uint8Array<ArrayBuffer> {
  const out = new Uint8Array(1 + 4 + 2 + 2 + raw.byteLength)
  out[0] = SFTP_SUB_UPLOAD
  writeU32BE(out, 1, reqId)
  out[5] = (idx >>> 8) & 0xff
  out[6] = idx & 0xff
  out[7] = (cnt >>> 8) & 0xff
  out[8] = cnt & 0xff
  out.set(raw, 9)
  return out
}

/**
 * 组装 0x10 请求的线上帧：[0x10][body]。
 * DC / 网关 WS 发送前必须经此封装——裸 body 会被对端当作前缀字节误路由
 * （首字节 0x00 会命中 MsgTerminal，整帧被当终端输入吞掉）。
 */
export function sftpWireFrame(body: Uint8Array): Uint8Array<ArrayBuffer> {
  return frame1(MSG_SFTP_REQUEST, body)
}

/** 解析 0x11 响应帧头：返回 {sub, reqId, off}；不合法返回 null */
export function parseSftpResp(payload: Uint8Array): { sub: number; reqId: number; off: number } | null {
  if (payload.byteLength < 5) return null
  return { sub: payload[0], reqId: payload[1] * 0x1000000 + (payload[2] << 16) + (payload[3] << 8) + payload[4], off: 5 }
}

/** 解析 0x11 META 帧：返回 meta JSON 字节；越界返回 null */
export function sftpMetaBytes(payload: Uint8Array, off: number): Uint8Array | null {
  if (off + 2 > payload.byteLength) return null
  const metaLen = (payload[off] << 8) | payload[off + 1]
  if (off + 2 + metaLen > payload.byteLength) return null
  return payload.subarray(off + 2, off + 2 + metaLen)
}

/** 解析 0x11 CHUNK 帧：返回 {idx, cnt, raw}；越界返回 null */
export function sftpChunkParts(payload: Uint8Array, off: number): { idx: number; cnt: number; raw: Uint8Array } | null {
  if (off + 4 > payload.byteLength) return null
  const idx = (payload[off] << 8) | payload[off + 1]
  const cnt = (payload[off + 2] << 8) | payload[off + 3]
  return { idx, cnt, raw: payload.subarray(off + 4) }
}
