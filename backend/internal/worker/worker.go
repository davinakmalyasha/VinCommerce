package worker

import (
	"context"
	"fmt"
	"log/slog"
	"time"

	"github.com/hibiken/asynq"
	"github.com/vincommerce/backend/internal/config"
	"github.com/vincommerce/backend/internal/repository"
	"github.com/vincommerce/backend/internal/service"
)

// Task types.
const (
	TaskCancelExpiredOrders = "orders:cancel_expired"
	TaskAutoCompleteOrders  = "orders:auto_complete"
	TaskAdvanceShipped      = "orders:advance_shipped"
	TaskPriceAlerts         = "alerts:price"
	TaskBackInStock         = "alerts:back_in_stock"
	TaskCartRecovery        = "cart:recover"
	TaskSellerDigest        = "seller:daily_digest"
	TaskLowStock            = "stock:low"
	TaskSellerPresence      = "seller:presence_stats"
	TaskSessionPurge        = "sessions:purge_expired"
)

// Server wires asynq workers and schedules recurring jobs.
type Server struct {
	server *asynq.Server
	mux    *asynq.ServeMux
	sched  *asynq.Scheduler
	logger *slog.Logger

	redis config.RedisConfig
}

// shutdownTimeout bounds how long Shutdown waits for in-flight tasks. Without
// it asynq blocks forever, and a single blackholed SMTP host (net/smtp dials
// with no timeout) consumes all Concurrency slots and wedges the queue until
// the container is SIGKILLed mid-transaction.
const shutdownTimeout = 30 * time.Second

// taskTimeout bounds a single handler invocation so a slow query or a hung
// network call cannot hold a worker slot indefinitely.
const taskTimeout = 2 * time.Minute

// redisOpt builds the asynq client options. It must carry the same
// credentials as the API's cache client: passing only the address meant the
// worker could not reach an authenticated Redis, so the moment production
// Redis required a password every scheduled job stopped with no error
// surfaced anywhere.
func redisOpt(cfg config.RedisConfig) asynq.RedisClientOpt {
	return asynq.RedisClientOpt{
		Addr:         cfg.Addr,
		Password:     cfg.Password,
		DB:           cfg.DB,
		DialTimeout:  10 * time.Second,
		ReadTimeout:  10 * time.Second,
		WriteTimeout: 10 * time.Second,
	}
}

// NewServer creates the asynq server with task handlers.
func NewServer(redisCfg config.RedisConfig, orders *service.OrderService, market *service.MarketService, seller *service.SellerService, logger *slog.Logger) (*Server, error) {
	return newServer(redisCfg, orders, market, seller, nil, logger)
}

// NewServerWithSessions accepts the session repository so expired refresh
// sessions can be purged periodically instead of accumulating forever.
func NewServerWithSessions(redisCfg config.RedisConfig, orders *service.OrderService, market *service.MarketService, seller *service.SellerService, sessions *repository.SessionRepository, logger *slog.Logger) (*Server, error) {
	return newServer(redisCfg, orders, market, seller, sessions, logger)
}

