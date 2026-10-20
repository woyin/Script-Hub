# internal/manager 文件夹索引

私有订阅与文件管理模块。默认关闭，通过 Backend URL 验证管理员；资源分享使用独立摘要 token。

- `source.go`：同源下载合并、磁盘源缓存、HTTP 条件更新及凭据隔离。
- `scheduler.go` / `runtime.go`：后台刷新、退避和持久状态、聚合指标及资源版本保护。
- `backup.go` / `admin.go`：有界快照、原子代次恢复及鉴权管理接口。
- `optimization_test.go`：源复用、304、回退、版本冲突、调度恢复、快照校验及读取保护。
- `store.go`：资源模型、验证、原子 JSON 持久化、永久数据预算、分享轮换与撤销。
- `handler.go`：管理/分享路由、来源读取、私有磁盘导出缓存、并发合并与超时。
- `page.go` / `index.html` / `management.css` / `management.js` / `icons.svg`：内嵌管理界面，按 Sub-Store 的导航、卡片和主题适配；不加载外部资源。
- `UI-NOTICE.md`：界面与素材来源、上游版本及 GPL-3.0 许可说明。
- `manager_test.go`：权限、资源隔离、重启、缓存撤销、远程失败、并发合并和目录校验。

数据目录必须与 cache 目录隔离；不保存分享 token 明文；不记录原始来源或凭据。
