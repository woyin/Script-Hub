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

仓库内 `fly.toml` 使用 `breestealth-scripthub-go`、新加坡区域和 9100 端口。Fly.io 健康检查请求 `GET /healthz`。

`[build].image` 指向 GitHub Actions 已发布的 `ghcr.io/woyin/script-hub/script-hub:1.0-beta`，部署只拉取现成镜像，不在本地或 Fly builder 再次编译。版本 Git 标签为 `v1.0.0-beta`，镜像同时发布 `1.0-beta`、`1.0.0-beta`、`v1.0.0-beta` 和 `latest`。`latest` 随后续主分支构建更新；需要固定此 beta 时使用 `1.0-beta`。正式部署前等待对应镜像工作流成功。

部署前创建同区域的 1 GB Volume（已有同名卷时不要重复创建）：

```bash
fly launch --no-deploy
fly volumes create scripthub_cache --region sin --size 1 --scheduled-snapshots=false
fly deploy --ha=false
fly status
```

`fly.toml` 的 `[[files]]` 会将当前仓库的 `config/refresh-jobs.json` 上传到 `/config/refresh-jobs.json`，并通过 `REFRESH_JOBS_FILE` 交给后台调度器。JSON 是定时刷新任务清单；程序据此定时下载规则、转换并更新缓存，无需定时生成 JSON。镜像也包含构建时的默认清单，供普通 Docker 部署使用；Fly 部署以本地上传的清单为准。

`fly.toml` 将卷挂载到 `/data`，缓存目录为 `/data/cache`。磁盘缓存仅由一个服务进程管理；多实例应分别挂载自己的 Volume。缓存可以重建，无需快照备份。

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
| `CACHE_TTL_SECONDS` | `86400` | 磁盘结果缓存 TTL 秒数（≤0 禁用；仅缓存不含 localtext 的远程转换）；Fly 使用 86400 |
| `CACHE_DIR` | 系统临时目录下的 `script-hub-cache` | 磁盘缓存目录；Fly 使用 `/data/cache` |
| `CACHE_MAX_BYTES` | `838860800` | 正式缓存、读取中的旧文件及临时空间的总预算，默认 800 MiB；≤0 使用默认值 |
| `MAX_CONCURRENT_CONVERSIONS` | `2` | 冷规则转换并发上限；Fly 配置为 35；等待受请求超时控制；≤0 使用 2 |
| `REFRESH_JOBS_FILE` | 空字符串 | 后台任务 JSON 配置路径；空字符串禁用调度。Fly 为 `/config/refresh-jobs.json` |
| `REFRESH_STATUS_FILE` | `CACHE_DIR` 后加 `-refresh-status.json` | 脱敏任务状态文件；Fly 为 `/data/refresh-status.json` |
| `REFRESH_COOLDOWN_SECONDS` | `60` | 公开同键刷新冷却秒数；≤0 使用默认，最大 86400 |
| `REFRESH_MAX_PER_MINUTE` | `10` | 实例公开强制刷新频率；≤0 使用默认，最大 1000 |
| `SSRF_BLOCK_PRIVATE` | `true` | 拦截指向私有/保留地址的上游请求（防 SSRF）。设为 `false` 时放行内网抓取 |

### 磁盘缓存与强制刷新

缓存保存最终转换结果，正文不常驻 Go 缓存。新格式在单个 `.bin` 文件中分别保存有长度上限的元数据和原始正文，命中时只读取元数据，再用固定缓冲区发送正文。旧 `.json` 缓存在启动时清理，允许升级后冷启动。缓存键包含服务版本、来源类型、实际目标格式、有序源 URL 和转换参数；文件名使用 SHA-256 摘要。GET 与 POST 的等价请求共享结果。含 `localtext` 的请求不缓存。

普通请求在有效期内读取磁盘文件；过期后重新下载并转换。GET 链接追加 `&refresh=true` 可立即刷新。POST `/api/convert` 在 `args` 中加入 `"refresh":"true"`。刷新控制参数不进入缓存键。公开强制刷新默认同键冷却 60 秒，且整个实例每分钟最多接受 10 次；超过时返回 HTTP 429 和 `Retry-After`。普通缓存读取不受该限流影响，后台配置任务不占用公开刷新配额。

