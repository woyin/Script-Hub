# internal/metrics 文件夹索引

## 架构说明
工具层，提供轻量级运行时指标收集与 Prometheus 文本格式输出。
使用原子计数器和互斥锁实现，零外部依赖（不依赖 prometheus client 库）。

## 文件清单

### metrics.go
- **地位**: 指标收集与输出的唯一定义点
- **功能**: `Metrics` 结构体（requests/conversions/errors/cache 原子计数器 + conversionsByKey 维度 map）、`New()`、`Inc*()` 系列递增方法、`Render()` Prometheus exposition text 输出
- **依赖**: fmt, io, sort, sync, sync/atomic
- **被依赖**: internal/server/server.go、internal/server/handler.go（/metrics 端点）

---
⚠️ **自指声明**: 当本文件夹内容变化时（新增/删除/重命名文件），请更新此索引
