package logger

import (
	"bytes"
	"fmt"
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

// 各级别标记：Debugf/Infof/Warnf/Errorf 写入标记，levelWriter 据此过滤。
// 旧的 log.Printf 不带标记，按 Info 处理。
var (
	markDebug = []byte("[DBG] ")
	markInfo  = []byte("[INF] ")
	markWarn  = []byte("[WRN] ")
	markError = []byte("[ERR] ")
)

// levelWriter 过滤低于当前级别的日志。
//
// 修复历史缺陷：
//  1. 原实现把 currentLevel 与 writer 自身的 level 比较，而两者在 Init 时被设为同值，
//     导致 currentLevel > lw.level 恒 false —— 级别过滤完全失效（info 时 debug 全落盘）。
//  2. 一旦 SetLevel("warn")，旧 writer 因 lw.level 仍是初值而 2 > 1 恒 true，
//     连 error 也被一并丢弃。
//
// 现改为按行内级别标记判定：级别不够才丢，且只在真要写日志时才扫描。
type levelWriter struct {
	w io.Writer
}

func (lw *levelWriter) Write(p []byte) (int, error) {
	if detectLevel(p) < atomic.LoadInt32(&currentLevel) {
		return len(p), nil // 静默丢弃
	}
	return lw.w.Write(p)
}

// detectLevel 只在行首 48 字节内找标记，避免消息正文里的同名子串误判。
func detectLevel(p []byte) int32 {
	n := len(p)
	if n > 48 {
		n = 48
	}
	h := p[:n]
	switch {
	case bytes.Contains(h, markError):
		return LevelError
	case bytes.Contains(h, markWarn):
		return LevelWarn
	case bytes.Contains(h, markInfo):
		return LevelInfo
	case bytes.Contains(h, markDebug):
		return LevelDebug
	default:
		return LevelInfo
	}
}

var defaultWriter io.Writer

// Init 初始化日志级别，设置 log.SetOutput
func Init(level string) {
	if defaultWriter == nil {
		defaultWriter = log.Writer()
	}
	atomic.StoreInt32(&currentLevel, parseLevel(level))
	log.SetOutput(&levelWriter{w: defaultWriter})
	// 启动横幅始终输出，不受级别影响
	fmt.Fprintf(defaultWriter, "[LOGGER] 日志级别: %s\n", strings.ToUpper(level))
}

// SetLevel 热切换日志级别
func SetLevel(level string) {
	atomic.StoreInt32(&currentLevel, parseLevel(level))
	log.Printf("[INF] [LOGGER] 日志级别切换为: %s", strings.ToUpper(level))
}

// GetLevel 获取当前日志级别字符串
func GetLevel() string {
	return levelName(atomic.LoadInt32(&currentLevel))
}

func levelName(lv int32) string {
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

// Enabled 判断级别是否放行 —— 供调用点在构造昂贵参数前短路。
func Enabled(lv int32) bool {
	return lv >= atomic.LoadInt32(&currentLevel)
}

// Debugf 级别不够时直接返回，不做格式化（热路径零开销）。
func Debugf(format string, args ...any) {
	if !Enabled(LevelDebug) {
		return
	}
	log.Printf("[DBG] "+format, args...)
}

// Infof 级别不够时直接返回，不做格式化。
func Infof(format string, args ...any) {
	if !Enabled(LevelInfo) {
		return
	}
	log.Printf("[INF] "+format, args...)
}

// Warnf 级别不够时直接返回，不做格式化。
func Warnf(format string, args ...any) {
	if !Enabled(LevelWarn) {
		return
	}
	log.Printf("[WRN] "+format, args...)
}

// Errorf 级别不够时直接返回，不做格式化。
func Errorf(format string, args ...any) {
	if !Enabled(LevelError) {
		return
	}
	log.Printf("[ERR] "+format, args...)
}
