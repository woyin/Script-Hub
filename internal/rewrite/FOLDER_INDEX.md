# internal/rewrite 文件夹索引

## 架构说明
业务层核心之一：重写规则解析与转换引擎，完整还原 JS 版 Rewrite-Parser.js 的功能。
采用「解析 → 中间表示 → 转换」三段式管线：各来源格式（QX 重写 / Surge 模块 / Loon 插件 / 自动识别）先解析为统一的 `ParsedModule`/`ParsedRewrite` 中间表示，再按目标平台（Surge / Shadowrocket / Loon / Stash / 通用）转换输出。
文件按职责拆分：类型与入口（parser.go）、来源解析（parsers.go）、目标转换（converter.go）、参数改写（params.go）、逻辑规则改写（ruleparser.go）。

## 文件清单

### parser.go
- **地位**: 包入口与核心类型定义
- **功能**: 定义 `ParseInput`/`ParseOutput`/`Parser`，`Parse()` 抓取远程内容（含 localtext/404/块注释处理），按 SourceType 分发解析；定义全部中间表示类型（ParsedRewrite / ParsedModule / PanelInfo / HostInfo / SgArgument 等）与 RewriteType 枚举
- **依赖**: context, log, regexp, strings, internal/config, internal/httpclient, internal/types
- **被依赖**: internal/server/handler.go（rewriteParserHandler）

### parsers.go
- **地位**: 来源格式解析器集合
- **功能**: 实现 QX 重写（parseQXRewrite/parseQXLine，含 cron）、Surge 模块（parseSurgeModule，按 section 解析）、Loon 插件（parseLoonPlugin）、自动识别（parseAutoDetect）；辅助解析 mock/echo-response/panel/host/arguments/body-rewrite 等结构化行
- **依赖**: encoding/json, fmt, net/url, regexp, strings, internal/util
- **被依赖**: parser.go（Parse 内部调用）

### converter.go
- **地位**: 目标平台转换器集合
- **功能**: `convertModules` 按目标分发；Surge/Shadowrocket、Loon、Stash、Generic 四套转换与格式化器；Header Rewrite / Map Local / Mock / Script / Panel / Host / Body Rewrite 各类型的平台映射；通用工具（uniqueStrings / filterCommented / sanitizeName / cleanRegexEscapes / extractHostnames）
- **依赖**: fmt, net/url, regexp, strings, internal/util
- **被依赖**: parser.go（Parse 内部调用）

### params.go
- **地位**: 参数改写函数集
- **功能**: Apply*Modification 系列——脚本参数/名称/超时/引擎/Cron、MITM 增删/正则删、规则策略/sni/pm；元数据覆盖（name/desc/icon/category）、KeLee 图标解析与随机贴纸（ApplyIconReplacement）、模板提取（TakeLeadingTemplate）、去重（dedupRewrites/dedupScripts）
- **依赖**: context, encoding/json, fmt, math/rand, regexp, strings, sync, internal/httpclient, internal/util
- **被依赖**: parser.go（Parse 内部调用）

### ruleparser.go
- **地位**: 逻辑规则改写引擎
- **功能**: 解析 Surge 逻辑规则（AND/OR/NOT）为规则树（ruleNode），按 RuleFlags（extended-matching / no-resolve / pre-matching / src）重生成为目标平台文本；含完整词法分析器（tokenize）
- **依赖**: fmt, log, regexp, strings
- **被依赖**: params.go（ApplySniPm 对逻辑规则调用 ModifyRule）

## 测试文件

- `parser_test.go`、`ruleparser_test.go` — 单元测试，验证解析与逻辑规则改写的正确性（不纳入索引文件头）

---
⚠️ **自指声明**: 当本文件夹内容变化时（新增/删除/重命名文件），请更新此索引
