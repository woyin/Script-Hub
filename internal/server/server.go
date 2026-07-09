// Package server 实现 Script Hub 的 HTTP 服务层。
// 使用 chi 路由器，提供与 JS 版 service.js / preview.js 完全对齐的路由逻辑。
package server

import (
	"context"
	"net/http"

	"github.com/go-chi/chi/v5"
	"github.com/script-hub-org/script-hub/internal/config"
)

// Server 是 Script Hub HTTP 服务的核心结构。
type Server struct {
	httpServer *http.Server
	router     *chi.Mux
	cfg        *config.Config
	isBeta     bool // 是否为 Beta 服务实例
}

// New 创建正式服务实例。
func New(cfg *config.Config) *Server {
	return newServer(cfg, false)
}

// NewBeta 创建 Beta 服务实例，镜像 JS 版 BETA_PORT 双服务模式。
func NewBeta(cfg *config.Config) *Server {
	return newServer(cfg, true)
}

func newServer(cfg *config.Config, isBeta bool) *Server {
	r := chi.NewRouter()
	s := &Server{
		router: r,
		cfg:    cfg,
		isBeta: isBeta,
	}
	s.setupRoutes()
	return s
}

// Start 在指定地址启动 HTTP 监听。
func (s *Server) Start(addr string) error {
	s.httpServer = &http.Server{
		Addr:    addr,
		Handler: s.router,
	}
	return s.httpServer.ListenAndServe()
}

// Shutdown 优雅关闭 HTTP 服务。
func (s *Server) Shutdown(ctx context.Context) error {
	if s.httpServer != nil {
		return s.httpServer.Shutdown(ctx)
	}
	return nil
}
