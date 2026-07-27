# internal/httpclient 文件夹索引

## 架构说明
工具层，提供统一的 HTTP 客户端封装。
集中处理自定义超时、默认请求头、gzip 自动解压与响应头提取，对应 JS 版 Env.js 的 HTTP 请求方法。
为 rewrite 与 rule 两个解析引擎提供抓取远程内容的能力，避免各处重复实现。

## 文件清单

### client.go
- **地位**: HTTP 请求能力的唯一封装点
- **功能**: `Client` 结构体、`NewClient(timeoutSec)`、GET/POST 系列方法（含自定义头注入）、`GetBytesWithHeaders`（返回字节+状态+响应头）、`ParseCustomHeaders`（从 "Key:Value\n" 文本解析头）
- **依赖**: compress/gzip, context, fmt, io, net/http, strings
- **被依赖**: internal/rewrite/parser.go、internal/rewrite/params.go、internal/rule/parser.go

---
⚠️ **自指声明**: 当本文件夹内容变化时（新增/删除/重命名文件），请更新此索引
