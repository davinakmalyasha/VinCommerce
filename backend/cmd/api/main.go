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

	"github.com/hibiken/asynq"
	"github.com/vincommerce/backend/internal/app"
	"github.com/vincommerce/backend/internal/cache"
	"github.com/vincommerce/backend/internal/config"
	"github.com/vincommerce/backend/internal/db"
	"github.com/vincommerce/backend/internal/httpapi"
	"github.com/vincommerce/backend/internal/httpapi/handler"
	"github.com/vincommerce/backend/internal/mail"
	"github.com/vincommerce/backend/internal/metrics"
	"github.com/vincommerce/backend/internal/worker"
)

// version is injected at build time via -ldflags "-X main.version=<git sha>".
var version = "dev"

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
	// config.Load already enforces the production guards (JWT secret strength,
	// sslmode, gateway policy) for every binary and normalises APP_ENV; this
	// is a belt-and-braces re-check of the single highest-impact guard.
	if cfg.IsProd() && config.IsWeakSecret(cfg.Auth.JWTSecret) {
		return fmt.Errorf("refusing to start: JWT_SECRET must be a strong unique value (32+ chars, high entropy) in %s", cfg.Environment)
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

	// One composition root, shared with cmd/worker. Building the graph here
	// rather than inside NewRouter is what makes it impossible for the two
	// processes to end up wired differently -- which is how the worker came to
	// have no payment service, no loyalty ledger, no broker, no notification
	// service and no mailer, and therefore a cart-recovery job that reported
	// success and sent nothing.
	graph, err := app.Build(ctx, app.Deps{
		Pool:   pool,
		Redis:  rdb.Client,
		Config: cfg,
		Logger: logger,
		Mailer: mailer,
	})
	if err != nil {
		return err
	}

	// DEFER TRANSACTIONAL EMAIL TO THE WORKER.
	//
	// Checkout used to wait on the mail server: `mail.Client.Send` is one SMTP
	// round trip per call and the order path makes up to three, so a slow SMTP
	// made the request slow without failing it -- the harder case to notice.
	// The producer is built HERE, in the process that creates the work, because
	// that is the process whose request was waiting.
	//
	// Best-effort by construction: if the queue is unreachable the enqueue fails
	// at call time and the service sends inline instead. A queue that is down
	// should cost a slow request, never a missing receipt.
	queueClient := asynq.NewClient(asynq.RedisClientOpt{
		Addr:     cfg.Redis.Addr,
		Password: cfg.Redis.Password,
		DB:       cfg.Redis.DB,
	})
	defer queueClient.Close()
	graph.Services.Order.SetEmailEnqueuer(worker.NewEmailEnqueuer(queueClient))
	logger.Info("transactional email deferred to the worker queue")

	srv := &http.Server{
		Addr: fmt.Sprintf(":%d", cfg.Port),
		Handler: httpapi.NewRouter(httpapi.Dependencies{
			App:     graph,
			Pool:    pool,
			Redis:   rdb,
			Config:  cfg,
			Logger:  logger,
			Mailer:  mailer,
			Metrics: metrics.New(),
		}),
		ReadHeaderTimeout: 10 * time.Second,
		IdleTimeout:       120 * time.Second,
	}
	logger.Info("build", "version", version)
	handler.Version = version

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
		if err := srv.Shutdown(shutdownCtx); err != nil {
			// Long-lived SSE connections never become idle, so Shutdown
			// regularly hits its deadline during deploys. Draining was
			// attempted; treat it as a clean exit so orchestrators don't
			// count every deploy as a crash.
			logger.Warn("shutdown deadline reached with active streams", "error", err.Error())
		}
		return nil
	}
}
