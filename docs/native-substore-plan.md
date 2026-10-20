# 原生 Go 订阅与文件管理设计及执行计划

状态：第一版及界面适配已部署至 `v1.1.1-beta`。用户已确认三个客户端实际测试通过。本轮优化按 `v1.1.2-beta` 实现、验证并发布。范围来自用户确认：Shadowrocket、Loon、Egern 三个客户端，节点订阅和 Clash YAML 输出；文本文件、远程文件及分享；不执行 JavaScript 文件脚本。

## 产品边界

- 保留现有公开规则集/重写转换，新增独立私有管理模块；不是 Sub-Store 前端或数据格式的直接替代品。
- 使用单管理员 Backend URL，不做账号/多租户。环境变量 `MANAGER_BACKEND_KEY` 为空时功能关闭；配置后访问 `/manage/<key>/`。密钥至少 32 个安全随机字符，不提交仓库。
- 本地 `.env` 通过 Docker `--env-file` 注入；Fly 使用 `fly secrets set`。程序不自动读取工作目录中的秘密文件。
- 订阅管理：新增、列表、查看、修改、删除；来源为本地内容或有序 HTTP(S) URL 列表；全部来源成功后才导出；支持合并、名称筛选、正则改名、去重、排序。
- 文件管理：本地文本或单个远程 URL；新增、查看、修改、删除、导出与分享。远程下载使用现有 SSRF 拨号检查、超时和解压后大小上限。文件按文本/附件输出，不执行脚本。
- 分享 token 与管理密钥分离。资源级 token 随机生成、只存摘要、可轮换和撤销；分享入口不能管理、枚举资源或强刷。新 token 只返回一次。

## 模块及接口

1. `internal/subscription`：`Parse` 将 Clash YAML、节点 URI、Base64 URI 订阅转换成节点集合；`Process` 执行可配置处理；`Export` 按目标格式生成结果；`Capabilities` 给出格式/协议支持范围。目标包括 `clash`、`shadowrocket`（Base64 URI）、`uri`、`loon`（原生 Proxy 文本）、`egern`（原生 YAML）。
2. `internal/manager`：资源持久化、管理及分享鉴权、来源读取、私有导出缓存、操作界面。对 server 暴露 HTTP Handler，注入读取函数和现有磁盘 Cache，不引入 Node。
3. `internal/config`、`internal/server`：配置及路由接入。启动时校验密钥/目录；有配置但初始化失败时拒绝启动，不退化成公开入口。

## 兼容性规则

- Clash 输出保留节点原始字段；输出是 `proxies` 节点订阅，不是包含策略组与规则的完整配置。
- 其他目标按客户端公开文档映射。首版覆盖 SS、Trojan、VMess、VLESS、HTTP/SOCKS5 及能精确表达的 HY2/TUIC；具体组合以 capabilities 和导出检查为准。
- Egern 使用协议对象 YAML。VMess/VLESS 的 TLS/WS/gRPC、Trojan WebSocket 分别映射，不能输出 Surge 行。
- 遇到未知协议、未实现字段、不可表达传输、TLS/Reality/flow 不兼容组合，整个导出失败并给出节点与字段；不默认丢弃节点或参数。
- Shadowrocket 使用标准 URI 及整份 Base64，不实现未经验证的私有参数扩展。真实客户端导入和连接仍需用户环境验收。
- 首版不包含任意 JS、节点测活、GeoIP、定时 Gist 同步、现有 Sub-Store 数据自动迁移、完整配置模板合成。

## 持久化及资源控制

