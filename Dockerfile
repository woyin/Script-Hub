# 多阶段构建：scratch 零基础层镜像
# 用法：
#   docker build -t script-hub .
#   docker run -p 9100:9100 script-hub
#
# 多架构：
#   docker buildx build --platform linux/amd64,linux/arm64 -t script-hub .
#
# 环境变量：
#   PORT        主端口（默认 9100）
#   HOST        监听地址（默认 0.0.0.0）
#   HTTP_TIMEOUT 请求超时秒数（默认 20）

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
FROM scratch

COPY --from=builder /etc/ssl/certs/ca-certificates.crt /etc/ssl/certs/
COPY --from=builder /script-hub /script-hub

EXPOSE 9100

ENTRYPOINT ["/script-hub"]
