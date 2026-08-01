# Script Hub (Go)

规则集与重写配置格式转换服务。基于 [Script-Hub-Org/Script-Hub](https://github.com/Script-Hub-Org/Script-Hub) 重写并精简为 Go 服务。

本项目不是原项目的官方 Go 版本。功能目标限定为客户端之间的规则集与重写配置格式转换。

## 产品边界

保留：

- QX Rewrite、Surge Module、Loon Plugin 自动识别与互转
- 输出 Surge/Egern/LanceX Module、Loon Plugin、Stash Override、Shadowrocket Module、QX Rewrite
- 常见规则集转换为 Surge、Loon、Stash、Shadowrocket 规则集或域名集
- 远程 HTTP(S) URL 和本地粘贴内容输入
- 简单 Web UI、Docker 镜像、跨平台 Release

不提供：

- JavaScript 运行时兼容转换
- 任意 JavaScript `eval`
- HTTP mock、内容代理、subconverter
- 静态 HTML 导出、客户端重载等部署辅助功能

脚本声明会转换为目标配置语法，但脚本 URL 和脚本内容保持原样。脚本自身必须兼容目标客户端。

## 架构

采用分层架构：入口层（main）→ API 层（internal/server，chi 路由 + singleflight）→ 业务层（internal/rewrite 重写引擎、internal/rule 规则集引擎）→ 工具/防护/可观测层（httpclient、ssrf、metrics、cache、util）。完整架构图、依赖关系与核心流程见 [PROJECT_INDEX.md](PROJECT_INDEX.md)。

## 运行

```bash
go build -o script-hub .
./script-hub
```

默认地址：`http://0.0.0.0:9100`

### Docker

```bash
docker pull ghcr.io/woyin/script-hub/script-hub:latest
docker run --rm -p 9100:9100 ghcr.io/woyin/script-hub/script-hub:latest
```

### Fly.io

```bash
fly launch --no-deploy
fly deploy
fly status
```

仓库内 `fly.toml` 使用 `breestealth-scripthub-go`、香港区域和 9100 端口。Fly.io 健康检查请求 `GET /healthz`。

## 端点

| 路径 | 说明 |
|---|---|
| `GET /` | 转换页面（Web UI） |
| `GET /healthz` | 健康检查（返回 200） |
| `GET /version` | 返回当前部署版本号（纯文本，如 `v2.3.0`） |
| `POST /api/convert` | 语义化转换 API（JSON 请求体，见下文） |
| `GET /formats` | 返回实例支持的来源/目标格式清单（JSON） |
| `GET /metrics` | 运行时指标（Prometheus 文本格式） |
| `GET /file/_start_/{URL_ENCODED_INPUT}/_end_/?type={SOURCE}&target={TARGET}` | 旧转换端点（兼容） |

### 指标说明

`/metrics` 仅统计**转换请求**（rewrite/rule 解析路径），不含 `/healthz`、`/version`、`/`、`/formats`、`/metrics` 自身等基础设施端点。`/healthz` 常被探活高频调用，计入会稀释业务转换 QPS 的信号。

阅读口径：
- `scripthub_conversions_total` 每次"产生一次转换结果"时递增（含缓存命中）。单次请求最多触发一次，故正常情况下 `conversions_total` 与 `requests_total` 同步增长。
- 缓存开启时，singleflight 的"等待方"（并发请求复用首个请求的工作）记为 `cache_misses`——它到达时缓存尚未填充，其收益体现在上游不被打爆，而非命中率。

| 指标 | 说明 |
|---|---|
| `scripthub_requests_total` | 转换请求总数（不含基础设施端点） |
| `scripthub_conversion_errors_total` | 转换失败次数（parser 返回错误） |
| `scripthub_fetch_errors_total` | 上游 HTTP fetch 失败次数 |
| `scripthub_cache_hits_total` / `scripthub_cache_misses_total` | 缓存命中 / 未命中 |
| `scripthub_conversions_total{source,target}` | 按 source×target 维度的转换计数 |

## 环境变量

| 变量 | 默认值 | 说明 |
|---|---:|---|
| `PORT` | `9100` | HTTP 端口 |
| `HOST` | `0.0.0.0` | 监听地址 |
| `HTTP_TIMEOUT` | `20` | 单次上游 fetch 超时秒数 |
| `PARSER_BODY_MAX` | `600` | 解析器内容上限 KB |
| `REQUEST_TIMEOUT` | `60` | 单次转换请求总超时秒数（覆盖多 URL 串行 fetch） |
| `CACHE_TTL_SECONDS` | `0` | 转换结果缓存 TTL 秒数（0=禁用；仅缓存不含 localtext 的远程转换） |
| `SSRF_BLOCK_PRIVATE` | `true` | 拦截指向私有/保留地址的上游请求（防 SSRF）。设为 `false` 时放行内网抓取 |

### SSRF 防护与 DNS rebinding

默认开启（`SSRF_BLOCK_PRIVATE` 默认 `true`，可设 `false` 关闭）。开启后，上游请求的拨号阶段会校验目标 IP：

- DNS 解析、IP 校验与 TCP 连接在 `DialContext` 中原子完成
- 消除"先检查再连接"的 TOCTOU 窗口，防御 DNS rebinding 攻击
- 任一解析候选 IP 落在私有/环回/链路本地/元地址范围即拒绝整个请求

## API

### 语义化转换 API（推荐）

```text
POST /api/convert
Content-Type: application/json
```

请求体：

```json
{
  "urls":   ["https://example.com/module.sgmodule"],
  "type":   "qx-rewrite",
  "target": "surge-module",
  "args":   { "localtext": "...", "policy": "DIRECT" }
}
```

- `urls`：远程地址列表；为空时必须提供 `args.localtext`
- `type`：来源格式，取值见下表
- `target`：目标格式，可选
- `args`：等价于旧端点的查询参数（`localtext`、`headers`、`policy` 等）

响应：转换后的原始文本，`Content-Type` 由目标格式决定。

#### 错误响应

| HTTP 状态码 | 触发条件 |
|---|---|
| `400` | JSON 无效、缺少 `type`、`type` 取值不支持、既无 `urls` 也无 `localtext` |
| `413` | 请求体超过 2 MiB |
| `500` | 转换失败（错误详情仅记录在服务端日志，不返回给客户端） |
| `504` | 请求超过 `REQUEST_TIMEOUT`（由端到端超时触发） |

错误响应体为 JSON：`{"error":"<message>"}`。

### 旧转换端点（兼容）

```text
/file/_start_/{URL_ENCODED_INPUT}/_end_/?type={SOURCE}&target={TARGET}
```

粘贴文本使用 `http://local.text` 作为输入，并通过 `localtext` 查询参数传递内容。

来源格式：

```text
all-module
qx-rewrite
surge-module
loon-plugin
rule-set
```

目标格式：

```text
surge-module
loon-plugin
stash-stoverride
shadowrocket-module
egern-module
lancex-module
qx-rewrite
surge-rule-set
loon-rule-set
stash-rule-set
shadowrocket-rule-set
egern-rule-set
lancex-rule-set
surge-domain-set
stash-domain-set
```

## 开发检查

```bash
go test ./...
go vet ./...
```

## 上游项目与致谢

本项目衍生自 [Script-Hub-Org/Script-Hub](https://github.com/Script-Hub-Org/Script-Hub)。感谢原项目作者与所有贡献者完成规则解析、客户端格式适配、脚本兼容研究及长期维护；本项目的转换行为、格式定义和部分实现思路建立在这些工作之上。

也感谢原项目 README 中列出的相关作者与项目，包括：

- [Chavy's Env.js](https://github.com/chavyleung/scripts)
- [KOP-XIAO/QuantumultX](https://github.com/KOP-XIAO/QuantumultX)
- [xream](https://github.com/xream)
- [keywos](https://github.com/keywos)
- [mieqq](https://github.com/mieqq)
- [Maasea](https://github.com/Maasea)
- [KeiKinn/StickerOnScreen](https://github.com/KeiKinn/StickerOnScreen)
- [Toperlock/Quantumult](https://github.com/Toperlock/Quantumult)

完整历史与贡献者名单以[原项目](https://github.com/Script-Hub-Org/Script-Hub)为准。

## 开源协议

原项目在 `package.json` 中声明 `GPL-3.0`，并附带 GNU General Public License Version 3。当前项目属于其修改和重写版本，因此继续采用 **GNU General Public License v3.0 only（GPL-3.0-only）**，不改用更宽松或闭源协议。

许可证全文见 [LICENSE](LICENSE)。使用、修改或分发本项目时，须遵守 GPL v3，包括但不限于：

- 保留适用的版权、许可证和免责声明；
- 标明本项目包含对原项目的修改；
- 分发目标代码时，按 GPL v3 提供对应源代码；
- 对本项目衍生作品整体继续使用 GPL v3 兼容条款。

原项目及其贡献者的版权归各自权利人所有。本说明不构成原作者对本项目的背书，也不取代 `LICENSE` 正文。

SPDX-License-Identifier: `GPL-3.0-only`
