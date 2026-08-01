// Input: sync, time
// Output: type Cache, type entry, func New(), func (Cache) Get(), func (Cache) Set()
// Pos: 工具层-短 TTL 内存缓存，用于缓存不含 localtext 的远程转换结果
//
// 本注释在文件修改时自动更新，同时触发 FOLDER_INDEX 和 PROJECT_INDEX 更新

// Package cache 提供一个简单的带 TTL 的内存缓存。
// 不是 LRU：条目过期后在下一次访问时被惰性清理。
// 设计目标是给高频重复的远程规则集/模块转换降低上游压力。
// 设有 maxEntries 上限，Set 时若超限先清理过期项，仍超限则随机驱逐。
package cache

import (
	"sync"
	"time"
)

// entry 是一个缓存条目。
type entry struct {
	value     any
	expiresAt time.Time
}

// maxEntries 是缓存条目数上限，防止长期运行内存无限增长。
// 缓存对象是转换结果文本（通常几 KB～几十 KB），4096 条约几十 MB 量级。
var maxEntries = 4096

// Cache 是带全局 TTL 的线程安全内存缓存。
// 所有 Set 共用同一个 ttl；Get 时若条目已过期则视为未命中并删除。
type Cache struct {
	mu    sync.Mutex
	items map[string]entry
	ttl   time.Duration
}

// New 创建一个缓存。ttlSeconds <=0 时返回 nil，表示禁用缓存。
func New(ttlSeconds int) *Cache {
	if ttlSeconds <= 0 {
		return nil
	}
	return &Cache{
		items: make(map[string]entry),
		ttl:   time.Duration(ttlSeconds) * time.Second,
	}
}

// Get 读取未过期的缓存值；过期或不存在返回 (nil, false)。
func (c *Cache) Get(key string) (any, bool) {
	if c == nil {
		return nil, false
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	e, ok := c.items[key]
	if !ok {
		return nil, false
	}
	if time.Now().After(e.expiresAt) {
		delete(c.items, key)
		return nil, false
	}
	return e.value, true
}

// Set 写入一个缓存条目，TTL 为创建缓存时指定的时长。
// 超过 maxEntries 时先清理已过期条目；仍超限则随机驱逐若干条目腾出空间。
func (c *Cache) Set(key string, value any) {
	if c == nil {
		return
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	if len(c.items) >= maxEntries {
		c.evictLocked()
	}
	c.items[key] = entry{
		value:     value,
		expiresAt: time.Now().Add(c.ttl),
	}
}

// evictLocked 清理过期条目；若仍超 maxEntries 则随机驱逐。
// 调用方必须已持有 c.mu。
func (c *Cache) evictLocked() {
	now := time.Now()
	for k, e := range c.items {
		if now.After(e.expiresAt) {
			delete(c.items, k)
		}
	}
	// 仍超限：随机驱逐至 maxEntries 的 3/4，避免每次 Set 都触发驱逐。
	target := maxEntries * 3 / 4
	for len(c.items) > target {
		for k := range c.items {
			delete(c.items, k)
			break
		}
	}
}

// Len 返回当前缓存条目数（含可能已过期但未惰性清理的），仅供观测。
func (c *Cache) Len() int {
	if c == nil {
		return 0
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	return len(c.items)
}
