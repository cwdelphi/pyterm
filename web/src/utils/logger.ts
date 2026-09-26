export enum LogLevel {
  DEBUG = 0,
  INFO = 1,
  WARN = 2,
  ERROR = 3,
  OFF = 4,
}

const LEVEL_KEY = 'console_log_level'
let _level: LogLevel = loadLevel()

function loadLevel(): LogLevel {
  try {
    const v = localStorage.getItem(LEVEL_KEY)
    if (v !== null) {
      const n = Number(v)
      if (n >= 0 && n <= 4) return n as LogLevel
    }
  } catch {}
  return LogLevel.DEBUG
}

export function getLogLevel(): LogLevel {
  return _level
}

export function setLogLevel(level: LogLevel): void {
  _level = level
  try { localStorage.setItem(LEVEL_KEY, String(level)) } catch {}
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
