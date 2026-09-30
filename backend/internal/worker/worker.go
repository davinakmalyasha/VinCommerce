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
	// TaskReconcileRefunds polls the payment gateway for refunds whose outcome it
	// still owns and settles the ones it confirms.
	//
	// Without this a refund the provider accepted stays `submitted` forever. The
	// submit path returns as soon as the provider takes the request, so nothing
	// else ever asks -- and an operator looking at the queue cannot tell which
	// refunds actually paid out.
	TaskReconcileRefunds = "payments:reconcile_refunds"
	// TaskReconcileLedger asserts the derived balance caches agree with the
	// journal entries, and that no journal is unbalanced.
	//
	// `account_balances` and `wallets` are caches of `ledger_entries`. A cache
	// that is never checked against its source is not a cache, it is a second
	// opinion nobody looks at -- so this job is what makes the ledger's numbers
	// trustworthy rather than merely present.
	TaskReconcileLedger = "ledger:reconcile"
	// TaskReconcileSettlements compares what the gateway says moved against what
	// our ledger recorded.
	//
	// The other two reconciliation jobs check the books against the CACHES derived
	// from them. This one checks the books against the outside world, which is the
	// only one of the three that can catch money the platform thinks it has and
	// does not -- or money it never recorded at all.
	TaskReconcileSettlements = "ledger:reconcile_settlements"
	// TaskReleasePayoutReservations releases seller holds whose T+n lag has
	// passed with no open return or dispute.
	//
	// This is the fraud control made real: a seller cannot withdraw a balance a
	// return is about to reverse, because the money has not left yet.
	TaskReleasePayoutReservations = "payouts:release_reservations"
)

// Deps is everything the worker needs.
//
// A struct rather than a positional parameter list, because the list was already
// a trap once. `NewServer` grew a `sessions` parameter, which meant a second
// constructor had to exist alongside it, and the next dependency would have
// needed a third. That is precisely the shape of the defect app.Build exists to
// fix -- two construction sites that can drift apart -- reproduced inside a
// single package. Adding Payment and Ledger as two more positional parameters
// would have made it four constructors and no way to see at a glance what a
// worker is wired to.
//
// Every field is optional. A nil service disables its job rather than failing the
// boot, so a partially-wired worker still runs everything it CAN run and the
// absence is visible in the log instead of being a silent no-op.
type Deps struct {
	Redis    config.RedisConfig
	Orders   *service.OrderService
	Market   *service.MarketService
	Seller   *service.SellerService
	Sessions *repository.SessionRepository
	// Payment and Ledger drive the money reconciliation jobs.
	Payment *service.PaymentService
	Ledger  *service.LedgerService
	Logger  *slog.Logger
}

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
//
// Kept as a thin wrapper so the many call sites that do not need the money
// services do not have to spell out the whole struct.
func NewServer(redisCfg config.RedisConfig, orders *service.OrderService, market *service.MarketService, seller *service.SellerService, logger *slog.Logger) (*Server, error) {
	return New(Deps{Redis: redisCfg, Orders: orders, Market: market, Seller: seller, Logger: logger})
}

// NewServerWithSessions accepts the session repository so expired refresh
// sessions can be purged periodically instead of accumulating forever.
//
// Deprecated in favour of New(Deps{...}). It is retained only so the change is
// reviewable as a diff rather than as a rewrite; there is one caller.
func NewServerWithSessions(redisCfg config.RedisConfig, orders *service.OrderService, market *service.MarketService, seller *service.SellerService, sessions *repository.SessionRepository, logger *slog.Logger) (*Server, error) {
	return New(Deps{
		Redis: redisCfg, Orders: orders, Market: market, Seller: seller,
		Sessions: sessions, Logger: logger,
	})
}

// jobSpec is one recurring job.
type jobSpec struct {
	spec, taskType, queue string
	uniq                  time.Duration
}

