# internal/types 文件夹索引

## 架构说明
数据共享层，定义各解析器/转换器与 HTTP 层之间传递的统一数据契约。
仅包含纯数据类型与最小接口，无任何业务逻辑，是整个项目的依赖根之一。
被 rewrite、rule、server 三个上游包共同依赖，提供 ResponseData 与 ResponseWriter 抽象。

## 文件清单

### types.go
- **地位**: 共享类型定义的唯一来源
- **功能**: 定义统一响应结构 `ResponseData`（status/headers/body）与输出接口 `ResponseWriter`（GetResponse()）
- **依赖**: 无外部依赖（纯标准库类型组合）
- **被依赖**: internal/rewrite/parser.go、internal/rule/parser.go、internal/server/handler.go

---
⚠️ **自指声明**: 当本文件夹内容变化时（新增/删除/重命名文件），请更新此索引
