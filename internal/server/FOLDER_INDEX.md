# internal/server 文件夹索引

## 架构说明
API 层，实现 Script Hub 的 HTTP 服务入口。
采用 chi 路由器，使用全捕获后手动分发的路由模式（因 URL 含 "://" 会干扰 chi 的模式匹配）。
三个文件分别负责：HTTP 服务生命周期（server.go）、URL 路由分发（router.go）、请求处理器（handler.go）。
handler.go 是业务协调点，依次调用 frontend、rewrite、rule 三个子包完成具体任务。

## 文件清单

### server.go
- **地位**: HTTP 服务核心结构
- **功能**: `Server` 结构体（chi.Mux + http.Server + Config）、`New(cfg)` 创建服务并初始化路由、`Start(addr)` 启动监听、`Shutdown(ctx)` 优雅关闭
- **依赖**: context, net/http, github.com/go-chi/chi/v5, internal/config
- **被依赖**: main.go（创建并启动服务）

### router.go
- **地位**: URL 路由分发器
- **功能**: `setupRoutes()` 注册全捕获路由、`dispatchHandler()` 按 URL 路径分发到首页 / 健康检查 / 文件处理器、`fileHandler()` 按 type 查询参数分发到重写解析器或规则解析器；URL 解析工具（buildScriptHubURL / extractReqFromURL / extractURLArg）
- **依赖**: net/http, strings, internal/config
- **被依赖**: server.go（setupRoutes 初始化时调用）

### handler.go
- **地位**: HTTP 请求处理器集合
- **功能**: `scriptHubHandler` 生成前端页面、`rewriteParserHandler` 协调重写解析、`ruleParserHandler` 协调规则解析；`writeResponse` 统一响应写出（含 script.hub URL 替换）；`inferTargetFromUA` 从 User-Agent 推断目标平台；`baseURLFromRequest` 从 Host header 推导服务地址
- **依赖**: fmt, log, net/http, net/url, strings, internal/frontend, internal/rewrite, internal/rule, internal/types, internal/util
- **被依赖**: router.go（dispatchHandler / fileHandler 分发调用）

---
⚠️ **自指声明**: 当本文件夹内容变化时（新增/删除/重命名文件），请更新此索引