// jobSpecs returns the recurring schedule, filtered by which services are wired.
//
// A pure function of Deps, so the schedule can be asserted without a Redis. That
// matters more than it sounds: a job missing from this table is invisible. The
// worker starts, its health check passes, and it never reconciles a refund -- and
// the operator sees refunds sitting in `submitted` with nothing obviously wrong
// anywhere.
//
// The uniq TTL MUST exceed the job's period for every entry. asynq's Unique lock
// expires with the TTL, so a TTL below the interval means the guard has already
// lapsed by the time the next tick fires and the dedup does nothing while
// appearing to. The first version of this table set every TTL just *below* its
// interval, which made the guard inert on all ten jobs.
func jobSpecs(d Deps) []jobSpec {
	all := []jobSpec{
		// Order expiry releases stock reservations and moves money-adjacent state,
		// so it runs on the high-priority queue: a backlog in `low` silently
		// delays expired-order cleanup and holds inventory hostage.
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
		// Refund polling is frequent because each run settles money that is
		// already at the provider and merely un-recorded here.
		{"@every 10m", TaskReconcileRefunds, "default", 30 * time.Minute},
		// The ledger check is a read-only report and cheap, so daily.
		{"@daily", TaskReconcileLedger, "low", 25 * time.Hour},
		// Settlements arrive on the gateway's own schedule, T+1 to T+7, so a
		// daily pass is the natural cadence: anything older than that is a
		// finding rather than a delay.
		{"@daily", TaskReconcileSettlements, "low", 25 * time.Hour},
		// Seller holds. Hourly rather than daily because the release conditions
		// include "past the lag by N days", and a daily pass means a seller waits
		// up to a day longer than the schedule promises -- which is exactly the
		// kind of drift that makes a stated payment term untrue. Hourly bounds the
		// error to an hour.
		//
		// `default` rather than `low`: this releases money a seller is waiting for,
		// so a backlog in `low` is a queue of sellers whose balance is not yet
		// withdrawable.
		{"@hourly", TaskReleasePayoutReservations, "default", 3 * time.Hour},
	}

	// A job whose service is absent is dropped rather than registered against a
	// handler that would panic the first time it fired. Dropping it is also why
	// the Warn above matters: the schedule is the only place this is visible.
	enabled := map[string]bool{
		TaskSessionPurge:              d.Sessions != nil,
		TaskReconcileRefunds:          d.Payment != nil,
		TaskReconcileLedger:           d.Ledger != nil,
		TaskReconcileSettlements:      d.Payment != nil,
		TaskReleasePayoutReservations: d.Payment != nil && d.Ledger != nil,
	}
	out := make([]jobSpec, 0, len(all))
	for _, j := range all {
		if on, gated := enabled[j.taskType]; gated && !on {
			continue
		}
		out = append(out, j)
	}
	return out
}

