package main

import (
	"context"
	"errors"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/sanyamgarg/airpipe/internal/relay"
)

// buildVersion is set via -ldflags at build time.
var buildVersion = "dev"

func main() {
	cfg := relay.LoadConfig()
	log := relay.NewLogger(cfg.LogFormat)

	rootCtx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	s, err := relay.New(rootCtx, cfg, log, buildVersion)
	if err != nil {
		log.Error("relay init failed", "err", err)
		os.Exit(1)
	}
	defer s.Shutdown()

	srv := &http.Server{
		Addr:              ":" + cfg.Port,
		Handler:           s.Routes(),
		ReadHeaderTimeout: 10 * time.Second,
	}

	go func() {
		log.Info("relay starting",
			"port", cfg.Port,
			"version", buildVersion,
			"allowed_origins", cfg.AllowedOrigins,
			"allow_any_origin", cfg.AllowAnyOrigin,
			"rate_limit_per_min", cfg.RateLimitPerMin,
			"max_upload_bytes", cfg.MaxUploadBytes,
			"file_expiry", cfg.FileExpiry.String(),
		)
		if err := srv.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
			log.Error("listen failed", "err", err)
			os.Exit(1)
		}
	}()

	<-rootCtx.Done()
	log.Info("shutdown signal received")

	shutdownCtx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	if err := srv.Shutdown(shutdownCtx); err != nil {
		log.Error("shutdown error", "err", err)
	}
	log.Info("shutdown complete")
}
