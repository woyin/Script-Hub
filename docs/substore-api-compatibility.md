# Sub-Store 订阅与文本文件发布 API

自 `1.1.3-beta` 起提供以下接口。原生管理 API 和现有分享链接继续使用原路径。

## Backend URL

原版格式的入口是：

```text
https://scripthub.woyin.me/<MANAGER_BACKEND_KEY>
```

管理页面“我的”中提供“复制 Sub-Store Backend URL”。`fgfw` 的 `SUBSTORE_BASE_URL` 使用这个地址，不要附加 `/api`。

这与当前页面地址 `/manage/<MANAGER_BACKEND_KEY>/` 不同。原因是两套接口都使用 `/api/files`，但请求和响应结构不同。新的入口提供原版扁平对象与响应包装；旧入口保留原生格式。两者读写同一个磁盘资源，不复制订阅或文件。

非冲突的 `/api/subs`、`/api/sub/:name`、`/api/file/:name`、`/api/wholeFile/:name`、`/api/wholeFiles`、`/download/:name` 也可通过 `/manage/<key>` 使用。需要完整原版文件列表和创建接口的工具，应统一使用 `/<key>` 入口。

Backend URL 包含管理密钥，能够读写资源。原有 `/share/subscriptions/...` 和 `/share/files/...` 继续用于不授予写权限的客户端分发。兼容入口不会自动轮换、创建或撤销这些分享链接。

## 接口范围

以下路径均相对于兼容 Backend URL。

| 路径 | 方法 | 行为 |
|---|---|---|
| `/api/subs` | GET | 返回完整订阅列表 |
| `/api/subs` | POST | 创建订阅，返回 201 |
| `/api/subs` | PUT | 原子替换全部订阅，保留其他文件 |
| `/api/sub/:name` | GET | 返回订阅完整对象 |
| `/api/sub/:name` | PATCH | 合并更新；省略字段保留原值 |
| `/api/sub/:name` | DELETE | 删除订阅 |
| `/api/files` | GET | 返回文件列表，省略 `content` |
| `/api/files` | POST | 创建文本/远程文件，返回 201 |
| `/api/files` | PUT | 原子替换全部文件，保留订阅 |
| `/api/wholeFiles` | GET | 返回包含 `content` 的完整文件列表 |
| `/api/wholeFile/:name` | GET | 返回文件完整对象 |
| `/api/file/:name` | GET | 返回本地原文或远程下载结果，不使用 JSON 包装 |
| `/api/file/:name` | PATCH | 合并更新文件 |
| `/api/file/:name` | DELETE | 删除文件 |
| `/download/:name` | GET | 根据目标格式输出订阅 |
| `/download/:name/:target` | GET | 从路径指定订阅目标格式 |

`:name` 按原版资源名称定位。名称使用 URL 路径编码，支持空格、中文和文件名称中的编码斜杠。订阅名称不允许斜杠。也接受当前原生管理资源的 32 位 ID，便于已有资源迁移。同一类型名称冲突会明确拒绝，不能悄悄更新其中一条。

普通对象和列表响应使用原版结构：

```json
{"status":"success","data":{"name":"provider-raw","source":"local","content":"..."}}
```

删除及批量替换成功返回 `{"status":"success"}`。失败使用 `status: "failed"`，以及包含 `code`、`type`、`message` 的 `error` 对象。未知资源为 404，重复名称或非法输入为 400，版本冲突或歧义名称为 409，未实现操作为 422。不会复制原版某些非法请求也返回 500 的行为。

`GET /api/sub/:name?raw=true` 和 `GET /api/wholeFile/:name?raw=true` 直接返回对象并提供 JSON 下载响应头，不带 `status/data` 包装。

## 写入格式

本地订阅：

```json
{
  "name": "provider-raw",
  "displayName": "Provider RAW",
  "source": "local",
  "content": "节点 URI 或 Clash YAML",
  "process": []
}
```

本地文本文件：

```json
{
  "name": "loon",
  "displayName": "Loon",
  "source": "local",
  "type": "text",
  "content": "完整配置内容",
  "process": []
}
```

远程来源使用 `source: "remote"` 和字符串 `url`。订阅允许换行分隔多个 URL；远程文件只允许一个 URL。`userAgent` 和 `headers` 映射到下载请求头，沿用现有 SSRF 防护、响应大小限制、磁盘缓存和失败处理。只有远程来源会实际使用 URL 与请求头。