刷新成功后通过临时文件加原子替换更新原缓存。上游任一下载失败或返回非 200 时，结果不写入缓存；强制刷新返回错误，并保留原文件。原结果仍在有效期内时，后续普通请求可以继续使用。普通转换保留原有的部分结果返回行为，但部分结果不缓存。缓存过期后不提供旧结果兜底。

响应头 `X-ScriptHub-Cache`：`HIT` 表示命中，`MISS` 表示未命中，`REFRESH` 表示强制刷新成功，`BYPASS` 表示未使用缓存。普通请求写缓存失败时仍返回转换结果并记录日志；强制刷新写入失败则返回错误。同键并发转换合并执行。

每 10 分钟清理一次过期文件；空间不足时按需清理。空间不足时先清理过期文件，再淘汰最早生成的结果。预算包含原子替换期间新旧文件共存的空间，也包含仍在发送的旧文件及转换临时空间。正在读取的文件不作为容量淘汰对象。服务重启后可读取已有缓存；目录不可写时记录日志，规则转换返回 503，避免自动回退全内存。后台定时刷新通过下述 JSON 清单配置。

### 后台定时刷新配置

编辑仓库内 `config/refresh-jobs.json`。Docker 镜像包含默认清单；Fly 的 `[[files]]` 在每次部署时上传当前清单到 `/config/refresh-jobs.json`，并已设置对应环境变量。配置在启动时读取，修改后需要重新部署，但仅修改清单不需要重建镜像。不提供热加载或公开管理接口。当前仓库清单包含 25 个远程源，分别转换为 Loon、Shadowrocket、Egern，共 75 个启用任务，每 21600 秒（6 小时）刷新。Fly 缓存 TTL 配置为 86400 秒（24 小时），为刷新失败预留重试时间。内联 `localtext` 规则不加入后台任务。

```json
{
  "refreshJobs": [
    {
      "id": "china-geosite",
      "enabled": true,
      "intervalSeconds": 21600,
      "request": {
        "type": "rule-set",
        "target": "surge-rule-set",
        "urls": [
          "https://cdn.jsdelivr.net/gh/MetaCubeX/meta-rules-dat@meta/geo-lite/geosite/cn.yaml"
        ],
        "args": {
          "del": "true",
          "jqEnabled": "true"
        }
      }
    }
  ]
}
```

| 字段 | 填写规则 |
|---|---|
| `refreshJobs` | 任务数组。填 `[]` 表示没有任务；最多 128 项 |
| `id` | 唯一标识，1–64 个字母、数字、`-` 或 `_`，首字符为字母或数字 |
| `enabled` | JSON 布尔值。设为 `true` 才执行 |
| `intervalSeconds` | 60–604800 秒。启用任务时，间隔加调度偏移上限必须小于缓存 TTL。偏移上限为间隔的 10%，最多 30 秒。默认 TTL 下推荐填 21600 |
| `request.type` | 当前固定为 `rule-set` |
| `request.target` | 明确的输出格式，如 `surge-rule-set`、`loon-rule-set`、`stash-rule-set`、`surge-domain-set`。不可留空或填 `rule-set` |
| `request.urls` | 1–16 个 HTTP(S) 源 URL，顺序与普通转换一致。填写解码后的源地址，不填写 `/file/_start_/...` 转换链接。URL 不允许用户名密码或片段 |
| `request.args` | 转换参数，所有值使用字符串，例如 `"del": "true"`。不得包含 `localtext`、`refresh`、`type` 或 `target` |

添加任务时，复制整个任务对象并修改 `id`、源地址、目标和参数。相同源的不同目标或参数可分别配置；完全相同的启用转换请求不允许重复配置。空列表、未知字段、错误类型和非法任务会在启动前校验；配置错误时服务拒绝启动，避免静默漏掉任务。

修改后先校验，再发布：

