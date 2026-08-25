package worker

import (
	"context"
	"fmt"
	"log/slog"
	"time"

	"github.com/hibiken/asynq"
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
}

// NewServer creates the asynq server with task handlers.
func NewServer(redisAddr string, orders *service.OrderService, market *service.MarketService, seller *service.SellerService, logger *slog.Logger) (*Server, error) {
	return newServer(redisAddr, orders, market, seller, nil, logger)
}

// NewServerWithSessions accepts the session repository so expired refresh
// sessions can be purged periodically instead of accumulating forever.
func NewServerWithSessions(redisAddr string, orders *service.OrderService, market *service.MarketService, seller *service.SellerService, sessions *repository.SessionRepository, logger *slog.Logger) (*Server, error) {
	return newServer(redisAddr, orders, market, seller, sessions, logger)
}

func newServer(redisAddr string, orders *service.OrderService, market *service.MarketService, seller *service.SellerService, sessions *repository.SessionRepository, logger *slog.Logger) (*Server, error) {
	srv := asynq.NewServer(
		asynq.RedisClientOpt{Addr: redisAddr},
		asynq.Config{
			Concurrency: 10,
			Logger:      &asynqLogger{logger},
			Queues: map[string]int{
				"critical": 6,
				"default":  3,
				"low":      1,
			},
		},
	)

	sched := asynq.NewScheduler(
		asynq.RedisClientOpt{Addr: redisAddr},
		&asynq.SchedulerOpts{Logger: &asynqLogger{logger}},
	)

	mux := asynq.NewServeMux()
	mux.HandleFunc(TaskCancelExpiredOrders, cancelExpiredOrdersHandler(orders, logger))
	mux.HandleFunc(TaskAutoCompleteOrders, autoCompleteOrdersHandler(orders, logger))
	mux.HandleFunc(TaskAdvanceShipped, advanceShippedHandler(orders, logger))
	mux.HandleFunc(TaskPriceAlerts, priceAlertsHandler(market, logger))
	mux.HandleFunc(TaskBackInStock, backInStockHandler(market, logger))
	mux.HandleFunc(TaskCartRecovery, cartRecoveryHandler(orders, logger))
	mux.HandleFunc(TaskSellerDigest, sellerDigestHandler(seller, logger))
	mux.HandleFunc(TaskLowStock, lowStockHandler(seller, logger))
	mux.HandleFunc(TaskSellerPresence, sellerPresenceHandler(seller, logger))
	if sessions != nil {
		mux.HandleFunc(TaskSessionPurge, sessionPurgeHandler(sessions, logger))
	}

	// Sweep unpaid orders every 5 minutes; auto-complete delivered orders daily; price alerts hourly.
	if _, err := sched.Register("@every 5m", asynq.NewTask(TaskCancelExpiredOrders, nil, asynq.Queue("low"))); err != nil {
		return nil, fmt.Errorf("register scheduler: %w", err)
	}
	if _, err := sched.Register("@daily", asynq.NewTask(TaskAutoCompleteOrders, nil, asynq.Queue("low"))); err != nil {
		return nil, fmt.Errorf("register scheduler: %w", err)
	}
	// Ghost-buyer sweep: shipped → delivered after the carrier window.
	if _, err := sched.Register("@every 6h", asynq.NewTask(TaskAdvanceShipped, nil, asynq.Queue("low"))); err != nil {
		return nil, fmt.Errorf("register scheduler: %w", err)
	}
	if _, err := sched.Register("@hourly", asynq.NewTask(TaskPriceAlerts, nil, asynq.Queue("low"))); err != nil {
		return nil, fmt.Errorf("register scheduler: %w", err)
	}
	if _, err := sched.Register("@every 15m", asynq.NewTask(TaskBackInStock, nil, asynq.Queue("low"))); err != nil {
		return nil, fmt.Errorf("register scheduler: %w", err)
	}
	if _, err := sched.Register("@every 30m", asynq.NewTask(TaskCartRecovery, nil, asynq.Queue("low"))); err != nil {
		return nil, fmt.Errorf("register scheduler: %w", err)
	}
	if _, err := sched.Register("@daily", asynq.NewTask(TaskSellerDigest, nil, asynq.Queue("low"))); err != nil {
		return nil, fmt.Errorf("register scheduler: %w", err)
	}
	if _, err := sched.Register("@every 30m", asynq.NewTask(TaskLowStock, nil, asynq.Queue("low"))); err != nil {
		return nil, fmt.Errorf("register scheduler: %w", err)
	}
	if _, err := sched.Register("@daily", asynq.NewTask(TaskSellerPresence, nil, asynq.Queue("low"))); err != nil {
		return nil, fmt.Errorf("register scheduler: %w", err)
	}
	if sessions != nil {
		if _, err := sched.Register("@every 1h", asynq.NewTask(TaskSessionPurge, nil, asynq.Queue("low"))); err != nil {
			return nil, fmt.Errorf("register scheduler: %w", err)
		}
	}

	return &Server{server: srv, mux: mux, sched: sched, logger: logger}, nil
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

	select {
	case <-ctx.Done():
		s.server.Shutdown()
		return nil
	case err := <-errCh:
		s.server.Shutdown()
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
