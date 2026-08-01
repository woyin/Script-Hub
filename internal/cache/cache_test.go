package cache

import (
	"testing"
	"time"
)

func TestCache_DisabledWhenZero(t *testing.T) {
	c := New(0)
	if c != nil {
		t.Error("New(0) should return nil")
	}
	// nil cache 上调用 Get/Set 不应 panic
	if _, ok := c.Get("x"); ok {
		t.Error("nil cache Get should miss")
	}
	c.Set("x", []byte("v"))
}

func TestCache_SetGet(t *testing.T) {
	c := New(60)
	c.Set("k", "v")
	got, ok := c.Get("k")
	if !ok {
		t.Fatal("miss after Set")
	}
	if got != "v" {
		t.Errorf("got %v, want v", got)
	}
}

func TestCache_MissUnknownKey(t *testing.T) {
	c := New(60)
	if _, ok := c.Get("nope"); ok {
		t.Error("unknown key should miss")
	}
}

func TestCache_TTLExpiry(t *testing.T) {
	c := New(1)
	c.Set("k", "v")
	// ttl=1s，等待过期
	time.Sleep(1100 * time.Millisecond)
	if _, ok := c.Get("k"); ok {
		t.Error("expired entry should miss")
	}
	if c.Len() != 0 {
		t.Errorf("after expiry+Get, Len = %d, want 0", c.Len())
	}
}

func TestCache_ConcurrentAccess(t *testing.T) {
	c := New(60)
	c.Set("k", 0)
	done := make(chan struct{})
	// 并发读写，验证不 panic 且最终可读到值
	for i := 0; i < 50; i++ {
		go func() {
			c.Set("k", 1)
			c.Get("k")
			done <- struct{}{}
		}()
	}
	for i := 0; i < 50; i++ {
		<-done
	}
	got, ok := c.Get("k")
	if !ok {
		t.Fatal("miss after concurrent access")
	}
	if got != 1 {
		t.Errorf("got %v, want 1", got)
	}
}

func TestCache_EvictionAtMaxEntries(t *testing.T) {
	old := maxEntries
	maxEntries = 4
	defer func() { maxEntries = old }()

	c := New(600)
	// 填满至 maxEntries
	for i := 0; i < 4; i++ {
		c.Set(string(rune('a'+i)), i)
	}
	if c.Len() != 4 {
		t.Fatalf("Len = %d, want 4", c.Len())
	}
	// 第 5 个 Set 触发驱逐：条目均未过期，应随机驱逐至 maxEntries*3/4 = 3
	c.Set("e", 4)
	if c.Len() > maxEntries {
		t.Errorf("Len = %d, should not exceed maxEntries=%d after eviction", c.Len(), maxEntries)
	}
	// 新写入的 key 必须存在
	if _, ok := c.Get("e"); !ok {
		t.Error("newly set key should be present")
	}
}

func TestCache_EvictionPrefersExpired(t *testing.T) {
	old := maxEntries
	maxEntries = 3
	defer func() { maxEntries = old }()

	c := New(600)
	c.Set("a", 1)
	c.Set("b", 2)
	// 让 "a" 过期
	c.items["a"] = entry{value: 1, expiresAt: time.Now().Add(-time.Second)}
	// 触发驱逐：应优先删除已过期的 "a"，保留 b
	c.Set("c", 3)
	c.Set("d", 4)
	if _, ok := c.Get("b"); !ok {
		t.Error("non-expired 'b' should survive when expired entries exist")
	}
}