```bash
# 使用 GitHub Actions 已生成的镜像校验，不执行本地编译。
docker run --rm \
  -v "$PWD/config/refresh-jobs.json:/config/refresh-jobs.json:ro" \
  ghcr.io/woyin/script-hub/script-hub:1.0-beta \
  --check-refresh-config /config/refresh-jobs.json
fly deploy --ha=false
```

后台只有一个工作任务，直接复用现有转换和缓存逻辑，不通过公网访问自身。规则转换还受 `MAX_CONCURRENT_CONVERSIONS` 的共同上限控制。启动及到期时检查缓存的新鲜程度；如果前台已刷新，会推迟重复工作。冷缓存启动时错开执行，成功后按间隔加少量随机偏移再次运行。失败后从约 1 分钟开始指数退避，最多为间隔的一半或 30 分钟中的较小值，再附加少量偏移。任务超时受 `REQUEST_TIMEOUT` 控制，服务关闭会取消任务及同键等待。失败不覆盖旧缓存，旧结果仍按原 TTL 使用。

任务状态保存到 `REFRESH_STATUS_FILE`，包含上次尝试、上次成功、下次执行、连续失败次数和错误类别；重启恢复失败退避。状态及调度日志不保存源 URL、请求头或完整错误文本。HTTP 转换指标包含后台转换。Fly 用户可通过自己的账号查看日志或下载状态文件：

```bash
fly logs
fly ssh sftp get /data/refresh-status.json ./refresh-status.json
```

权限由配置文件权限和部署账号控制。没有匿名配置修改、任务详情或手动任务触发接口。不要把含私密源地址或 Authorization 请求头的清单提交到公开仓库；这类部署应使用受控文件路径，并通过 `REFRESH_JOBS_FILE` 指定。状态文件使用 0600 权限。不要将它放入 `CACHE_DIR`，该目录仅供缓存管理。

### 规则集流式转换

磁盘可用时，规则集转换依次下载每个源文件到可复用临时文件。下载完整且未超限后，逐行解析 Clash `payload` 列表或现有规则文本，分别将有效、不支持、已排除的规则写入临时文件。统计完成后组装原有格式的统计头及正文，发布完整缓存，再流式返回。保留原顺序和重复规则，不增加任意 YAML 语法支持。重写模块的冷转换继续使用原解析器，其缓存命中同样走流式发送。

临时文件和正式缓存按实际文件长度计入全局磁盘预算，不为单个转换预留固定额度，也不设固定的单任务临时空间上限。每次写入前原子核算当前数据块的新增字节；失败或短写退回未写入部分，截断或清理文件时释放额度。仍被响应持有的文件继续计费。程序默认最多同时执行两个冷规则转换，Fly 通过环境变量设置为 35。单行长度上限为 2 MiB 与上游正文大小上限的较大值。上游正文限制仍按解压后字节数执行，生产默认仍为 600 KB。

下载中断的源会整体丢弃。普通请求可返回其他完整源产生的结果，但不会缓存；强制刷新失败保留原缓存。磁盘或临时预算不可用时返回 503，不回退全内存路径。仅显式禁用缓存（TTL≤0）时保留原有内存转换行为。

性能基准可运行：

```bash
go test ./internal/cache ./internal/server -run '^$' -bench 'BenchmarkCacheStreamHit|BenchmarkReplaceStream' -benchtime=3x
go test ./internal/rule -run '^$' -bench BenchmarkRuleConversionMemory -benchtime=1x
```

本次 Apple M4 本地单次基准：8 MiB 输入的旧转换耗时约 536 ms，采样峰值额外 Go 堆内存约 96.38 MiB；流式转换耗时约 537 ms，采样峰值约 3.56 MiB。1 MiB 输入分别约 12.78 MiB 和 3.42 MiB。缓存文件从 1 MiB 增加至 50 MiB 时，正文读取的每次内存分配约 2.5–7 KB；GET 地址替换约 139 KB，不随文件大小线性增长。另以独立测试进程测量同一 8 MiB 输入，最大 RSS 从约 140 MiB 降到 20 MiB。测试专门将输入上限提高到 16 MiB，生产默认上限保持不变。这些是本地测量结果，不代表 Fly.io RSS 或线上延迟保证。

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
