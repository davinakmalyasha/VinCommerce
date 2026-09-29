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

	"github.com/vincommerce/backend/internal/app"
	"github.com/vincommerce/backend/internal/cache"
	"github.com/vincommerce/backend/internal/config"
	"github.com/vincommerce/backend/internal/db"
	"github.com/vincommerce/backend/internal/mail"
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

	// Redis is required, not optional, for this process: it is the task queue.
	// The healthcheck already refuses to report healthy when it is unreachable,
	// so failing at boot too means a credential mismatch is a crash with a
	// readable reason rather than a container that starts and silently runs no
	// jobs.
	rdb, err := cache.Connect(ctx, cfg.Redis)
	if err != nil {
		return fmt.Errorf("worker requires redis for its task queue: %w", err)
	}
	defer rdb.Close()

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

	// The SAME composition root cmd/api uses. This used to be a hand-rolled
	// second copy that applied 3 of the router's 49 setter calls, and because the
	// wiring was setter-based rather than constructor-based, every omitted
	// dependency was a nil field guarded by `if s.x == nil` rather than a
	// compile error. The consequences, all of them silent:
	//
	//   payments    nil -> AdvanceShippedOrders never captured COD
	//   loyalty     nil -> CompleteDelivered never awarded a point
	//   broker      nil -> no job published an SSE event
	//   notifs      nil -> no job wrote an in-app notification
	//   mailer      nil -> RecoverAbandonedCarts returned (0, nil) forever, so
	//                     the cart-recovery job reported SUCCESS every 30
	//                     minutes and sent no email, ever
	//   logger      nil -> the log line that would have shown the above was
	//                     the one line that was skipped
	//
	// Four of those produce a green dashboard and no work done. Now there is
	// one graph, and a service that gains a dependency gains it in both
	// processes.
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

	srv, err := worker.NewServerWithSessions(
		cfg.Redis,
		graph.Services.Order,
		graph.Services.Market,
		graph.Services.Seller,
		graph.Repositories.Sessions,
		logger,
	)
	if err != nil {
		return err
	}

	logger.Info("worker starting", "env", cfg.Environment)
	return srv.Start(ctx)
}