func newServer(redisCfg config.RedisConfig, orders *service.OrderService, market *service.MarketService, seller *service.SellerService, sessions *repository.SessionRepository, logger *slog.Logger) (*Server, error) {
	// A nil logger would panic in shutdown(), which is the worst possible
	// moment: the process is already on its way out and the panic turns a
	// clean drain into a SIGKILL.
	if logger == nil {
		logger = slog.Default()
	}
	opt := redisOpt(redisCfg)

	// Fail fast and loudly if the queue is unreachable. A worker that starts
	// happily against an unusable Redis looks healthy while processing
	// nothing, which is how a password mismatch went unnoticed.
	if err := pingRedis(opt); err != nil {
		return nil, fmt.Errorf("worker cannot reach redis at %s: %w", redisCfg.Addr, err)
	}

	srv := asynq.NewServer(
		opt,
		asynq.Config{
			Concurrency: 10,
			Logger:      &asynqLogger{logger},
			Queues: map[string]int{
				"critical": 6,
				"default":  3,
				"low":      1,
			},
			ShutdownTimeout:     shutdownTimeout,
			HealthCheckInterval: 15 * time.Second,
		},
	)

	sched := asynq.NewScheduler(
		opt,
		&asynq.SchedulerOpts{Logger: &asynqLogger{logger}},
	)

	mux := asynq.NewServeMux()
	mux.HandleFunc(TaskCancelExpiredOrders, withTimeout(cancelExpiredOrdersHandler(orders, logger)))
	mux.HandleFunc(TaskAutoCompleteOrders, withTimeout(autoCompleteOrdersHandler(orders, logger)))
	mux.HandleFunc(TaskAdvanceShipped, withTimeout(advanceShippedHandler(orders, logger)))
	mux.HandleFunc(TaskPriceAlerts, withTimeout(priceAlertsHandler(market, logger)))
	mux.HandleFunc(TaskBackInStock, withTimeout(backInStockHandler(market, logger)))
	mux.HandleFunc(TaskCartRecovery, withTimeout(cartRecoveryHandler(orders, logger)))
	mux.HandleFunc(TaskSellerDigest, withTimeout(sellerDigestHandler(seller, logger)))
	mux.HandleFunc(TaskLowStock, withTimeout(lowStockHandler(seller, logger)))
	mux.HandleFunc(TaskSellerPresence, withTimeout(sellerPresenceHandler(seller, logger)))
	if sessions != nil {
		mux.HandleFunc(TaskSessionPurge, withTimeout(sessionPurgeHandler(sessions, logger)))
	}

	// Recurring jobs get:
	//   * MaxRetry(3) so a permanently failing task does not burn 25 retries;
	//   * Retention so a dead-lettered task is not archived forever with nobody
	//     reading the archive;
	//   * Unique(ttl) so a slow run overlapping the next tick cannot
	//     double-process.
	//
	// The Unique TTL MUST EXCEED the job's period, otherwise the lock has
	// already expired by the time the next tick fires and the guard does
	// nothing. The first version of this file set every TTL just *below* its
	// interval, which made the guard inert on all ten jobs — a check that
	// cannot fail is worse than no check, because it reads like protection.
	register := func(spec, taskType, queue string, uniq time.Duration) error {
		_, err := sched.Register(spec, asynq.NewTask(taskType, nil,
			asynq.Queue(queue),
			asynq.MaxRetry(3),
			asynq.Retention(7*24*time.Hour),
			asynq.Unique(uniq),
		))
		if err != nil {
			return fmt.Errorf("register scheduler %s: %w", taskType, err)
		}
		return nil
	}

	// Order expiry releases stock reservations and moves money-adjacent state,
	// so it runs on the high-priority queue: a backlog in `low` silently
	// delays expired-order cleanup and holds inventory hostage.
	for _, j := range []struct {
		spec, taskType, queue string
		uniq                  time.Duration
	}{
		{"@every 5m", TaskCancelExpiredOrders, "critical", 15 * time.Minute},
		{"@daily", TaskAutoCompleteOrders, "default", 25 * time.Hour},
		// Ghost-buyer sweep: shipped -> delivered after the carrier window.
		{"@every 6h", TaskAdvanceShipped, "default", 12 * time.Hour},
		{"@hourly", TaskPriceAlerts, "low", 2 * time.Hour},
		{"@every 15m", TaskBackInStock, "low", 45 * time.Minute},
		{"@every 30m", TaskCartRecovery, "low", 75 * time.Minute},
		{"@daily", TaskSellerDigest, "low", 25 * time.Hour},
		{"@every 30m", TaskLowStock, "low", 75 * time.Minute},
		{"@daily", TaskSellerPresence, "low", 25 * time.Hour},
		{"@every 1h", TaskSessionPurge, "low", 3 * time.Hour},
	} {
		if j.taskType == TaskSessionPurge && sessions == nil {
			continue
		}
		if err := register(j.spec, j.taskType, j.queue, j.uniq); err != nil {
			return nil, err
		}
	}

	return &Server{
		server: srv,
		mux:    mux,
		sched:  sched,
		logger: logger,
		redis:  redisCfg,
	}, nil
}

// pingRedis verifies the queue is actually usable before Start returns, so a
// misconfigured worker fails at boot rather than silently doing nothing.
//
// asynq's Inspector API is context-free, so the bound comes from the client
// options' own DialTimeout/ReadTimeout rather than a passed context. The
// previous version rebuilt the options WITHOUT those timeouts, so a
// blackholed Redis left this call hanging indefinitely, and it raced
// `inspector.Close()` whenever a real context was supplied.
func pingRedis(opt asynq.RedisClientOpt) error {
	inspector := asynq.NewInspector(opt)
	defer inspector.Close()

	queues, err := inspector.Queues()
	if err != nil {
		return err
	}
	_ = queues
	return nil
}

// withTimeout bounds each handler so no single task can occupy a worker slot
// forever.
func withTimeout(h func(context.Context, *asynq.Task) error) func(context.Context, *asynq.Task) error {
	return func(ctx context.Context, t *asynq.Task) error {
		c, cancel := context.WithTimeout(ctx, taskTimeout)
		defer cancel()
		return h(c, t)
	}
}

