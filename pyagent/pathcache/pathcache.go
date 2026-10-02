// Package pathcache 记录 ICE 建连成功时胜出的本端接口（P2 路径缓存，方案 §4.5）。
// L1 快路径只放行上次胜出的接口；键为对端标识，if_hash 变化即整体失效（回落 L2）。
package pathcache

import (
	"encoding/json"
	"log"
	"os"
	"path/filepath"
	"sort"
	"sync"
	"time"
)

// Entry 一条路径缓存
type Entry struct {
	Key        string `json:"key"`
	LocalIface string `json:"local_iface"`
	IfHash     string `json:"if_hash"`
	ConnType   string `json:"conn_type,omitempty"`
	RTTMs      int64  `json:"rtt_ms,omitempty"`
	Hits       int    `json:"hits,omitempty"`
	UpdatedAt  int64  `json:"updated_at,omitempty"`
}

// DefaultTTL 缓存有效期（方案 §4.5：30 分钟）
const DefaultTTL = 30 * time.Minute

// CacheKey Agent 侧缓存键：本机只有一条「最佳路径」，不按对端区分。
const CacheKey = "default"

var (
	enabledMu sync.RWMutex
	enabled   = true
)

// SetEnabled 后台 config_json.ice.path_cache（缺省开）。关闭后既不记录也不查询。
func SetEnabled(on bool) {
	enabledMu.Lock()
	enabled = on
	enabledMu.Unlock()
}

// Enabled 路径缓存开关
func Enabled() bool {
	enabledMu.RLock()
	defer enabledMu.RUnlock()
	return enabled
}

// fileName 落盘文件名（与 config.json 同目录）
const fileName = "ice_cache.json"

type fileState struct {
	Entries []Entry `json:"entries"`
}

// Cache 进程内路径缓存；dir 为空则纯内存（目录不可写时自动降级）。
type Cache struct {
	mu      sync.Mutex
	dir     string
	ttl     time.Duration
	entries map[string]Entry
	// P2 #12: 脏标记 + 5s 批量落盘（旧实现 Lookup/Record 每次全量 marshal+WriteFile，
	// 持锁阻塞 ICE 建连关键路径；进程退出最多丢一个窗口，缓存本就可再生）
	dirty    bool
	flushing bool
	lastSave time.Time
}

// New 创建一个独立缓存（单测用）；dir 为空表示纯内存。
func New(dir string) *Cache {
	return NewTTL(dir, DefaultTTL)
}

// NewTTL 创建指定 TTL 的缓存（单测过期逻辑用）；dir 为空表示纯内存。
func NewTTL(dir string, ttl time.Duration) *Cache {
	c := &Cache{
		dir:     dir,
		ttl:     ttl,
		entries: map[string]Entry{},
	}
	c.load()
	return c
}

var (
	gmu   sync.Mutex
	gdir  string
	ginst *Cache
)

// SetDir 设定默认实例的落盘目录（须在 Default() 前调用；已有实例则切目录并重载）
func SetDir(dir string) {
	gmu.Lock()
	defer gmu.Unlock()
	gdir = dir
	if ginst != nil {
		ginst.mu.Lock()
		if ginst.dir != dir {
			ginst.dir = dir
			ginst.entries = map[string]Entry{}
			ginst.load()
		}
		ginst.mu.Unlock()
	}
}

// Default 进程级单例
func Default() *Cache {
	gmu.Lock()
	defer gmu.Unlock()
	if ginst == nil {
		ginst = New(gdir)
	}
	return ginst
}

// Lookup 取缓存；要求未过期且 if_hash 一致（传空跳过 hash 校验）。
// 命中会累加 Hits 并落盘。
func (c *Cache) Lookup(key, ifHash string) (Entry, bool) {
	c.mu.Lock()
	defer c.mu.Unlock()
	e, ok := c.entries[key]
	if !ok {
		return Entry{}, false
	}
	if c.ttl > 0 && e.UpdatedAt > 0 && time.Since(time.Unix(e.UpdatedAt, 0)) > c.ttl {
		delete(c.entries, key)
		c.save()
		return Entry{}, false
	}
	if ifHash != "" && e.IfHash != "" && e.IfHash != ifHash {
		delete(c.entries, key)
		c.save()
		return Entry{}, false
	}
	e.Hits++
	c.entries[key] = e
	c.save()
	return e, true
}

// Record 记录一次建连成功胜出的本端接口
func (c *Cache) Record(key, iface, ifHash, connType string, rtt time.Duration) {
	if key == "" || iface == "" {
		return
	}
	entry := Entry{
		Key:        key,
		LocalIface: iface,
		IfHash:     ifHash,
		ConnType:   connType,
		UpdatedAt:  time.Now().Unix(),
	}
	if rtt > 0 {
		entry.RTTMs = rtt.Milliseconds()
	}
	c.mu.Lock()
	if old, ok := c.entries[key]; ok && old.IfHash == ifHash {
		entry.Hits = old.Hits + 1
	}
	c.entries[key] = entry
	c.save()
	c.mu.Unlock()
}

