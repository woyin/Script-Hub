# internal/cache 文件夹索引

## 架构说明
工具层，提供带 TTL 和条目上限的内存缓存。
用于缓存不含 localtext 的远程转换结果，降低高频重复转换的上游压力。
非 LRU：条目在 Get 时惰性检查过期；Set 时若超过 maxEntries 先清理过期项，仍超限则随机驱逐。

## 文件清单

### cache.go
- **地位**: 缓存的唯一定义点
- **功能**: `Cache` 结构体（mutex + items map + ttl）、`New()` 工厂（ttl<=0 返回 nil 禁用）、`Get()`/`Set()`/`Len()`、`evictLocked()` 过期清理+驱逐
- **依赖**: sync, time
- **被依赖**: internal/server/server.go、internal/server/handler.go

---
⚠️ **自指声明**: 当本文件夹内容变化时（新增/删除/重命名文件），请更新此索引