// QueueStats is a point-in-time snapshot of one queue, used by the health
// endpoint and the operational dashboard.
type QueueStats struct {
	Queue      string `json:"queue"`
	Pending    int64  `json:"pending"`
	Active     int64  `json:"active"`
	Scheduled  int64  `json:"scheduled"`
	Retry      int64  `json:"retry"`
	Archived   int64  `json:"archived"`
	OldestSecs int64  `json:"oldest_age_seconds"`
}

// Stats reports per-queue depth for this server's Redis.
//
// A non-zero Archived count is a dead-letter signal: nothing in the codebase
// ever drains the archive, so this is the only way those failures become
// visible.
func (s *Server) Stats(ctx context.Context) ([]QueueStats, error) {
	return CheckQueue(ctx, s.redis)
}

// CheckQueue reports per-queue depth against a Redis instance.
//
// It deliberately does NOT need a *Server, a database pool, or a running asynq
// server, so the container HEALTHCHECK can call it in a short-lived process.
// Building a whole Server for a probe would open a second pgx pool and
// construct every task handler just to answer "is the queue reachable?".
func CheckQueue(ctx context.Context, cfg config.RedisConfig) ([]QueueStats, error) {
	inspector := asynq.NewInspector(asynq.RedisClientOpt{
		Addr:         cfg.Addr,
		Password:     cfg.Password,
		DB:           cfg.DB,
		DialTimeout:  5 * time.Second,
		ReadTimeout:  5 * time.Second,
		WriteTimeout: 5 * time.Second,
	})
	defer inspector.Close()

	// The Inspector API is context-free, so a hung Redis read would outlive
	// any ctx the caller passed. Run it on a goroutine and let the deadline win.
	type result struct {
		stats []QueueStats
		err   error
	}
	done := make(chan result, 1)
	go func() {
		stats, err := queueStats(inspector)
		done <- result{stats: stats, err: err}
	}()

	select {
	case res := <-done:
		return res.stats, res.err
	case <-ctx.Done():
		return nil, ctx.Err()
	}
}

func queueStats(inspector *asynq.Inspector) ([]QueueStats, error) {
	names, err := inspector.Queues()
	if err != nil {
		return nil, err
	}

	out := make([]QueueStats, 0, len(names))
	for _, name := range names {
		q, err := inspector.GetQueueInfo(name)
		if err != nil {
			continue
		}
		out = append(out, QueueStats{
			Queue: name,
			// Latency is the age of the oldest pending task — the signal that
			// matters for a backlog alert, where depth alone can be tiny.
			Pending:    int64(q.Pending),
			Active:     int64(q.Active),
			Scheduled:  int64(q.Scheduled),
			Retry:      int64(q.Retry),
			Archived:   int64(q.Archived),
			OldestSecs: int64(q.Latency.Seconds()),
		})
	}
	return out, nil
}

// Start runs the worker until ctx is done or the asynq server fails.
// A dead asynq server (e.g. prolonged Redis outage) must terminate the
// process — not leave it a zombie that silently stops processing tasks.
func (s *Server) Start(ctx context.Context) error {
	if err := s.sched.Start(); err != nil {
		return err
	}
	defer s.sched.Shutdown()

	errCh := make(chan error, 1)
	go func() {
		if err := s.server.Run(s.mux); err != nil {
			errCh <- err
		}
	}()

	// Stop the scheduler first so no new periodic work is enqueued while we
	// drain, then bound the drain so a wedged task cannot hold the process
	// open until the container is SIGKILLed.
	shutdown := func() {
		s.sched.Shutdown()
		done := make(chan struct{})
		go func() { s.server.Shutdown(); close(done) }()
		select {
		case <-done:
		case <-time.After(shutdownTimeout):
			s.logger.Warn("worker shutdown timed out; forcing exit",
				"timeout", shutdownTimeout,
				"hint", "a task is likely blocked on an unbounded network call")
		}
	}

	select {
	case <-ctx.Done():
		s.logger.Info("worker shutting down", "timeout", shutdownTimeout)
		shutdown()
		return nil
	case err := <-errCh:
		shutdown()
		return fmt.Errorf("asynq server stopped: %w", err)
	}
}

func cancelExpiredOrdersHandler(orders *service.OrderService, logger *slog.Logger) func(context.Context, *asynq.Task) error {
	return func(ctx context.Context, t *asynq.Task) error {
		cancelled, err := orders.CancelExpired(ctx, 100)
		if err != nil {
			return err
		}
		if cancelled > 0 {
			logger.Info("cancelled expired orders", "count", cancelled)
		}
		return nil
	}
}

