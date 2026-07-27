# internal/config 文件夹索引

## 架构说明
配置层，集中管理运行时配置与领域常量。
所有配置项通过环境变量读取，与 JS 版 service.js / preview.js 的环境变量一一对应。
同时承载目标平台（Platform）、重写目标格式（Target*）、重写来源格式（SourceType*）等全局常量枚举，供路由与解析器共享。

## 文件清单

### config.go
- **地位**: 配置加载与领域常量的唯一定义点
- **功能**: `Config` 结构体（Port/Host/HTTPTimeout/MaxBodyKB）、`LoadConfig()` 从环境变量加载、`Platform` 类型与各代理平台常量、重写目标/来源格式常量集合
- **依赖**: os, strconv
- **被依赖**: main.go、internal/server/server.go、internal/server/router.go、internal/rewrite/parser.go、internal/rule/parser.go

---
⚠️ **自指声明**: 当本文件夹内容变化时（新增/删除/重命名文件），请更新此索引
