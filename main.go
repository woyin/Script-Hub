// Input: context, fmt, log, net/http, os, os/signal, syscall, time, internal/config, internal/server
// Output: func main(), var version
// Pos: 入口层-程序启动，加载配置并管理 HTTP 服务生命周期
//
// 本注释在文件修改时自动更新，同时触发 FOLDER_INDEX 和 PROJECT_INDEX 更新

// Script Hub — 代理规则与脚本转换服务（Go 重写版）
//
// 本程序是 Script Hub 的 Go 语言重写版本，完整还原了原始 Node.js 版本的所有功能：
//   - 重写转换（QX → Surge/Loon/Stash/Shadowrocket）
//   - 规则集转换
//   - 脚本转换（QX 脚本 → 目标平台脚本）
//   - 前端 Web UI
//
// 启动方式：
//
//	直接运行即可，默认监听 0.0.0.0:9100
//	环境变量配置参见 internal/config/config.go
package main

import (
	"context"
	"fmt"
	"log"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/script-hub-org/script-hub/internal/config"
	"github.com/script-hub-org/script-hub/internal/server"
)

// version 通过 -ldflags -X main.version=... 注入，未设置时为 dev
var version = "dev"

func main() {
	cfg := config.LoadConfig()

	// 将 ldflags 注入的版本号同步到 config 包，供 /version 端点使用
	config.SetVersion(version)
	cfg.Version = version

	log.Printf("Script Hub %s", version)

	// ── HTTP 服务模式 ──
	srv := server.New(cfg)

	stop := make(chan os.Signal, 1)
	signal.Notify(stop, os.Interrupt, syscall.SIGTERM)

	go func() {
		addr := fmt.Sprintf("%s:%s", cfg.Host, cfg.Port)
		log.Printf("Script Hub 启动于 %s", addr)
		if err := srv.Start(addr); err != nil && err != http.ErrServerClosed {
			log.Fatalf("服务启动失败: %v", err)
		}
	}()

	<-stop
	log.Println("正在关闭服务...")

	shutdownCtx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	if err := srv.Shutdown(shutdownCtx); err != nil {
		log.Printf("服务关闭错误: %v", err)
	}

	log.Println("服务已停止。")
}