func autoCompleteOrdersHandler(orders *service.OrderService, logger *slog.Logger) func(context.Context, *asynq.Task) error {
	return func(ctx context.Context, t *asynq.Task) error {
		completed, err := orders.CompleteDelivered(ctx, 7*24*time.Hour, 100)
		if err != nil {
			return err
		}
		if completed > 0 {
			logger.Info("auto-completed delivered orders", "count", completed)
		}
		return nil
	}
}

func advanceShippedHandler(orders *service.OrderService, logger *slog.Logger) func(context.Context, *asynq.Task) error {
	return func(ctx context.Context, t *asynq.Task) error {
		// Carrier delivery windows are typically ≤7 days; anything older is
		// treated as delivered by the ghost-buyer sweep.
		advanced, err := orders.AdvanceShippedOrders(ctx, 7*24*time.Hour, 100)
		if err != nil {
			return err
		}
		if advanced > 0 {
			logger.Info("auto-advanced shipped orders to delivered", "count", advanced)
		}
		return nil
	}
}

func priceAlertsHandler(market *service.MarketService, logger *slog.Logger) func(context.Context, *asynq.Task) error {
	return func(ctx context.Context, t *asynq.Task) error {
		triggered, err := market.ProcessPriceAlerts(ctx, 100)
		if err != nil {
			return err
		}
		if triggered > 0 {
			logger.Info("price alerts triggered", "count", triggered)
		}
		return nil
	}
}

func backInStockHandler(market *service.MarketService, logger *slog.Logger) func(context.Context, *asynq.Task) error {
	return func(ctx context.Context, t *asynq.Task) error {
		triggered, err := market.ProcessBackInStock(ctx, 100)
		if err != nil {
			return err
		}
		if triggered > 0 {
			logger.Info("back-in-stock alerts triggered", "count", triggered)
		}
		return nil
	}
}

func cartRecoveryHandler(orders *service.OrderService, logger *slog.Logger) func(context.Context, *asynq.Task) error {
	return func(ctx context.Context, t *asynq.Task) error {
		sent, err := orders.RecoverAbandonedCarts(ctx, 24*time.Hour, 100)
		if err != nil {
			return err
		}
		if sent > 0 {
			logger.Info("abandoned cart recovery emails sent", "count", sent)
		}
		return nil
	}
}

func sellerDigestHandler(seller *service.SellerService, logger *slog.Logger) func(context.Context, *asynq.Task) error {
	return func(ctx context.Context, t *asynq.Task) error {
		sent, err := seller.SendDailyDigests(ctx)
		if err != nil {
			return err
		}
		if sent > 0 {
			logger.Info("seller daily digests sent", "count", sent)
		}
		return nil
	}
}

func lowStockHandler(seller *service.SellerService, logger *slog.Logger) func(context.Context, *asynq.Task) error {
	return func(ctx context.Context, t *asynq.Task) error {
		notified, err := seller.ProcessLowStock(ctx, 100)
		if err != nil {
			return err
		}
		if notified > 0 {
			logger.Info("low-stock alerts sent", "count", notified)
		}
		return nil
	}
}

func sellerPresenceHandler(seller *service.SellerService, logger *slog.Logger) func(context.Context, *asynq.Task) error {
	return func(ctx context.Context, t *asynq.Task) error {
		if err := seller.UpdatePresenceStats(ctx); err != nil {
			return err
		}
		logger.Info("seller presence stats recomputed")
		return nil
	}
}

func sessionPurgeHandler(sessions *repository.SessionRepository, logger *slog.Logger) func(context.Context, *asynq.Task) error {
	return func(ctx context.Context, t *asynq.Task) error {
		purged, err := sessions.PurgeExpired(ctx, time.Now().UTC())
		if err != nil {
			return err
		}
		if purged > 0 {
			logger.Info("purged expired refresh sessions", "count", purged)
		}
		return nil
	}
}

type asynqLogger struct {
	logger *slog.Logger
}

func (l *asynqLogger) Debug(args ...any) { l.logger.Debug(fmt.Sprint(args...)) }
func (l *asynqLogger) Info(args ...any)  { l.logger.Info(fmt.Sprint(args...)) }
func (l *asynqLogger) Warn(args ...any)  { l.logger.Warn(fmt.Sprint(args...)) }
func (l *asynqLogger) Error(args ...any) { l.logger.Error(fmt.Sprint(args...)) }
func (l *asynqLogger) Fatal(args ...any) { l.logger.Error(fmt.Sprint(args...)) }
