# internal/rule 文件夹索引

## 架构说明
业务层核心之一：规则集解析与转换引擎，完整还原 JS 版 rule-parser.js 的功能。
接收各平台规则集（DOMAIN / IP-CIDR / USER-AGENT 等），经预处理（去注释、前缀缩写展开、域名检测）后解析为结构化 ruleLine，再按目标平台格式化输出。
支持五种目标格式：Surge / Shadowrocket / Loon / Stash / 域名集（domain-set / domain-set2）。

## 文件清单

### parser.go
- **地位**: 包入口与全部业务逻辑
- **功能**: `ParseInput`/`ParseOutput`/`Parser` 定义，`Parse()` 抓取远程规则内容；`parseRules()` 完整预处理管线（去注释、payload: 前缀、{N,M} 逗号保护、include/exclude、no-resolve 注入）；`parseRuleLine()` 单行解析与类型归一化；`formatOutput()` / `formatSurgeRule()` / `formatLoonRule()` / `formatStashRule()` / `formatDomainSet()` / `formatDomainSet2()` 多平台输出；规则类型归一化与不支持类型检测
- **依赖**: context, fmt, log, regexp, strings, internal/config, internal/httpclient, internal/types, internal/util
- **被依赖**: internal/server/handler.go（ruleParserHandler）

## 测试文件

- `parser_test.go` — 单元测试，验证规则解析与跨平台格式化的正确性（不纳入索引文件头）

---
⚠️ **自指声明**: 当本文件夹内容变化时（新增/删除/重命名文件），请更新此索引
