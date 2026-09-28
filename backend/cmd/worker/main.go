package main

import (
	"context"
	"flag"
	"fmt"
	"log/slog"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/vincommerce/backend/internal/config"
	"github.com/vincommerce/backend/internal/db"
	"github.com/vincommerce/backend/internal/mail"
	"github.com/vincommerce/backend/internal/repository"
	"github.com/vincommerce/backend/internal/service"
	"github.com/vincommerce/backend/internal/worker"
)

// healthcheckTimeout bounds the container HEALTHCHECK probe. It is shorter
// than the Dockerfile's `--timeout=10s` so a hung Redis is reported as a
// failed probe rather than being killed mid-write by Docker.
const healthcheckTimeout = 8 * time.Second

// Backlog threshold at which the worker is reported unhealthy. A sustained
// oldest-task age this far past the scheduler's 5-minute expiry sweep means
// tasks are being accepted and not executed — the "looks healthy, does
// nothing" failure mode a plain PING check cannot see.
const backlogSeconds = 30 * 60

func main() {
	healthcheck := flag.Bool("healthcheck", false,
		"probe the task queue and exit 0 (healthy) or 1 (unhealthy); used by the container HEALTHCHECK")
	flag.Parse()

	logger := slog.New(slog.NewJSONHandler(os.Stdout, nil))
	slog.SetDefault(logger)

	if *healthcheck {
		if err := runHealthcheck(); err != nil {
			// Written to stderr, not the JSON logger: Docker surfaces the
			// healthcheck output verbatim in `docker inspect`, and a single
			// plain line is far more readable there than a log envelope.
			fmt.Fprintf(os.Stderr, "worker healthcheck: %v\n", err)
			os.Exit(1)
		}
		os.Exit(0)
	}

	if err := run(logger); err != nil {
		logger.Error("worker fatal", "error", err)
		os.Exit(1)
	}
}

// runHealthcheck reports whether the worker can actually do its job.
//
// Two failures matter, and both are invisible to a PING:
//
//  1. The queue is unreachable — the signature of a Redis credential or DB
//     mismatch, which otherwise stops every scheduled job with nothing logged.
//  2. The queue is reachable but not draining — tasks are accepted and never
//     executed, so the backlog age grows without bound.
//
// A queue with zero registered queues is reported as HEALTHY: a freshly created
// Redis has none until the scheduler enqueues its first periodic task, and
// failing on that would mark every freshly-deployed worker unhealthy and flap
// it. The warning still goes to stderr, so it is visible in `docker inspect`.
func runHealthcheck() error {
	// Redis only. Deliberately NOT config.Load(): that runs Validate(), which
	// rejects secrets the worker never uses, so a healthy queue would be
	// reported unhealthy because JWT_SECRET is absent from this container.
	redisCfg, err := config.LoadRedis()
	if err != nil {
		return err
	}

	ctx, cancel := context.WithTimeout(context.Background(), healthcheckTimeout)
	defer cancel()

	stats, err := worker.CheckQueue(ctx, redisCfg)
	if err != nil {
		return fmt.Errorf("queue unreachable at %s: %w", redisCfg.Addr, err)
	}
	if len(stats) == 0 {
		fmt.Fprintln(os.Stderr, "worker healthcheck: queue reachable but empty; no registered queues yet")
		return nil
	}

	var oldest int64
	for _, q := range stats {
		if q.OldestSecs > oldest {
			oldest = q.OldestSecs
		}
	}
	if oldest > backlogSeconds {
		return fmt.Errorf("queue backlog: oldest task is %ds old (limit %ds)", oldest, backlogSeconds)
	}
	return nil
}

func run(logger *slog.Logger) error {
	cfg, err := config.Load()
	if err != nil {
		return err
	}

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	pool, err := db.Connect(ctx, cfg.Database)
	if err != nil {
		return err
	}
	defer pool.Close()

	carts := repository.NewCartRepository(pool)
	orders := repository.NewOrderRepository(pool)
	addresses := repository.NewAddressRepository(pool)
	orderSvc := service.NewOrderService(carts, orders, addresses)

	marketRepo := repository.NewQARepository(pool)
	notifRepo := repository.NewNotificationRepository(pool)
	marketSvc := service.NewMarketService(marketRepo)
	marketSvc.SetNotificationService(service.NewNotificationService(notifRepo))

	users := repository.NewUserRepository(pool)
	products := repository.NewProductRepository(pool)
	stores := repository.NewStoreRepository(pool)
	paymentRepo := repository.NewPaymentRepository(pool)
	sellerSvc := service.NewSellerService(stores, users, products, orders, paymentRepo)
	sellerSvc.SetMailer(mail.NewClient(mail.Config{Host: cfg.SMTP.Host, Port: cfg.SMTP.Port, From: cfg.SMTP.From, Username: cfg.SMTP.Username, Password: cfg.SMTP.Password, UseTLS: false}), cfg.App.WebURL)
	sellerSvc.SetNotificationService(service.NewNotificationService(notifRepo))

	sessions := repository.NewSessionRepository(pool)
	srv, err := worker.NewServerWithSessions(cfg.Redis, orderSvc, marketSvc, sellerSvc, sessions, logger)
	if err != nil {
		return err
	}

	logger.Info("worker starting", "env", cfg.Environment)
	return srv.Start(ctx)
}