`displayName`、`icon`、备注和其他描述元数据保存在资源中。通过原生页面修改内容、重启服务、快照恢复后仍可从兼容 API 读取。有效内容和来源使用原有资源结构保存，不为兼容层再保存一份正文。

`PATCH` 不要求 `If-Match`，与原版及 `fgfw` 一致。读取对象同时返回 ETag；调用方可选传入 `If-Match` 获得版本冲突检查。省略它时采用原版的最后写入生效规则。服务器仍在同一个资源锁内读取、合并和发布，避免并行 PATCH 丢掉彼此修改的不同字段。

批量 PUT 接受对象数组。全部对象先验证，再写入完整的暂存目录，建立安全快照，最后切换数据代。失败时不提交部分内容。保留的名称沿用原生 ID 和分享权限；省略的资源被删除，其分享失效。数组顺序在列表及重启后保留。

## 目标格式与限制

`target` 支持 `Clash`、`ClashMeta`/`Mihomo`/`Stash`、`ShadowRocket`、`Loon`、`Egern`、`URI`，大小写不敏感。`V2Ray` 使用与 Shadowrocket 相同的 Base64 URI 输出。`platform` 也可指定目标。未指定目标时按已识别的客户端 User-Agent 选择，否则输出 Clash 节点 YAML。

`noCache=true`、`noCache=1` 或 `refresh=true` 要求重新生成。该能力只存在于含 Backend Key 的管理入口。公开分享链接继续禁止强制刷新。

本次兼容范围是订阅、文本/远程文件和发布流程，不是完整重写原版 Sub-Store：

- 不执行 JavaScript、启用的 `process` 操作、响应变换、来源混合、Mihomo 配置生成或 AGE 输出加密。请求这些操作时明确返回 422，不将其当作已执行。
- 空的 `process` 数组及标记为 `disabled: true` 的操作可以保存。现有原生处理选项继续保留；可通过 `processing` 扩展读写。
- 集合、流量查询、归档、原版分享令牌、Gist 同步等 API 不在此范围。删除默认是永久删除；`mode=archive` 会被拒绝，不会被静默解释为永久删除。
- 错误的密钥不会得到资源数据。沿用原生管理 API 的同源校验和磁盘容量限制。列表和写入有独立的并发及请求时间限制，资源正文不进入常驻缓存。
- 下载及文件 GET 不接受动态 URL、内容或脚本覆盖，避免把未实现的处理静默忽略。完整客户端配置由文本文件提供，节点订阅导出仍仅包含节点。

## 验证证据

- Go 回归测试覆盖原版列表包装、正文与元数据读取、合并 PATCH、名称/ID 定位、中文及编码路径、下载、删除、并发创建、源请求头、源类型切换、可选版本检查、重启和快照恢复。
- 批量替换验证中间项非法、重复名称、保留 ID/分享、删除省略资源、其他类型不受影响及数组顺序。
- 服务器测试经过真实 router，覆盖兼容路径、现有路径、错误密钥和跨域拒绝。
- 使用本机 `FuckGFWByMany` 的真实 CLI 代码副本、合成凭据及临时 Go 实例，执行两组 `plan → review → apply --dry-run → apply --yes → verify`：一组包含 Loon、Shadowrocket、Egern 三个客户端，另一组为 GAME Provider RAW。10 条 CLI 命令成功，写入后的单元均为 `executed_verified`。
- 真实 `fgfw` 备份函数成功读取订阅列表、文件元数据列表、完整资源；4 种订阅下载目标成功。
- 上述 CLI 验证没有修改原 `FuckGFWByMany` 项目的配置，也没有写入线上实例。
- 浏览器实际点击“复制 Sub-Store Backend URL”，确认得到不含 `/manage` 或 `/api` 的兼容入口，无脚本错误。
- 最终 `go test -race ./... -count=1`、`go vet ./...`、`go build`、JavaScript 语法检查及 `git diff --check` 通过。

接口依据：[原版 subscriptions.js](https://github.com/sub-store-org/Sub-Store/blob/master/backend/src/restful/subscriptions.js)、[file.js](https://github.com/sub-store-org/Sub-Store/blob/master/backend/src/restful/file.js)、[response.js](https://github.com/sub-store-org/Sub-Store/blob/master/backend/src/restful/response.js)。实现复用现有 Go 存储与导出管线，没有引入 Node 服务或 JavaScript 运行时。
