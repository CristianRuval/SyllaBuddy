package main

import (
	"context"
	"errors"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"path/filepath"
	"syscall"
	"time"

	"syllabuddy/internal/auth"
	"syllabuddy/internal/config"
	"syllabuddy/internal/db"
	"syllabuddy/internal/gemini"
	"syllabuddy/internal/handlers"
	"syllabuddy/internal/logger"
)

func main() {
	cfg, err := config.Load()
	if err != nil {
		slog.Error("configuration error", slog.String("error", err.Error()))
		os.Exit(1)
	}

	log := logger.New(cfg.Env)
	log.Info("starting syllabuddy server", slog.Any("config", cfg))

	if err := ensureUploadDirs(cfg.UploadDir); err != nil {
		log.Error("failed to initialize upload directories", slog.String("error", err.Error()))
		os.Exit(1)
	}

	initCtx, initCancel := context.WithTimeout(context.Background(), 15*time.Second)
	database, err := db.Connect(initCtx, cfg.DatabaseURL)
	if err != nil {
		initCancel()
		log.Error("database connection failed", slog.String("error", err.Error()))
		os.Exit(1)
	}
	if err := database.Migrate(initCtx); err != nil {
		initCancel()
		database.Close()
		log.Error("database migration failed", slog.String("error", err.Error()))
		os.Exit(1)
	}
	initCancel()

	authMgr := auth.NewManager(cfg.SecretKey, cfg.AccessTokenTTL(), cfg.CookieSecure, cfg.GoogleClientID)
	geminiParser := gemini.NewParser(cfg.GeminiAPIKey)

	srv, err := handlers.NewServer(cfg, log, database, authMgr, geminiParser)
	if err != nil {
		database.Close()
		log.Error("server initialization failed", slog.String("error", err.Error()))
		os.Exit(1)
	}

	httpServer := &http.Server{
		Addr:              "0.0.0.0:" + cfg.Port,
		Handler:           srv.Routes(),
		ReadHeaderTimeout: 10 * time.Second,
		ReadTimeout:       60 * time.Second,
		WriteTimeout:      90 * time.Second,
		IdleTimeout:       120 * time.Second,
	}

	sigCtx, stopSignals := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stopSignals()

	errCh := make(chan error, 1)
	go func() {
		log.Info("http server listening", slog.String("addr", httpServer.Addr))
		if serveErr := httpServer.ListenAndServe(); serveErr != nil && !errors.Is(serveErr, http.ErrServerClosed) {
			errCh <- serveErr
			return
		}
		errCh <- nil
	}()

	select {
	case <-sigCtx.Done():
		log.Info("shutdown signal received")
	case serveErr := <-errCh:
		if serveErr != nil {
			log.Error("http server error", slog.String("error", serveErr.Error()))
		}
	}

	shutdownCtx, shutdownCancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer shutdownCancel()

	if err := httpServer.Shutdown(shutdownCtx); err != nil {
		log.Error("http server shutdown error", slog.String("error", err.Error()))
	}
	if err := srv.Shutdown(shutdownCtx); err != nil {
		log.Error("background worker shutdown error", slog.String("error", err.Error()))
	}
	database.Close()
	log.Info("syllabuddy server stopped cleanly")
}

func ensureUploadDirs(uploadDir string) error {
	if err := os.MkdirAll(uploadDir, 0o750); err != nil {
		return err
	}
	return os.MkdirAll(filepath.Join(uploadDir, "thumbs"), 0o750)
}
