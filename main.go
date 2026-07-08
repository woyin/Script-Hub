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

func main() {
	cfg := config.LoadConfig()

	if cfg.ExportHTML != "" {
		exportDir := cfg.ExportHTML
		betaDir := filepath.Join(exportDir, "beta")
		if err := os.MkdirAll(betaDir, 0755); err != nil {
			log.Fatalf("Failed to create export directory: %v", err)
		}
		mainHTML := frontend.GenerateHTML(cfg.BaseURL)
		betaHTML := frontend.GenerateHTML(cfg.BetaBaseURL)
		if err := os.WriteFile(filepath.Join(exportDir, "index.html"), []byte(mainHTML), 0644); err != nil {
			log.Fatalf("Failed to write index.html: %v", err)
		}
		if err := os.WriteFile(filepath.Join(betaDir, "index.html"), []byte(betaHTML), 0644); err != nil {
			log.Fatalf("Failed to write beta/index.html: %v", err)
		}
		log.Printf("Exported HTML to %s", exportDir)
		return
	}

	srv := server.New(cfg)
	betaSrv := server.NewBeta(cfg)

	stop := make(chan os.Signal, 1)
	signal.Notify(stop, os.Interrupt, syscall.SIGTERM)

	go func() {
		addr := fmt.Sprintf("%s:%s", cfg.Host, cfg.Port)
		log.Printf("Script Hub listening on %s, BASE URL: %s", addr, cfg.BaseURL)
		if err := srv.Start(addr); err != nil && err != http.ErrServerClosed {
			log.Fatalf("Server error: %v", err)
		}
	}()

	// Beta service mirrors the JS BETA_PORT dual-service mode.
	go func() {
		addr := fmt.Sprintf("%s:%s", cfg.Host, cfg.BetaPort)
		log.Printf("Script Hub (beta) listening on %s, BETA BASE URL: %s", addr, cfg.BetaBaseURL)
		if err := betaSrv.Start(addr); err != nil && err != http.ErrServerClosed {
			log.Fatalf("Beta server error: %v", err)
		}
	}()

	<-stop
	log.Println("Shutting down...")

	shutdownCtx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	if err := srv.Shutdown(shutdownCtx); err != nil {
		log.Printf("Server shutdown error: %v", err)
	}
	if err := betaSrv.Shutdown(shutdownCtx); err != nil {
		log.Printf("Beta server shutdown error: %v", err)
	}

	log.Println("Server stopped.")
}
