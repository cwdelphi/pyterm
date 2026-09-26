package logger

import (
	"io"
	"log"
	"strings"
	"sync/atomic"
)

// 日志级别常量
const (
	LevelDebug = 0
	LevelInfo  = 1
	LevelWarn  = 2
	LevelError = 3
)

var currentLevel int32 = LevelInfo

// levelWriter 过滤低于指定级别的日志
type levelWriter struct {
	w     io.Writer
	level int32
}

var defaultWriter io.Writer

func (lw *levelWriter) Write(p []byte) (int, error) {
	if atomic.LoadInt32(&currentLevel) > lw.level {
		return len(p), nil // 静默丢弃
	}
	return lw.w.Write(p)
}

// Init 初始化日志级别，设置 log.SetOutput
func Init(level string) {
	if defaultWriter == nil {
		defaultWriter = log.Writer()
	}
	lv := parseLevel(level)
	atomic.StoreInt32(&currentLevel, lv)
	log.SetOutput(&levelWriter{w: defaultWriter, level: lv})
	log.Printf("[LOGGER] 日志级别: %s", strings.ToUpper(level))
}

// SetLevel 热切换日志级别
func SetLevel(level string) {
	lv := parseLevel(level)
	atomic.StoreInt32(&currentLevel, lv)
	log.Printf("[LOGGER] 日志级别切换为: %s", strings.ToUpper(level))
}

// GetLevel 获取当前日志级别字符串
func GetLevel() string {
	lv := atomic.LoadInt32(&currentLevel)
	switch lv {
	case LevelDebug:
		return "debug"
	case LevelInfo:
		return "info"
	case LevelWarn:
		return "warn"
	case LevelError:
		return "error"
	default:
		return "info"
	}
}

func parseLevel(s string) int32 {
	switch strings.ToLower(strings.TrimSpace(s)) {
	case "debug":
		return LevelDebug
	case "info", "":
		return LevelInfo
	case "warn", "warning":
		return LevelWarn
	case "error":
		return LevelError
	default:
		return LevelInfo
	}
}
