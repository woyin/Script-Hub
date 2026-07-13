<div align="center">
<br>
<img width="200" src="https://raw.githubusercontent.com/Script-Hub-Org/Script-Hub/main/assets/icon-dark.png" alt="Script Hub">
<br>
<br>
<h1>Script Hub (Go)</h1>
</div>

<p align="center">
Advanced Script Converter for QX, Loon, Surge, Stash, Egern, LanceX and Shadowrocket
</p>
<p align="center">
重写 & 规则集转换 — Go 高性能重写版
</p>

## 简介

Script Hub (Go) 是 [Script Hub](https://github.com/Script-Hub-Org/Script-Hub) 的 Go 重写版本，编译为单一二进制文件，使用 scratch 基础镜像部署时仅约 10MB。保持与原版 Node.js 实现的完整功能对齐。

- 将 QX 重写解析至 Surge / Shadowrocket / Loon / Stash
- 将 Surge 模块解析至 Loon / Stash
- 将 Loon 插件解析至 Surge / Shadowrocket / Stash
- QX / Surge / Loon / Shadowrocket / Clash 规则集解析
- QX 脚本转换为 Surge / Loon / Stash 脚本（兼容层）
- 支持修改参数 `argument`、`timeout`、`engine`、`cron` 等
- 支持一键导入 Shadowrocket / Loon / Stash
- 高级操作：OR / eval 修改任意文本
- AND / OR / NOT 逻辑规则的参数注入（extended-matching、no-resolve、pre-matching）
- 子转换器代理模式（subconverter）
- 纯文本输入 + 高级操作修改远程链接内容

## 快速开始

### 编译

需要 Go 1.22 或更高版本。

```bash
# 编译当前平台
go build -o script-hub .

# 交叉编译 (示例)
CGO_ENABLED=0 GOOS=linux GOARCH=amd64 go build -ldflags="-s -w" -trimpath -o script-hub .
```

### 运行

```bash
./script-hub
# 默认监听 http://0.0.0.0:9100
```

### Docker

直接拉取预构建镜像（推荐）：

```bash
docker pull ghcr.io/woyin/script-hub/script-hub:latest
docker run -p 9100:9100 ghcr.io/woyin/script-hub/script-hub:latest
```

或本地构建：

```bash
docker build -t script-hub .
docker run -p 9100:9100 script-hub
```

### 导出静态 HTML

不启动服务器，仅导出前端 HTML 文件用于静态部署：

```bash
EXPORT_HTML=./public ./script-hub
# 输出 public/index.html
```

## 环境变量

| 变量 | 默认值 | 说明 |
|------|--------|------|
| `PORT` | `9100` | 服务端口 |
| `HOST` | `0.0.0.0` | 监听地址 |
| `BASE_URL` | `https://127.0.0.1:PORT` | 仅静态导出模式需要，指定目标 URL |
| `HTTP_TIMEOUT` | `20` | HTTP 请求超时（秒） |
| `PARSER_BODY_MAX` | `600` | Mock 响应体最大 KB |
| `EXPORT_HTML` | *(空)* | 设置后导出静态 HTML 到指定目录 |

## 项目结构

```
.
├── main.go                      # 入口：服务器启动 / HTML 导出
├── Dockerfile                   # 多阶段构建（golang → scratch）
├── internal/
│   ├── config/                  # 环境变量配置
│   ├── server/                  # HTTP 路由与请求处理
│   ├── rewrite/                 # 重写规则解析与转换（QX/Surge/Loon → 各平台）
│   ├── rule/                    # 规则集解析与转换
│   ├── converter/               # QX 脚本转换（兼容层）
│   ├── eval/                    # eval 文本操作（正则提取 + goja JS 引擎回退）
│   ├── httpclient/              # HTTP 客户端封装
│   ├── frontend/                # 内嵌前端 HTML 生成
│   ├── env/                     # JS 环境模拟辅助
│   ├── scripts/                 # 内嵌脚本资源
│   └── types/                   # 共享类型定义
```

## 与原版的主要差异

| 方面 | Node.js 原版 | Go 重写版 |
|------|-------------|----------|
| 运行时 | Node.js | 单一静态二进制 |
| 镜像大小 | ~200MB (node:alpine) | ~10MB (scratch) |
| 前端嵌入 | 运行时读取文件 | 编译时 `embed.FS` 嵌入 |
| JS eval | 原生 `eval()` | 正则提取 + [goja](https://github.com/dop251/goja) 引擎回退 |
| 依赖 | npm 包管理 | Go modules，零外部运行时依赖 |

## 文档

安装体验与详细使用说明请查看 [项目 Wiki](https://github.com/Script-Hub-Org/Script-Hub/wiki)。

## 社群

欢迎加入社群进行交流讨论

- 群组：[张佩服(群组)](https://t.me/zhangpeifu) / [折腾啥(群组)](https://t.me/zhetengsha_group)
- 频道：[张佩服(频道)](https://t.me/h5683577) / [折腾啥(频道)](https://t.me/zhetengsha)

## 鸣谢

- Powered by [_@Chavy's_](https://github.com/chavyleung) [Env.js](https://github.com/chavyleung/scripts)
- 原脚本作者 @小白脸
- 脚本修改 [_@chengkongyiban_](https://github.com/chengkongyiban)
- 大量借鉴 [_@KOP-XIAO_](https://github.com/KOP-XIAO) 的 [resource-parser.js](https://github.com/KOP-XIAO/QuantumultX/raw/master/Scripts/resource-parser.js)
- 感谢 [_@xream_](https://github.com/xream) 与 [_@keywos_](https://github.com/keywos) 提供前端及脚本支持
- 感谢 [_@mieqq_](https://github.com/mieqq) 提供 [replace-body.js](https://github.com/mieqq/mieqq/raw/master/replace-body.js)
- 感谢 [_@Maasea_](https://github.com/Maasea) 的指导
- 项目 logo 感谢 [_@Toperlock_](https://github.com/Toperlock)
- 插件图标使用 [_@Keikinn_](https://github.com/Keikinn) 的 [StickerOnScreen](https://github.com/KeiKinn/StickerOnScreen) 及 [_@Toperlock_](https://github.com/Toperlock) 的 [QX 图标库](https://github.com/Toperlock/Quantumult/tree/main/icon)

## 赞助

支持我们的工作：[Patreon](https://www.patreon.com/scripthuborg)

## License

[GNU General Public License v3.0](LICENSE)

本项目为 [Script Hub](https://github.com/Script-Hub-Org/Script-Hub) 的 Go 语言重写版本。基于原项目 GPL v3 许可证的 copyleft 条款，本衍生作品同样采用 GPL v3 许可证发布。
