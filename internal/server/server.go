package server

import (
	"context"
	"net/http"

	"github.com/go-chi/chi/v5"
	"github.com/script-hub-org/script-hub/internal/config"
)

type Server struct {
	httpServer *http.Server
	router     *chi.Mux
	cfg        *config.Config
	isBeta     bool
}

func New(cfg *config.Config) *Server {
	return newServer(cfg, false)
}

// NewBeta creates a Beta server instance that serves the beta frontend and
// beta module files, mirroring the JS BETA_PORT dual-service mode.
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

func (s *Server) Start(addr string) error {
	s.httpServer = &http.Server{
		Addr:    addr,
		Handler: s.router,
	}
	return s.httpServer.ListenAndServe()
}

func (s *Server) Shutdown(ctx context.Context) error {
	if s.httpServer != nil {
		return s.httpServer.Shutdown(ctx)
	}
	return nil
}
