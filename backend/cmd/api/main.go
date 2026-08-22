package main

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/vincommerce/backend/internal/cache"
	"github.com/vincommerce/backend/internal/config"
	"github.com/vincommerce/backend/internal/db"
	"github.com/vincommerce/backend/internal/httpapi"
	"github.com/vincommerce/backend/internal/mail"
	"github.com/vincommerce/backend/internal/metrics"
)

func main() {
	logger := slog.New(slog.NewJSONHandler(os.Stdout, &slog.HandlerOptions{Level: slog.LevelInfo}))
	slog.SetDefault(logger)

	if err := run(logger); err != nil {
		logger.Error("fatal", "error", err)
		os.Exit(1)
	}
}

func run(logger *slog.Logger) error {
	cfg, err := config.Load()
	if err != nil {
		return err
	}
	if cfg.Environment == "production" && (len(cfg.Auth.JWTSecret) < 32 || cfg.Auth.JWTSecret == "dev-secret-change-me") {
		return fmt.Errorf("refusing to start: JWT_SECRET must be a strong unique value (32+ chars) in production")
	}

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	pool, err := db.Connect(ctx, cfg.Database)
	if err != nil {
		return err
	}
	defer pool.Close()
	logger.Info("database connected", "name", cfg.Database.Name)

	if cfg.Database.MigrateOnStart {
		if err := db.Migrate(ctx, cfg.Database); err != nil {
			return err
		}
		logger.Info("migrations applied")
	}

	rdb, err := cache.Connect(ctx, cfg.Redis)
	if err != nil {
		return err
	}
	defer rdb.Close()
	logger.Info("redis connected", "addr", cfg.Redis.Addr)

	mailer := mail.NewClient(mail.Config{
		Host:     cfg.SMTP.Host,
		Port:     cfg.SMTP.Port,
		From:     cfg.SMTP.From,
		Username: cfg.SMTP.Username,
		Password: cfg.SMTP.Password,
	})
	if mailer.IsConfigured() {
		logger.Info("mail client configured", "host", cfg.SMTP.Host)
	}

	srv := &http.Server{
		Addr:              fmt.Sprintf(":%d", cfg.Port),
		Handler:           httpapi.NewRouter(httpapi.Dependencies{Pool: pool, Redis: rdb, Config: cfg, Logger: logger, Mailer: mailer, Metrics: metrics.New()}),
		ReadHeaderTimeout: 10 * time.Second,
		IdleTimeout:       120 * time.Second,
	}

	errCh := make(chan error, 1)
	go func() {
		logger.Info("api listening", "addr", srv.Addr, "env", cfg.Environment)
		if err := srv.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
			errCh <- err
		}
	}()

	select {
	case err := <-errCh:
		return err
	case <-ctx.Done():
		logger.Info("shutting down")
		shutdownCtx, cancel := context.WithTimeout(context.Background(), cfg.ShutdownGap)
		defer cancel()
		return srv.Shutdown(shutdownCtx)
	}
}