- `MANAGER_DATA_DIR`（Fly `/data/manager`）保存每资源一份 JSON，目录 0700、文件 0600、临时文件 + 原子替换；单进程管理。
- 与 `/data/cache` 隔离，不参与缓存淘汰；目录不能与缓存目录重叠。默认持久数据总量 32 MiB、来源/管理请求 8 MiB、资源数 256、节点数 10000；均不做每任务固定磁盘预留。
- 列表返回摘要，不一次读取所有订阅正文到一个列表。写入失败保留旧资源。
- 导出正文复用磁盘缓存；缓存键包含资源 ID、配置内容版本和目标格式；先鉴权再查缓存。更新资源使旧结果失效；分享 token 撤销后即使已有缓存也不能访问。
- 节点解析/排序需要完整集合，不能声称纯流式。管理导出使用单独的有界并发（首版默认 2，可通过 `MANAGER_CONCURRENCY` 调整），不直接复用规则的 35 个转换额度；同 key 合并，超时取消等待。
- 不记录管理密钥、分享 token、原始订阅 URL、请求头或节点凭据。管理/分享响应 no-store、no-referrer、nosniff，不开放通配 CORS。

## HTTP 接口

管理根路径为 `/manage/<key>`，内含管理页面及以下接口：

- `GET/POST /api/subscriptions`、`GET/PUT/DELETE /api/subscriptions/{id}`。
- `GET/POST /api/files`、`GET/PUT/DELETE /api/files/{id}`。
- `GET /api/{subscriptions|files}/{id}/export?target=...`；管理员可加 `refresh=true`。
- `POST/DELETE /api/{subscriptions|files}/{id}/share` 生成/撤销分享 token。
- `GET /api/capabilities` 查询支持范围。
- 公开只读分享：`GET /share/{subscriptions|files}/{id}/{token}?target=...`。

## 实施顺序和验收

| 步骤 | 产物 | 可验证条件 |
|---|---|---|
| 1 | 密钥配置、原子存储和 CRUD | 无密钥关闭；错密钥拒绝；重启读回；并发写入不损坏；失败更新不覆盖旧值 |
| 2 | 节点输入及各目标输出 | YAML/URI/Base64 测试；IPv6、转义、TLS/WS/gRPC/Reality；未知字段拒绝；输出结构与客户端文档一致 |
| 3 | 文件、下载和分享 | 远程失败不输出部分结果；SSRF 保留；跨资源 token 拒绝；轮换/撤销立即生效；正文缓存不能绕过权限 |
| 4 | 管理 UI、现有 server 接入 | 页面可操作两种资源；无脚本执行入口；旧公开转换路由继续通过原有测试 |
| 5 | 文档和完整验证 | go vet/build、配置校验、全量 race 测试；模拟完整管理/导出/分享流程 |

本轮先在工作区完成可审查实现。不开启线上管理密钥，不导入用户私有订阅，不自动改动生产数据。真实客户端验收、实际节点协议补齐和管理导出并发调优在明确样本后继续。

## 第一方格式依据

- Loon: https://nsloon.app/docs/Node/ 及 https://nsloon.app/docs/Node/subscription/
- Egern: https://egernapp.com/docs/configuration/proxies/
- Shadowsocks SIP002: https://shadowsocks.org/doc/sip002.html
- YAML Go: https://pkg.go.dev/gopkg.in/yaml.v3

## 首版兼容范围

| 协议 | Shadowrocket / 标准 URI | Loon | Egern |
|---|---|---|---|
| SS | SIP002，无插件/客户端专有开关 | AES-GCM、chacha20-ietf-poly1305 | 首版限 AES-128/256-GCM |
| Trojan | 标准 URI；专有参数未承诺 | TCP / WS | TCP / 独立 websocket 结构 |
| VMess | v2 分享 JSON；不输出 Reality/fingerprint 扩展 | TCP / WS；不支持 gRPC | TCP / WS / TLS gRPC；alterId 限 0 |
| VLESS | 标准 URI；实际客户端需验收 | TCP / WS，可表达的 Reality/Vision | TCP / WS / TLS gRPC，可表达的 Reality/Vision |
| HTTP / SOCKS5 | 基础 URI；不输出 SOCKS5 TLS URI | 基础 / TLS | 独立 http、https、socks5、socks5_tls 对象 |
| Hysteria2 | 基础 URI、salamander；不输出带宽字段 | 基础、混淆、下载带宽 | 基础、混淆、上传带宽 |
| TUIC | 未实现 | 未实现 | 基础字段、ALPN、UDP relay mode；未知扩展拒绝 |