// InvalidateIfHash 清掉 if_hash 不一致的条目（接口变化即回落 L2）。
// 返回被清除的条数。
func (c *Cache) InvalidateIfHash(ifHash string) int {
	if ifHash == "" {
		return 0
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	n := 0
	for k, e := range c.entries {
		if e.IfHash != "" && e.IfHash != ifHash {
			delete(c.entries, k)
			n++
		}
	}
	if n > 0 {
		c.save()
	}
	return n
}

// Clear 清空全部缓存
func (c *Cache) Clear() {
	c.mu.Lock()
	defer c.mu.Unlock()
	if len(c.entries) == 0 {
		return
	}
	c.entries = map[string]Entry{}
	c.save()
}

// Entries 按更新时间倒序返回全部条目（看板用）
func (c *Cache) Entries() []Entry {
	c.mu.Lock()
	defer c.mu.Unlock()
	out := make([]Entry, 0, len(c.entries))
	for _, e := range c.entries {
		out = append(out, e)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].UpdatedAt > out[j].UpdatedAt })
	return out
}

// Len 有效条目数
func (c *Cache) Len() int {
	c.mu.Lock()
	defer c.mu.Unlock()
	return len(c.entries)
}

func (c *Cache) path() string {
	if c.dir == "" {
		return ""
	}
	return filepath.Join(c.dir, fileName)
}

func (c *Cache) load() {
	p := c.path()
	if p == "" {
		return
	}
	data, err := os.ReadFile(p)
	if err != nil {
		return
	}
	var st fileState
	if err := json.Unmarshal(data, &st); err != nil {
		log.Printf("[PATHCACHE] 载入失败 %s: %v", p, err)
		return
	}
	for _, e := range st.Entries {
		if e.Key != "" && e.LocalIface != "" {
			c.entries[e.Key] = e
		}
	}
	if len(c.entries) > 0 {
		log.Printf("[PATHCACHE] 已载入 %d 条缓存 from %s", len(c.entries), p)
	}
}

// saveInterval 落盘节流窗口（P2 #12）
const saveInterval = 5 * time.Second

// save 标脏并按 5s 窗口批量落盘（调用方须持 c.mu）；dir 为空（纯内存/单测）直接跳过。
// 首次变更同步落盘（进程内一次，避免测试/关停时异步写与目录清理竞态），其余变更异步并入窗口。
func (c *Cache) save() {
	path := c.path()
	if path == "" {
		return
	}
	c.dirty = true
	if c.flushing {
		return
	}
	if c.lastSave.IsZero() {
		st := c.snapshotLocked()
		c.dirty = false
		c.lastSave = time.Now()
		writeTo(path, st)
		return
	}
	c.flushing = true
	delay := saveInterval - time.Since(c.lastSave)
	if delay < 0 {
		delay = 0
	}
	go func() {
		if delay > 0 {
			time.Sleep(delay)
		}
		c.flush(path)
	}()
}

// flush 快照标脏内容 → 锁外 marshal+写盘；写盘期间的变更继续标脏，写完再排下一轮窗口。
// flushing 在写盘完成后才清除，保证 Flush() 排空等待时文件已就绪。
// 落盘前复核 path（SetDir 切目录时放弃写入，避免把新目录内容写进旧路径）。
func (c *Cache) flush(path string) {
	c.mu.Lock()
	if path != c.path() || !c.dirty {
		c.flushing = false
		c.mu.Unlock()
		return
	}
	st := c.snapshotLocked()
	c.dirty = false
	c.lastSave = time.Now()
	c.mu.Unlock()
	writeTo(path, st)
	c.mu.Lock()
	if c.dirty {
		// 写盘期间又有变更：flushing 保持在途，5s 后接续下一轮
		next := c.path()
		go func() {
			time.Sleep(saveInterval)
			c.flush(next)
		}()
	} else {
		c.flushing = false
	}
	c.mu.Unlock()
}

// Flush 同步落盘（测试/关停用）：等待在途异步窗口结束，再把残余脏数据立即写出。
func (c *Cache) Flush() {
	deadline := time.Now().Add(2 * time.Second)
	for {
		c.mu.Lock()
		busy := c.flushing
		c.mu.Unlock()
		if !busy || time.Now().After(deadline) {
			break
		}
		time.Sleep(2 * time.Millisecond)
	}
	c.mu.Lock()
	path := c.path()
	if path == "" || !c.dirty {
		c.mu.Unlock()
		return
	}
	st := c.snapshotLocked()
	c.dirty = false
	c.lastSave = time.Now()
	c.mu.Unlock()
	writeTo(path, st)
}

// snapshotLocked 排序快照（调用方须持 c.mu）
func (c *Cache) snapshotLocked() fileState {
	st := fileState{Entries: make([]Entry, 0, len(c.entries))}
	for _, e := range c.entries {
		st.Entries = append(st.Entries, e)
	}
	sort.Slice(st.Entries, func(i, j int) bool { return st.Entries[i].UpdatedAt > st.Entries[j].UpdatedAt })
	return st
}

// writeTo marshal + tmp 原子写盘（锁外执行；目录只读等失败静默降级为纯内存）
func writeTo(path string, st fileState) {
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return
	}
	data, err := json.MarshalIndent(st, "", "  ")
	if err != nil {
		return
	}
	tmp := path + ".tmp"
	if err := os.WriteFile(tmp, data, 0o644); err != nil {
		return
	}
	_ = os.Rename(tmp, path)
}
