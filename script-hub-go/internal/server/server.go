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
}

func New(cfg *config.Config) *Server {
	r := chi.NewRouter()
	s := &Server{
		router: r,
		cfg:    cfg,
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
