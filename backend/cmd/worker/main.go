package main

import (
	"context"
	"log/slog"
	"os"
	"os/signal"
	"syscall"

	"github.com/vincommerce/backend/internal/config"
	"github.com/vincommerce/backend/internal/db"
	"github.com/vincommerce/backend/internal/mail"
	"github.com/vincommerce/backend/internal/repository"
	"github.com/vincommerce/backend/internal/service"
	"github.com/vincommerce/backend/internal/worker"
)

func main() {
	logger := slog.New(slog.NewJSONHandler(os.Stdout, nil))
	slog.SetDefault(logger)

	if err := run(logger); err != nil {
		logger.Error("worker fatal", "error", err)
		os.Exit(1)
	}
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
	srv, err := worker.NewServerWithSessions(cfg.Redis.Addr, orderSvc, marketSvc, sellerSvc, sessions, logger)
	if err != nil {
		return err
	}

	logger.Info("worker starting", "env", cfg.Environment)
	return srv.Start(ctx)
}