// New builds the worker from an explicit dependency set.
func New(d Deps) (*Server, error) {
	// A nil logger would panic in shutdown(), which is the worst possible
	// moment: the process is already on its way out and the panic turns a
	// clean drain into a SIGKILL.
	logger := d.Logger
	if logger == nil {
		logger = slog.Default()
	}
	redisCfg := d.Redis
	orders, market, seller, sessions := d.Orders, d.Market, d.Seller, d.Sessions

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
	// The money reconciliation jobs. Registered only when their service is
	// present, and the ABSENCE is logged: a worker that silently does not
	// reconcile refunds looks identical to one that reconciles them and finds
	// nothing, and those two states must never be confused.
	if d.Payment != nil {
		mux.HandleFunc(TaskReconcileRefunds, withTimeout(reconcileRefundsHandler(d.Payment, logger)))
	} else {
		logger.Warn("payment service not wired: gateway refunds will never be " +
			"reconciled, so a refund the provider accepted stays pending forever")
	}
	if d.Ledger != nil {
		mux.HandleFunc(TaskReconcileLedger, withTimeout(reconcileLedgerHandler(d.Ledger, logger)))
		if d.Payment != nil {
			mux.HandleFunc(TaskReconcileSettlements, withTimeout(reconcileSettlementsHandler(d.Payment, logger)))
			mux.HandleFunc(TaskReleasePayoutReservations,
				withTimeout(releasePayoutReservationsHandler(d.Payment, logger)))
		}
	} else {
		logger.Warn("ledger not wired: the derived balance caches will never be " +
			"checked against the journal, so drift would go unnoticed")
	}
	if d.Payment == nil {
		logger.Warn("payment service not wired: seller holds will never be released, " +
			"so money earned stays un-withdrawable")
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

	// The schedule as DATA, so it can be asserted without a Redis.
	//
	// Building a Server requires a reachable Redis, which means the registration
	// table used to be untestable. A job silently missing from that table is
	// invisible: the worker starts, reports healthy, and never reconciles
	// anything. That is the same class of defect as the cart-recovery job that
	// had no mailer wired and reported success forever.
	for _, j := range jobSpecs(d) {
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

// reconcileRefundBatch bounds one run.
//
// A bound rather than "reconcile everything": the loop makes a network call per
// refund, so an unbounded run against a large pending queue would hold a worker
// slot past taskTimeout, get retried, and do the same work again.
const reconcileRefundBatch = 100

// reconcileRefundsHandler settles refunds the gateway has confirmed but this
// system has not yet recorded as settled.
//
// The error is returned so asynq retries, and the work is idempotent: a refund
// already terminal is not polled again, and a poll that cannot reach the provider
// changes nothing.
func reconcileRefundsHandler(pay *service.PaymentService, logger *slog.Logger) func(context.Context, *asynq.Task) error {
	return func(ctx context.Context, t *asynq.Task) error {
		succeeded, failed, manual, err := pay.ReconcileRefunds(ctx, reconcileRefundBatch)
		if err != nil {
			return err
		}
		// Logged even when every count is zero. A reconciliation job that only
		// speaks when it finds something is indistinguishable from a job that is
		// not running, and the difference matters here: silence would mean either
		// "all settled" or "nothing has polled the gateway in a week".
		logger.Info("refund reconciliation complete",
			"settled", succeeded, "failed", failed, "manual", manual)
		// `manual` is an operator queue rather than an error, so it is reported
		// and not raised: retrying will not move money a human must move.
		if manual > 0 {
			logger.Warn("refunds need an operator", "count", manual,
				"hint", "the gateway cannot complete these; a human must transfer the money")
		}
		return nil
	}
}

// reconcileLedgerHandler proves the derived caches agree with the journal.
//
// A dirty result is logged at Error and NOT returned, because there is nothing to
// retry: the drift is a fact about the data, and re-running the same query
// produces the same answer. Returning an error would burn three retries and bury
// the finding under a task failure. The report is the alarm.
func reconcileLedgerHandler(ledger *service.LedgerService, logger *slog.Logger) func(context.Context, *asynq.Task) error {
	return func(ctx context.Context, t *asynq.Task) error {
		report, err := ledger.ReconcileAll(ctx)
		if err != nil {
			return err
		}
		if report.Clean {
			logger.Info("ledger reconciliation clean", "generated_at", report.GeneratedAt)
			return nil
		}
		logger.Error("ledger reconciliation found drift",
			"balance_cache_drift", report.BalanceCacheRows,
			"unbalanced_journals", report.UnbalancedJournals,
			"wallet_drift", report.WalletDriftRows,
			"hint", "account_balances and wallets are caches of ledger_entries; "+
				"drift means a posting path skipped the ledger, or wrote a cache directly")
		// Name the offenders rather than only counting them. A count tells an
		// operator that something is wrong; the account codes tell them where.
		for _, d := range report.BalanceCacheDrift {
			logger.Error("account balance disagrees with its entries",
				"account", d.AccountCode, "cached", d.Cached, "expected", d.Expected)
		}
		for _, u := range report.Unbalanced {
			logger.Error("journal does not sum to zero",
				"journal_id", u.JournalID, "idempotency_key", u.IdempotencyKey,
				"tx_type", u.TxType, "debits", u.Debits, "credits", u.Credits)
		}
		for _, w := range report.WalletDrift {
			logger.Error("wallet disagrees with the seller's ledger account",
				"user_id", w.UserID,
				"wallet_balance", w.WalletBalance, "ledger_balance", w.LedgerBalance,
				"wallet_held", w.WalletHeld, "ledger_held", w.LedgerHeld)
		}
		return nil
	}
}

// reconcileSettlementsHandler compares gateway-reported movements against the
// ledger.
//
// A dirty result is logged and NOT returned, for the same reason as the ledger
// handler above: the drift is a fact about the data, so a retry produces the same
// answer, and returning an error would bury the finding under a task failure.
//
// It logs at ERROR rather than Warn even when nothing matched, because a gateway
// movement with no journal means money moved and the platform has no record of
// it. That is the finding the whole settlement table exists to surface, and it
// should be impossible to miss in a log full of Info.
func reconcileSettlementsHandler(pay *service.PaymentService, logger *slog.Logger) func(context.Context, *asynq.Task) error {
	return func(ctx context.Context, t *asynq.Task) error {
		report, err := pay.ReconcileSettlements(ctx, 0)
		if err != nil {
			return err
		}
		if report.Clean {
			logger.Info("settlement reconciliation clean",
				"generated_at", report.GeneratedAt, "matched", report.Matched)
			return nil
		}
		logger.Error("settlement reconciliation found gaps",
			"unreconciled", report.Unreconciled,
			"discrepant", report.Discrepant,
			"matched", report.Matched,
			"hint", "an unreconciled movement is money the gateway reported and no "+
				"journal records; a discrepant one is matched but the amounts differ, "+
				"and the gateway's figure is the one to trust")
		// Name the movements, not just the counts: a count tells an operator
		// something is wrong, a settlement ref tells them which one to go and look
		// up in the provider's dashboard.
		for _, s := range report.UnmatchedSamples {
			logger.Error("gateway movement has no journal", "detail", s)
		}
		for _, d := range report.DiscrepantSamples {
			logger.Error("gateway movement disagrees with the journal", "detail", d)
		}
		return nil
	}
}

// releasePayoutReservationsHandler runs the fraud control that
// TaskReleasePayoutReservations was named for.
//
// The task existed as a constant with a comment describing exactly what it was
// for, and no handler and no schedule entry. Nothing about that state is visible
// from outside: the worker starts, reports healthy, and the holds it is supposed
// to release are never released. A hold that is never released is a permanent
// deduction from a seller's balance, and the seller experiences it as the
// platform keeping their money.
//
// The log line is the point. "released 0" and "this job does not exist" produce
// identical seller-visible symptoms, and only the first is a number someone can
// watch for a change in.
func releasePayoutReservationsHandler(pay *service.PaymentService, logger *slog.Logger) func(context.Context, *asynq.Task) error {
	return func(ctx context.Context, _ *asynq.Task) error {
		released, err := pay.ReleaseExpiredReservations(ctx, 0)
		if err != nil {
			return err
		}
		if released == 0 {
			// Logged rather than silent: zero is the normal steady state, and a
			// normal steady state that is indistinguishable from a broken job is
			// not something an operator can diagnose at 3am.
			logger.Info("no seller holds were due for release",
				"lag_days", pay.PayoutLagDays())
			return nil
		}
		logger.Info("released seller holds", "released", released, "lag_days", pay.PayoutLagDays())
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
