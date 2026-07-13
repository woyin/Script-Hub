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
//	直接运行即可，默认监听 0.0.0.0:9100（正式）和 0.0.0.0:9101（beta）
//	环境变量配置参见 internal/config/config.go
package main

import (
	"context"
	"fmt"
	"log"
	"net/http"
	"os"
	"os/signal"
	"path/filepath"
	"syscall"
	"time"

	"github.com/script-hub-org/script-hub/internal/config"
	"github.com/script-hub-org/script-hub/internal/frontend"
	"github.com/script-hub-org/script-hub/internal/server"
)

// version 通过 -ldflags -X main.version=... 注入，未设置时为 dev
var version = "dev"

func main() {
	cfg := config.LoadConfig()

	log.Printf("Script Hub %s", version)

	// ── 静态导出模式 ──
	// 设置 EXPORT_HTML 目录路径时，仅导出 HTML 文件后退出，不启动 HTTP 服务。
	// 与 JS 版 preview.js 的 EXPORT_HTML 功能对齐。
	if cfg.ExportHTML != "" {
		exportDir := cfg.ExportHTML
		betaDir := filepath.Join(exportDir, "beta")
		if err := os.MkdirAll(betaDir, 0755); err != nil {
			log.Fatalf("创建导出目录失败: %v", err)
		}
		mainHTML := frontend.GenerateHTML(cfg.BaseURL)
		betaHTML := frontend.GenerateHTML(cfg.BetaBaseURL)
		if err := os.WriteFile(filepath.Join(exportDir, "index.html"), []byte(mainHTML), 0644); err != nil {
			log.Fatalf("写入 index.html 失败: %v", err)
		}
		if err := os.WriteFile(filepath.Join(betaDir, "index.html"), []byte(betaHTML), 0644); err != nil {
			log.Fatalf("写入 beta/index.html 失败: %v", err)
		}
		log.Printf("HTML 已导出至 %s", exportDir)
		return
	}

	// ── HTTP 服务模式 ──
	// 正式服务与 Beta 服务双端口运行，对齐 JS 版的 PORT / BETA_PORT 双服务模式。
	srv := server.New(cfg)
	betaSrv := server.NewBeta(cfg)

	stop := make(chan os.Signal, 1)
	signal.Notify(stop, os.Interrupt, syscall.SIGTERM)

	go func() {
		addr := fmt.Sprintf("%s:%s", cfg.Host, cfg.Port)
		log.Printf("Script Hub 正式服务启动于 %s, BASE URL: %s", addr, cfg.BaseURL)
		if err := srv.Start(addr); err != nil && err != http.ErrServerClosed {
			log.Fatalf("服务启动失败: %v", err)
		}
	}()

	// Beta 服务镜像 JS 版 BETA_PORT 双服务模式
	go func() {
		addr := fmt.Sprintf("%s:%s", cfg.Host, cfg.BetaPort)
		log.Printf("Script Hub (beta) 服务启动于 %s, BETA BASE URL: %s", addr, cfg.BetaBaseURL)
		if err := betaSrv.Start(addr); err != nil && err != http.ErrServerClosed {
			log.Fatalf("Beta 服务启动失败: %v", err)
		}
	}()

	<-stop
	log.Println("正在关闭服务...")

	shutdownCtx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	if err := srv.Shutdown(shutdownCtx); err != nil {
		log.Printf("正式服务关闭错误: %v", err)
	}
	if err := betaSrv.Shutdown(shutdownCtx); err != nil {
		log.Printf("Beta 服务关闭错误: %v", err)
	}

	log.Println("服务已停止。")
}
