// Package app is the composition root.
//
// Every service in this application is a struct that holds repository pointers
// and collaborator services, wired together at boot. Before this package
// existed, that wiring lived inside httpapi.NewRouter, and the worker binary
// re-implemented its own version of it by hand.
//
// That duplication is the single most damaging structural defect in the
// codebase, and it was not a style problem. NewRouter applied 49 setter calls;
// cmd/worker applied 3. Because the wiring was setter-based rather than
// constructor-based, a missing dependency was not a compile error -- it was a
// nil field, and every service that read it guarded with `if s.x == nil` and
// silently did nothing. In the worker that meant, at the time of writing:
//
//	payments    nil  -> AdvanceShippedOrders never captured COD
//	loyalty     nil  -> CompleteDelivered never awarded a single point
//	broker      nil  -> no SSE event was ever published by a job
//	notifs      nil  -> no in-app notification was ever written by a job
//	mailer      nil  -> RecoverAbandonedCarts returned (0, nil) forever, so the
//	                     cart-recovery job reported SUCCESS every 30 minutes
//	                     and sent no email, ever
//	logger      nil  -> the one line that would have logged the others was the
//	                     one line that was skipped
//
// Four of those five produce a green dashboard and no work done, which is why
// this is now one shared Build call: both binaries get the identical graph, and
// a service that gains a dependency in one place gains it in both.
package app

import (
	"context"
	"fmt"
	"log/slog"
	"strings"

	"github.com/redis/go-redis/v9"
	"github.com/vincommerce/backend/internal/ai"
	"github.com/vincommerce/backend/internal/cache"
	"github.com/vincommerce/backend/internal/config"
	"github.com/vincommerce/backend/internal/db"
	"github.com/vincommerce/backend/internal/domain"
	"github.com/vincommerce/backend/internal/mail"
	"github.com/vincommerce/backend/internal/payments"
	"github.com/vincommerce/backend/internal/repository"
	"github.com/vincommerce/backend/internal/service"
	"github.com/vincommerce/backend/internal/stream"
)

// Deps is everything Build needs from the process. Both cmd/api and cmd/worker
// construct exactly this and nothing else.
type Deps struct {
	Pool   *db.Pool
	Redis  *redis.Client
	Config *config.Config
	Logger *slog.Logger
	// Mailer is optional: with no SMTP configured, mail.NewClient returns nil
	// and every service that sends email must degrade to "do not send" rather
	// than nil-panic. The API and worker both tolerate that today (local dev
	// has no SMTP), so Build tolerates it too -- but the services receive the
	// nil explicitly instead of discovering it later.
	Mailer *mail.Client
}

// Services is the fully-wired application graph.
type Services struct {
	Repositories Repositories
	Services     DomainServices
	Broker       *stream.Broker
	Password     *service.Password
	Tokens       *service.TokenManager
	Gateways     []payments.Gateway
}

// Repositories is exposed so a process that legitimately needs raw persistence
// (a reconciliation job, the API's SEO sitemap) can reach it without
// re-constructing repositories and creating a second, differently-wired set.
type Repositories struct {
	Users         *repository.UserRepository
	Sessions      *repository.SessionRepository
	Categories    *repository.CategoryRepository
	Products      *repository.ProductRepository
	Reviews       *repository.ReviewRepository
	Carts         *repository.CartRepository
	Orders        *repository.OrderRepository
	Shipments     *repository.ShipmentRepository
	Addresses     *repository.AddressRepository
	Payments      *repository.PaymentRepository
	Stores        *repository.StoreRepository
	Wishlist      *repository.WishlistRepository
	Analytics     *repository.AnalyticsRepository
	Support       *repository.SupportRepository
	Notifications *repository.NotificationRepository
	FeatureFlags  *repository.FeatureFlagRepository
	Chat          *repository.ChatRepository
	Market        *repository.QARepository
	Loyalty       *repository.LoyaltyRepository
	Disputes      *repository.DisputeRepository
	Gamification  *repository.GamificationRepository
	Live          *repository.LiveRepository
	Ledger        *repository.LedgerRepository
}

