# 多阶段构建：scratch 零基础层镜像
# 用法：
#   docker build -t script-hub .
#   docker run -p 9100:9100 script-hub
#
# 多架构：
#   docker buildx build --platform linux/amd64,linux/arm64 -t script-hub .
#
# 环境变量：
#   PORT                主端口（默认 9100）
#   HOST                监听地址（默认 0.0.0.0）
#   HTTP_TIMEOUT        单次上游 fetch 超时秒数（默认 20）
#   REQUEST_TIMEOUT     单次转换请求总超时秒数（默认 60）
#   PARSER_BODY_MAX     响应体上限 KB（默认 600）
#   CACHE_TTL_SECONDS   转换结果缓存 TTL 秒数（默认 0=禁用）
#   SSRF_BLOCK_PRIVATE  拦截私有地址请求（默认 true；设 false 放行内网抓取）

# --- 阶段 1: 编译 ---
FROM golang:1.25-alpine AS builder

WORKDIR /build

COPY go.mod go.sum ./
RUN go mod download

COPY . .

ARG TARGETOS TARGETARCH VERSION=dev
RUN CGO_ENABLED=0 GOOS=${TARGETOS} GOARCH=${TARGETARCH} \
    go build -ldflags="-s -w -X main.version=${VERSION}" -trimpath -o /script-hub .

# --- 阶段 2: 运行（scratch 零基础层，~10MB 最终镜像） ---
# 健康检查：scratch 无 shell/curl，不使用 Docker HEALTHCHECK；
# 依赖部署平台的 HTTP 探针（如 fly.toml 的 [[http_service.checks]]）探测 /healthz。
FROM scratch

COPY --from=builder /etc/ssl/certs/ca-certificates.crt /etc/ssl/certs/
COPY --from=builder /script-hub /script-hub

EXPOSE 9100

ENTRYPOINT ["/script-hub"]
