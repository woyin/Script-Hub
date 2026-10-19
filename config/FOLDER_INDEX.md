# config 文件夹索引

## 架构说明
用户维护的后台规则刷新清单，构建镜像时复制到 `/config`。
启动前严格校验，修改后重新部署，不通过 HTTP 暴露配置。

## 文件清单

### refresh-jobs.json
- 包含从用户 Shadowrocket 清单提取的 25 个远程源，每源配置 Loon、Shadowrocket、Egern，共 75 个启用任务，间隔为 21600 秒（6 小时）。内联 localtext 规则不加入后台刷新。
- `refreshJobs` 数组中填写 id、enabled、intervalSeconds 和 request。
- request 的 type 固定为 rule-set；target 必须明确；urls 为解码后的源 URL；args 值均为字符串。
- 校验：`go run . --check-refresh-config config/refresh-jobs.json`。
- 完整字段说明及权限约定见 README.md。
