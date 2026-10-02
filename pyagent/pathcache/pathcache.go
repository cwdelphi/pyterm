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

// save 落盘（目录不存在尝试创建；任何失败静默降级为纯内存）
func (c *Cache) save() {
	p := c.path()
	if p == "" {
		return
	}
	if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
		return
	}
	st := fileState{Entries: make([]Entry, 0, len(c.entries))}
	for _, e := range c.entries {
		st.Entries = append(st.Entries, e)
	}
	sort.Slice(st.Entries, func(i, j int) bool { return st.Entries[i].UpdatedAt > st.Entries[j].UpdatedAt })
	data, err := json.MarshalIndent(st, "", "  ")
	if err != nil {
		return
	}
	tmp := p + ".tmp"
	if err := os.WriteFile(tmp, data, 0o644); err != nil {
		// 目录只读（如容器只读根）→ 退化为内存缓存
		return
	}
	_ = os.Rename(tmp, p)
}
