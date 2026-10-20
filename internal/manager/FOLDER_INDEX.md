# internal/manager 文件夹索引

私有订阅与文件管理模块。默认关闭，通过 Backend URL 验证管理员；资源分享使用独立摘要 token。

- `source.go`：同源下载合并、磁盘源缓存、HTTP 条件更新、失败冷却及凭据隔离。
- `scheduler.go` / `runtime.go`：按到期时间与变更唤醒、前台额度保留、后台刷新退避及持久状态、聚合指标。
- `hotpath.go` / `preparation.go`：元数据鉴权、独立有界缓存读取、活跃转换临时共享节点。
- `backup_check.go`：配置/权限数据指纹及持久化检查时间，跳过重复自动快照。
- `hotpath_test.go` / `deadline_test.go`：热点路径、取消与背压、调度优先级、备份去重及列表基准。
- `backup.go` / `admin.go`：有界快照、原子代次恢复及鉴权管理接口。
- `optimization_test.go`：源复用、304、回退、版本冲突、调度恢复、快照校验及读取保护。
- `store.go`：资源模型、轻量元数据索引、验证、原子 JSON 持久化、永久数据预算、分享轮换与撤销。
- `handler.go`：管理/分享路由、来源读取、私有磁盘导出缓存、并发合并与超时。
- `page.go` / `index.html` / `management.css` / `management.js` / `icons.svg`：内嵌管理界面，按 Sub-Store 的导航、卡片和主题适配；不加载外部资源。
- `UI-NOTICE.md`：界面与素材来源、上游版本及 GPL-3.0 许可说明。
- `manager_test.go`：权限、资源隔离、重启、缓存撤销、远程失败、并发合并和目录校验。

数据目录必须与 cache 目录隔离；不保存分享 token 明文；不记录原始来源或凭据。

- `subscription_compat_test.go`：混合 URI 订阅经公开分享接口输出 Egern 的首次转换、缓存命中和 UDP 映射回归测试。

- `compat.go`：原版发布 API 路由、响应包装、JSON 请求与原文下载；完整兼容入口独立于原生同名文件列表接口。
- `compat_model.go`：原版扁平文档与原生资源映射、按名称或 ID 查找、原子合并更新及描述元数据保留。
- `compat_store.go`：完整订阅/文件集合的验证、安全快照和原子替换，复用快照恢复的数据代切换。
- `compat_test.go`：发布契约、冲突、编码名称、元数据持久化、并发、远程请求头及批量替换回归测试。
