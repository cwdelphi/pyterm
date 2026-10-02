package pathcache

import (
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestRecordLookupHits(t *testing.T) {
	c := NewTTL("", DefaultTTL)
	c.Record("agent-a", "eth0", "hash1", "P2P", 0)
	e, ok := c.Lookup("agent-a", "hash1")
	if !ok || e.LocalIface != "eth0" {
		t.Fatalf("want hit eth0, got %+v ok=%v", e, ok)
	}
	if e.Hits != 1 {
		t.Fatalf("want hits 1 after first lookup, got %d", e.Hits)
	}
	if e, _ := c.Lookup("agent-a", "hash1"); e.Hits != 2 {
		t.Fatalf("want hits 2, got %d", e.Hits)
	}
}

func TestLookupMiss(t *testing.T) {
	c := NewTTL("", DefaultTTL)
	if _, ok := c.Lookup("nope", "h"); ok {
		t.Fatal("empty cache must miss")
	}
}

func TestLookupIfHashMismatchInvalidates(t *testing.T) {
	c := NewTTL("", DefaultTTL)
	c.Record("agent-a", "eth0", "hash1", "relay", 0)
	if _, ok := c.Lookup("agent-a", "hash2"); ok {
		t.Fatal("hash mismatch must miss")
	}
	// 被踢掉后应查不到
	if _, ok := c.Lookup("agent-a", "hash2"); ok {
		t.Fatal("entry should be dropped after hash mismatch")
	}
}

func TestLookupSkipsHashCheckWhenUnknown(t *testing.T) {
	c := NewTTL("", DefaultTTL)
	c.Record("agent-a", "eth0", "hash1", "P2P", 0)
	// 调用方 if_hash 未知("") → 跳过 hash 校验
	if _, ok := c.Lookup("agent-a", ""); !ok {
		t.Fatal("empty ifHash must skip validation")
	}
}

func TestLookupExpiry(t *testing.T) {
	const ttl = 30 * time.Second
	c := NewTTL("", ttl)
	c.Record("agent-a", "eth0", "h", "P2P", 0)
	if _, ok := c.Lookup("agent-a", "h"); !ok {
		t.Fatal("should hit before expiry")
	}
	// UpdatedAt 是秒粒度 → 手工回拨到 TTL 之外，避免 sleep
	c.mu.Lock()
	e := c.entries["agent-a"]
	e.UpdatedAt = time.Now().Add(-time.Minute).Unix()
	c.entries["agent-a"] = e
	c.mu.Unlock()
	if _, ok := c.Lookup("agent-a", "h"); ok {
		t.Fatal("must miss after TTL")
	}
}

func TestPersistAndReload(t *testing.T) {
	dir := t.TempDir()
	c := New(dir)
	c.Record("agent-a", "eth0", "hash1", "P2P", 0)
	if _, err := os.Stat(filepath.Join(dir, fileName)); err != nil {
		t.Fatalf("cache file not written: %v", err)
	}
	// 新实例（模拟重启）应载入
	c2 := New(dir)
	if e, ok := c2.Lookup("agent-a", "hash1"); !ok || e.LocalIface != "eth0" {
		t.Fatalf("reload miss, got %+v ok=%v", e, ok)
	}
}

func TestInvalidateIfHash(t *testing.T) {
	c := NewTTL("", DefaultTTL)
	c.Record("a", "eth0", "hash1", "P2P", 0)
	c.Record("b", "eth1", "hash2", "P2P", 0)
	n := c.InvalidateIfHash("hash2")
	if n != 1 {
		t.Fatalf("want 1 invalidated, got %d", n)
	}
	if _, ok := c.Lookup("a", ""); ok {
		t.Fatal("stale-hash entry must be dropped")
	}
	if _, ok := c.Lookup("b", ""); !ok {
		t.Fatal("matching-hash entry must survive")
	}
}

func TestClear(t *testing.T) {
	c := NewTTL("", DefaultTTL)
	c.Record("a", "eth0", "h", "P2P", 0)
	c.Clear()
	if c.Len() != 0 {
		t.Fatalf("want 0 after clear, got %d", c.Len())
	}
}

func TestEntriesSortedByTime(t *testing.T) {
	c := NewTTL("", DefaultTTL)
	c.Record("a", "eth0", "h", "P2P", 0)
	c.Record("b", "eth1", "h", "P2P", 0)
	// UpdatedAt 是秒粒度，直接改时间戳避免 sleep
	c.mu.Lock()
	a := c.entries["a"]
	a.UpdatedAt = 1000
	c.entries["a"] = a
	b := c.entries["b"]
	b.UpdatedAt = 2000
	c.entries["b"] = b
	c.mu.Unlock()
	es := c.Entries()
	if len(es) != 2 || es[0].Key != "b" || es[1].Key != "a" {
		t.Fatalf("want newest first, got %+v", es)
	}
}

func TestRecordRequiresKeyAndIface(t *testing.T) {
	c := NewTTL("", DefaultTTL)
	c.Record("", "eth0", "h", "P2P", 0)
	c.Record("k", "", "h", "P2P", 0)
	if c.Len() != 0 {
		t.Fatalf("invalid records must be ignored, got %d", c.Len())
	}
}