Clash 目标保留节点字段；以上表格只描述首版已实现组合，不等于对应客户端的全部能力。端口跳跃、其他 WS headers（Loon/Trojan 部分目标）、未映射指纹等会明确拒绝。Shadowrocket 的原生专有格式文档不完整，因此采用标准 URI/Base64，不能宣称所有参数已完成客户端验收。

## 本轮验证结果

- `go vet ./...`、`go build ./...` 通过。
- `go run . --check-refresh-config config/refresh-jobs.json` 通过，原有 75 个任务有效。
- 最终 `go test -race -count=1 ./...` 全部通过，包含新增 manager/subscription 及现有全部模块。
- 浏览器使用本地临时数据验证创建/保存、Clash/Loon/Egern 导出、分享 200、撤销后 404、文本中的 script 不执行；重启后资源仍可读。导出格式切换会清除旧预览，避免下载错格式。
- 磁盘预算、原子更新失败保留旧数据、token 跨资源使用、缓存命中时撤销、同 key 并发合并/取消、真实 server SSRF 拦截均有测试。
- `.env` 和默认私有数据目录不进入 Git 或 Docker 构建上下文；`.env.example` 不含有效密钥。
- 本轮没有使用真实机场订阅、没有在客户端导入节点、没有测试实际握手和连通性，也没有在 Fly 上测量新模块的内存/并发上限。这些属于后续实际环境验收。

## 管理界面适配（v1.1.1-beta）

2026-10-20：按 Sub-Store Front-End 的主题、容器断点、导航、资源卡片及编辑表单重做管理页。原版素材与许可记录在 `internal/manager/UI-NOTICE.md`。界面仅覆盖当前后端支持的功能，不表示已经实现原版全部页面或接口。

静态 HTML、CSS、JavaScript 和图标均由 Go 内嵌，不需要 Node 运行服务。浏览器仅保存外观偏好，不保存 Backend Key、订阅内容或分享 token。新增 CSS/JS/SVG 文件变更均会触发镜像工作流。

当前验证：`go build`、`go vet ./...`、`go test -race -count=1 ./...`、`node --check internal/manager/management.js` 均通过。浏览器验证本地订阅/文件创建、编辑、Clash/Loon/Egern 导出、分享读取/撤销、筛选与重命名、文本脚本不执行、390px 无横向溢出和桌面日夜主题。没有使用真实订阅凭据，本轮界面改动按 `v1.1.1-beta` 发布并通过 GitHub Actions 镜像部署。


## 1.1.2-beta 优化

完成源文件磁盘缓存及 304 更新、同源下载合并、单快照多目标后台刷新、有界过期回退、聚合指标与持久状态、每日/手动有界快照、完整校验后原子恢复、If-Match 编辑冲突保护。具体配置、接口和恢复语义以 README 的 1.1.2-beta 章节为准。

本地模拟 1000 节点，36 个同时请求分布到 Shadowrocket/Loon/Egern，全部 200；上游正文只下载一次。刷新全部目标仅增加一次 304 请求；采样峰值 RSS 34.25 MiB。该数据属于本机模拟，不代表 Fly 并发容量。浏览器验证启用自动刷新、三目标后台成功和 nextRun、409 保留草稿/加载最新版本、快照创建与上传恢复。测试数据均为虚构节点。

最终补充并通过强刷与普通缓存缺失请求合并的回归测试，确保强刷仍执行源验证。全量 `go test -race -count=1 ./...`、`go vet ./...`、编译、原有 75 个任务配置验证及 Fly 配置验证均通过。
