# Script Hub (Go)

规则集与重写配置格式转换服务。

## 产品边界

保留：

- QX Rewrite、Surge Module、Loon Plugin 自动识别与互转
- 输出 Surge Module、Loon Plugin、Stash Override、Shadowrocket Module
- 常见规则集转换为 Surge、Loon、Stash、Shadowrocket 规则集或域名集
- 远程 HTTP(S) URL和本地粘贴内容输入
- 简单 Web UI、Docker 镜像、跨平台 Release

不提供：

- JavaScript 运行时兼容转换
- 任意 JavaScript `eval`
- HTTP mock、内容代理、subconverter
- 静态 HTML 导出、客户端重载等部署辅助功能

脚本声明会转换为目标配置语法，但脚本 URL 和脚本内容保持原样。脚本自身必须兼容目标客户端。

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

## 环境变量

| 变量 | 默认值 | 说明 |
|---|---:|---|
| `PORT` | `9100` | HTTP 端口 |
| `HOST` | `0.0.0.0` | 监听地址 |
| `HTTP_TIMEOUT` | `20` | 获取远程输入超时秒数 |
| `PARSER_BODY_MAX` | `600` | 解析器内容上限 KB |

## API

兼容原有规则转换 URL：

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
surge-rule-set
loon-rule-set
stash-rule-set
shadowrocket-rule-set
surge-domain-set
stash-domain-set
```

## 开发检查

```bash
go test ./...
go vet ./...
```

## License

[GNU General Public License v3.0](LICENSE)
