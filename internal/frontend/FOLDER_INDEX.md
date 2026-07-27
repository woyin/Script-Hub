# internal/frontend 文件夹索引

## 架构说明
UI 层，提供转换页面的 Web 界面。
通过 `//go:embed` 将 index.html 内嵌进二进制，部署时无需分发静态文件。
由 server 层在根路径 "/" 调用，注入动态 baseURL 后返回给客户端。

## 文件清单

### frontend.go
- **地位**: 前端页面的生成入口
- **功能**: 内嵌 index.html，`GenerateHTML(baseURL)` 将模板占位 `__BASE_URL__` 替换为从请求 Host 推导的实际服务地址
- **依赖**: embed, strings
- **被依赖**: internal/server/handler.go（scriptHubHandler）

### index.html
- **地位**: 转换页面的 HTML 实现
- **功能**: 前端 UI，构建转换请求 URL 并跳转到对应解析器端点；包含 `__BASE_URL__` 占位由 frontend.go 注入
- **依赖**: 由 frontend.go 内嵌
- **被依赖**: frontend.go（go:embed）

---
⚠️ **自指声明**: 当本文件夹内容变化时（新增/删除/重命名文件），请更新此索引
