# internal/util 文件夹索引

## 架构说明
工具层，提供跨模块共享的纯函数工具集。
所有函数均为无状态纯函数，对齐 JS 版的 arg 解析与 query string 行为。
被 rewrite、rule、server 包广泛复用，是行为一致性的统一维护点。

## 文件清单

### util.go
- **地位**: 全局工具函数库
- **功能**: 布尔判断 `IsTrue`、"+" 分隔参数拆分 `GetArgArr`（含 ➕ 还原）、查询字符串解析 `ParseQueryString` / `ParseQueryStringLenient`（严格/宽松两种模式）
- **依赖**: net/url, strings
- **被依赖**: internal/rewrite/params.go、internal/rewrite/converter.go、internal/rewrite/parsers.go、internal/rule/parser.go、internal/server/handler.go

---
⚠️ **自指声明**: 当本文件夹内容变化时（新增/删除/重命名文件），请更新此索引