// DomainServices is the wired service layer.
type DomainServices struct {
	Auth         *service.AuthService
	OAuth        *service.OAuthService
	Catalog      *service.CatalogService
	Product      *service.ProductService
	Order        *service.OrderService
	Payment      *service.PaymentService
	Seller       *service.SellerService
	Engagement   *service.EngagementService
	Market       *service.MarketService
	Support      *service.SupportService
	Analytics    *service.AnalyticsService
	Chat         *service.ChatService
	Live         *service.LiveService
	AI           *service.AIService
	Notification *service.NotificationService
	Seo          *service.SeoService
	FeatureFlags *service.FeatureFlagService
	Ledger       *service.LedgerService
}

// Build wires the entire application. It is called by both binaries.
func Build(ctx context.Context, d Deps) (*Services, error) {
	cfg := d.Config
	logger := d.Logger
	if logger == nil {
		logger = slog.Default()
	}
	if d.Pool == nil {
		return nil, fmt.Errorf("app.Build: a database pool is required")
	}
	if cfg == nil {
		return nil, fmt.Errorf("app.Build: config is required")
	}
	pool := d.Pool

	// --- repositories -------------------------------------------------------
	// One instance each. Constructing a second repository over the same pool is
	// harmless today (they are stateless wrappers) but it makes "which set is
	// wired" ambiguous, which is how the worker's partial graph happened.
	r := Repositories{
		Users:         repository.NewUserRepository(pool),
		Sessions:      repository.NewSessionRepository(pool),
		Categories:    repository.NewCategoryRepository(pool),
		Products:      repository.NewProductRepository(pool),
		Reviews:       repository.NewReviewRepository(pool),
		Carts:         repository.NewCartRepository(pool),
		Orders:        repository.NewOrderRepository(pool),
		Shipments:     repository.NewShipmentRepository(pool),
		Addresses:     repository.NewAddressRepository(pool),
		Payments:      repository.NewPaymentRepository(pool),
		Stores:        repository.NewStoreRepository(pool),
		Wishlist:      repository.NewWishlistRepository(pool),
		Analytics:     repository.NewAnalyticsRepository(pool),
		Support:       repository.NewSupportRepository(pool),
		Notifications: repository.NewNotificationRepository(pool),
		FeatureFlags:  repository.NewFeatureFlagRepository(pool),
		Chat:          repository.NewChatRepository(pool),
		Market:        repository.NewQARepository(pool),
		Loyalty:       repository.NewLoyaltyRepository(pool),
		Disputes:      repository.NewDisputeRepository(pool),
		Gamification:  repository.NewGamificationRepository(pool),
		Live:          repository.NewLiveRepository(pool),
		// The ledger. Registered alongside the others rather than lazily inside
		// the payment service, because a money subsystem that exists only on some
		// code paths is the same class of defect as the sandbox gateway: a process
		// that does not build a router gets no gateway list, and nobody notices
		// until a refund posts no journal.
		Ledger: repository.NewLedgerRepository(pool),
	}

	// --- gateways -----------------------------------------------------------
	// The sandbox adapter is DEV-ONLY. Its webhook secret is the literal string
	// "sandbox-webhook-secret", which is published in this repository and on
	// gitleaks' allowlist, and its approve endpoint lets an order owner mark
	// their own order paid. If a production deployment ever registers it, a
	// forged notification settles any order. cfg.IsDev() is the gate.
	//
	// This was correct, but it lived in the router -- so a process that did not
	// build a router had no gateway list at all, and the worker's payment
	// service (once it had one) would have had an empty set. Registering here
	// means the guard is in one place that every process passes through.
	isDev := cfg.IsDev()
	gateways := []payments.Gateway{}
	if isDev {
		if gw, err := payments.NewGateway("sandbox", cfg.Payments.SandboxBaseURL, "", "", nil); err == nil {
			gateways = append(gateways, gw)
		} else {
			logger.Error("payment gateway: sandbox adapter unavailable in dev", "reason", err.Error())
		}
	} else if cfg.Payments.Gateway == "sandbox" {
		logger.Error("payment gateway: refusing sandbox gateway outside development",
			"configured", "sandbox",
			"action", "set PAYMENT_GATEWAY to midtrans, or APP_ENV to development")
	}
	if cfg.Payments.MidtransServerKey != "" {
		methods := []string{}
		for _, m := range strings.Split(cfg.Payments.EnabledMethods, ",") {
			if m = strings.TrimSpace(m); m != "" {
				methods = append(methods, m)
			}
		}
		mt, err := payments.NewGateway("midtrans", "", cfg.Payments.MidtransServerKey, cfg.Payments.MidtransEnv, methods)
		if err != nil {
			logger.Error("payment gateway: midtrans disabled", "reason", err.Error())
		} else {
			gateways = append(gateways, mt)
			logger.Info("payment gateway: midtrans enabled", "env", cfg.Payments.MidtransEnv)
		}
	}

	// --- services -----------------------------------------------------------
	password := service.NewPassword(
		cfg.Auth.Argon2Memory, cfg.Auth.Argon2Iterations, cfg.Auth.Argon2Parallelism, cfg.Auth.Argon2SaltLength)
	tokens := service.NewTokenManager(cfg.Auth.JWTSecret, cfg.Auth.AccessTokenTTL, cfg.Auth.RefreshTokenTTL)
	authSvc := service.NewAuthService(r.Users, r.Sessions, password, tokens, d.Mailer, cfg)
	oauthSvc := service.NewOAuthService(r.Users, r.Sessions, password, tokens,
		cfg.OAuth.GoogleClientID, cfg.OAuth.GoogleClientSecret, cfg.OAuth.GoogleRedirectURL)

	catalogSvc := service.NewCatalogService(r.Categories)
	productSvc := service.NewProductService(r.Products, r.Reviews, r.Orders)
	orderSvc := service.NewOrderService(r.Carts, r.Orders, r.Addresses)
	paymentSvc := service.NewPaymentService(r.Payments, r.Orders, gateways, cfg.Payments.Gateway, cfg.App.BaseURL)
	sellerSvc := service.NewSellerService(r.Stores, r.Users, r.Products, r.Orders, r.Payments)
	// Parcels. A setter rather than constructor arguments: DispatchParcel notifies
	// through SellerService.emailBuyer, so passing a mail client to both would be
	// two paths to one inbox.
	sellerSvc.SetShipmentService(service.NewShipmentService(r.Orders, r.Shipments))
	engagementSvc := service.NewEngagementService(r.Wishlist, r.Products)
	marketSvc := service.NewMarketService(r.Market)
	supportSvc := service.NewSupportService(r.Support, r.Orders)
	analyticsSvc := service.NewAnalyticsService(r.Analytics)
	notificationSvc := service.NewNotificationService(r.Notifications)

	// The broker is a constructor argument for ChatService and LiveService but a
	// setter for OrderService and PaymentService. Both shapes now coexist in one
	// place, which is itself the argument for eventually making them uniform.
	var broker *stream.Broker
	var store *cache.Store
	if d.Redis != nil {
		broker = stream.NewBroker(d.Redis)
		store = cache.NewStore(d.Redis)
	} else {
		logger.Warn("no Redis client: cache, rate limiting, SSE streaming and the " +
			"job queue are disabled for this process")
	}
	chatSvc := service.NewChatService(r.Chat, r.Orders, broker)
	liveSvc := service.NewLiveService(r.Live, r.Products)
	// The ledger takes a logger rather than none, because an unbalanced journal
	// from a future code path is exactly the kind of event that must reach the
	// log even when the caller swallows the returned error.
	ledgerSvc := service.NewLedgerService(r.Ledger, logger)
	if store != nil {
		catalogSvc.SetCache(store)
		engagementSvc.SetCache(store)
		sellerSvc.SetPresenceCache(store)
	}
	if broker != nil {
		liveSvc.SetBroker(broker)
		orderSvc.SetBroker(broker)
		paymentSvc.SetBroker(broker)
	}

	// --- the wiring that used to differ between the two binaries ------------
	orderSvc.SetStores(r.Stores)
	orderSvc.SetLoyalty(r.Loyalty)
	orderSvc.SetWishlist(r.Wishlist)
	orderSvc.SetInsurancePct(cfg.Payments.ShippingInsurancePct)
	orderSvc.SetPaymentService(paymentSvc)
	orderSvc.SetNotificationService(notificationSvc)
	orderSvc.SetUsers(r.Users)
	orderSvc.SetMailer(d.Mailer, cfg.App.WebURL)
	// The order service has post-commit paths (loyalty debit, email send) where
	// the work is already durable, so the logger is the only way their failures
	// stay visible. This is also the dependency the worker was silently missing
	// before the graph was unified, which is why it is set here rather than in
	// the router: a setter that is only called from one binary is a setter that
	// can be forgotten.
	orderSvc.SetLogger(logger)

	// The seller service needs the payment SERVICE, not just its repository:
	// RefundReturn has to go through the same refund implementation as every
	// other refund path, and holding only the repository is what let it become a
	// second, independent refund with no cumulative cap, no debit anywhere, and a
	// terminal intent status on the first refunded item.
	sellerSvc.SetPaymentService(paymentSvc)

	sellerSvc.SetSessions(r.Sessions)
	sellerSvc.SetReturnAutoApprove(cfg.Payments.ReturnAutoApproveMax)
	sellerSvc.SetMailer(d.Mailer, cfg.App.WebURL)
	sellerSvc.SetNotificationService(notificationSvc)

	engagementSvc.SetStores(r.Stores)

	marketSvc.SetNotificationService(notificationSvc)
	marketSvc.SetLoyalty(r.Loyalty, r.Disputes)
	marketSvc.SetGamification(r.Gamification)
	marketSvc.SetUsers(r.Users)
	marketSvc.SetMailer(d.Mailer, cfg.App.WebURL)
	marketSvc.SetPaymentService(paymentSvc)

	supportSvc.SetUsers(r.Users)
	supportSvc.SetMailer(d.Mailer, cfg.App.WebURL)
	supportSvc.SetNotificationService(notificationSvc)

	analyticsSvc.SetOrders(r.Orders)
	productSvc.SetNotificationService(notificationSvc)

	paymentSvc.SetSandboxAutoSend(isDev)
	paymentSvc.SetNotificationService(notificationSvc)
	paymentSvc.SetUsers(r.Users)
	// A "split" dispute decision moves real money, so the payment service must
	// be able to claim the dispute row inside its own transaction.
	paymentSvc.SetDisputes(r.Disputes)
	// The ledger, so capture, release and refund each post a double-entry journal
	// in the SAME transaction as the wallet movement. Set last and commented
	// loudly because it is the one omission that leaves money moving with nothing
	// accounting for it -- and a nil ledger is tolerated at the call sites so that
	// a missing wire degrades loudly (Error log + reconciliation drift) rather
	// than failing every payment.
	paymentSvc.SetLedger(ledgerSvc)
	paymentSvc.SetMailer(d.Mailer, cfg.App.WebURL)
	// Payout eligibility is a seller-side invariant (KYC approved, no negative
	// balance), expressed as a function value rather than a dependency so the
	// payment package does not import the seller package. It is a live coupling
	// and is called out here because it is the one edge in the graph that no
	// interface describes.
	paymentSvc.SetPayoutGuard(sellerSvc.AssertPayoutEligible)

	// The payout lag. `SetPayoutLag` has existed with a full range check and had NO
	// CALLER, so the lag was always the Go default of 7 no matter how the
	// deployment was configured -- an operator could set PAYOUT_LAG_DAYS and watch
	// nothing happen, which is the worst shape a configuration option can have.
	//
	// Validated in config.Validate as well, so this cannot receive a value outside
	// the range. An error here is therefore a wiring bug rather than an operator
	// mistake, and it is returned rather than logged-and-ignored: a payout term that
	// silently is not the configured one is worse than a failed boot.
	if err := paymentSvc.SetPayoutLag(cfg.Payments.PayoutLagDays); err != nil {
		return nil, fmt.Errorf("app.Build: payout lag: %w", err)
	}

	// Product changes invalidate the cached bestseller feed.
	productSvc.SetOnProductChanged(engagementSvc.InvalidateRecommended)
	sellerSvc.SetOnProductChanged(engagementSvc.InvalidateRecommended)

	// --- AI assistant -------------------------------------------------------
	// Knowledge base = published help articles, read once at boot. A failure
	// here is not fatal: the assistant degrades to an empty corpus rather than
	// preventing the process from starting.
	assistant := ai.NewAssistant(ai.Config{
		BaseURL: cfg.AI.BaseURL, APIKey: cfg.AI.APIKey, Model: cfg.AI.Model,
	}, aiCorpus(ctx, supportSvc), logger)
	if assistant.LLMEnabled() {
		logger.Info("ai assistant: llm mode", "model", cfg.AI.Model)
	} else {
		logger.Info("ai assistant: offline retrieval mode (set AI_API_KEY for llm mode)")
	}
	aiSvc := service.NewAIService(assistant, r.Products, r.Categories)
	aiSvc.SetContext(r.Orders, r.Reviews)

	// Cross-cutting policies. Both of these were defined inside a transport
	// handler, which meant the router had to reach through a handler
	// constructor to install them and nothing else could consult or test them.
	seoSvc := service.NewSeoService(r.Products, r.Categories, store, cfg.App.BaseURL)
	featureFlags := service.NewFeatureFlagService(r.FeatureFlags, logger)

	_ = domain.KindInternal // keep the domain import meaningful for future use
	return &Services{
		Repositories: r,
		Services: DomainServices{
			Auth:         authSvc,
			OAuth:        oauthSvc,
			Catalog:      catalogSvc,
			Product:      productSvc,
			Order:        orderSvc,
			Payment:      paymentSvc,
			Seller:       sellerSvc,
			Engagement:   engagementSvc,
			Market:       marketSvc,
			Support:      supportSvc,
			Analytics:    analyticsSvc,
			Chat:         chatSvc,
			Live:         liveSvc,
			AI:           aiSvc,
			Notification: notificationSvc,
			Seo:          seoSvc,
			FeatureFlags: featureFlags,
			Ledger:       ledgerSvc,
		},
		Broker:   broker,
		Password: password,
		Tokens:   tokens,
		Gateways: gateways,
	}, nil
}

// aiCorpus loads the published help articles the assistant retrieves over.
func aiCorpus(ctx context.Context, support *service.SupportService) []ai.Document {
	articles, err := support.AllArticles(ctx)
	if err != nil {
		// Not fatal. An empty corpus makes every retrieval miss, which the
		// assistant already handles with a "I don't know" answer.
		slog.Default().Warn("ai assistant: knowledge base unavailable", "reason", err.Error())
		return nil
	}
	docs := make([]ai.Document, 0, len(articles))
	for _, a := range articles {
		if !a.IsPublished {
			continue
		}
		docs = append(docs, ai.Document{ID: a.ID, Title: a.Title, Content: a.Content, Source: a.Section})
	}
	return docs
}
