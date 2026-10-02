export enum LogLevel {
  DEBUG = 0,
  INFO = 1,
  WARN = 2,
  ERROR = 3,
  OFF = 4,
}

/** 旧版全局键（迁移来源，保留不再写入） */
const LEGACY_KEY = 'console_log_level'

/** 当前登录账户 id：日志等级按账户隔离，同浏览器不同账户各存各的 */
function currentUid(): string {
  try {
    const u = JSON.parse(localStorage.getItem('user') || '{}')
    return u && u.id ? String(u.id) : 'anon'
  } catch {
    return 'anon'
  }
}

function levelKey(): string {
  return `${LEGACY_KEY}:${currentUid()}`
}

let _level: LogLevel = loadLevel()

function loadLevel(): LogLevel {
  try {
    // 1) 本账户已存过 → 直接用
    const scoped = localStorage.getItem(levelKey())
    if (scoped !== null) {
      const n = Number(scoped)
      if (n >= 0 && n <= 4) return n as LogLevel
    }
    // 2) 首次按账户隔离 → 继承旧版全局值（保住既有偏好），旧键不删
    const legacy = localStorage.getItem(LEGACY_KEY)
    if (legacy !== null) {
      const n = Number(legacy)
      if (n >= 0 && n <= 4) {
        try { localStorage.setItem(levelKey(), String(n)) } catch {}
        return n as LogLevel
      }
    }
  } catch {}
  // 缺省降为 INFO：DEBUG 是全量帧级日志，不该是默认值
  return LogLevel.INFO
}

export function getLogLevel(): LogLevel {
  return _level
}

export function setLogLevel(level: LogLevel): void {
  _level = level
  try { localStorage.setItem(levelKey(), String(level)) } catch {}
}

/** 登录/切换账户后调用：按新账户重新读取（模块初始化时已按当时账户读过一次） */
export function reloadLogLevel(): void {
  _level = loadLevel()
}

export function log(level: LogLevel, tag: string, ...args: any[]): void {
  if (level < _level) return
  const prefix = `[${tag}]`
  if (level >= LogLevel.ERROR) {
    console.error(prefix, ...args)
  } else if (level === LogLevel.WARN) {
    console.warn(prefix, ...args)
  } else {
    console.log(prefix, ...args)
  }
}
