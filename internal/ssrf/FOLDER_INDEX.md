# internal/ssrf 文件夹索引

## 架构说明
工具层，提供可选的 SSRF 防护。
当启用时（SSRF_BLOCK_PRIVATE=true），拦截指向私有/环回/链路本地/云元数据地址的上游请求。
防 DNS rebinding：真正的校验发生在 DialContext 拨号阶段，解析的 IP 与实际连接的 IP 原子绑定，消除"先检查再连接"的 TOCTOU 窗口。

## 文件清单

### ssrf.go
- **地位**: SSRF 防护的唯一定义点
- **功能**: `IsBlockedAddr()` IP 范围校验、`DialContext()` 受控拨号器（解析+校验+直连）、`NewTransport()` 构造受控 `*http.Transport`、`IsBlocked()`/`Check()`/`MaybeCheck()` 纯校验公共 API、`Enabled` 全局开关
- **依赖**: context, errors, fmt, net, net/http, net/netip, net/url, time
- **被依赖**: internal/httpclient/client.go（NewClient 启用时注入 Transport）、main.go（设置 Enabled）

---
⚠️ **自指声明**: 当本文件夹内容变化时（新增/删除/重命名文件），请更新此索引
