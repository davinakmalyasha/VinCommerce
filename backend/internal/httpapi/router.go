package httpapi

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"path/filepath"
	"strings"
	"time"

	"github.com/go-chi/chi/v5"
	chimw "github.com/go-chi/chi/v5/middleware"
	"github.com/go-chi/cors"
	"github.com/vincommerce/backend/internal/ai"
	"github.com/vincommerce/backend/internal/cache"
	"github.com/vincommerce/backend/internal/config"
	"github.com/vincommerce/backend/internal/db"
	"github.com/vincommerce/backend/internal/domain"
	"github.com/vincommerce/backend/internal/httpapi/handler"
	mw "github.com/vincommerce/backend/internal/httpapi/middleware"
	"github.com/vincommerce/backend/internal/mail"
	"github.com/vincommerce/backend/internal/metrics"
	"github.com/vincommerce/backend/internal/payments"
	"github.com/vincommerce/backend/internal/repository"
	"github.com/vincommerce/backend/internal/service"
	"github.com/vincommerce/backend/internal/stream"
)

// ctxPlaceholder avoids leaking request contexts into service construction.
func ctxPlaceholder() context.Context { return context.Background() }

// Dependencies bundles services for router construction.
type Dependencies struct {
	Pool    *db.Pool
	Redis   *cache.Client
	Config  *config.Config
	Logger  *slog.Logger
	Mailer  *mail.Client
	Metrics *metrics.Registry
}

// NewRouter wires the HTTP application together.
func NewRouter(deps Dependencies) http.Handler {
	cfg := deps.Config
	logger := deps.Logger

	metricsReg := deps.Metrics
	if metricsReg == nil {
		metricsReg = metrics.New()
	}

	// repositories
	users := repository.NewUserRepository(deps.Pool)
	sessions := repository.NewSessionRepository(deps.Pool)
	categories := repository.NewCategoryRepository(deps.Pool)
	products := repository.NewProductRepository(deps.Pool)
	reviews := repository.NewReviewRepository(deps.Pool)
	carts := repository.NewCartRepository(deps.Pool)
	orders := repository.NewOrderRepository(deps.Pool)
	addresses := repository.NewAddressRepository(deps.Pool)
	paymentRepo := repository.NewPaymentRepository(deps.Pool)
	stores := repository.NewStoreRepository(deps.Pool)
	wishlistRepo := repository.NewWishlistRepository(deps.Pool)
	analyticsRepo := repository.NewAnalyticsRepository(deps.Pool)
	supportRepo := repository.NewSupportRepository(deps.Pool)
	notificationRepo := repository.NewNotificationRepository(deps.Pool)
	flagRepo := repository.NewFeatureFlagRepository(deps.Pool)
	chatRepo := repository.NewChatRepository(deps.Pool)
	marketRepo := repository.NewQARepository(deps.Pool)
	loyaltyRepo := repository.NewLoyaltyRepository(deps.Pool)
	disputeRepo := repository.NewDisputeRepository(deps.Pool)
	gameRepo := repository.NewGamificationRepository(deps.Pool)
	liveRepo := repository.NewLiveRepository(deps.Pool)

	// services
	password := service.NewPassword(
		cfg.Auth.Argon2Memory, cfg.Auth.Argon2Iterations, cfg.Auth.Argon2Parallelism, cfg.Auth.Argon2SaltLength)
	tokens := service.NewTokenManager(cfg.Auth.JWTSecret, cfg.Auth.AccessTokenTTL, cfg.Auth.RefreshTokenTTL)
	authSvc := service.NewAuthService(users, sessions, password, tokens, deps.Mailer, cfg)
	oauthSvc := service.NewOAuthService(users, sessions, password, tokens, cfg.OAuth.GoogleClientID, cfg.OAuth.GoogleClientSecret, cfg.OAuth.GoogleRedirectURL)
	catalogSvc := service.NewCatalogService(categories)
	productSvc := service.NewProductService(products, reviews, orders)
	catalogSvc.SetCache(cache.NewStore(deps.Redis.Client))
	orderSvc := service.NewOrderService(carts, orders, addresses)
	orderSvc.SetStores(stores)
	orderSvc.SetLoyalty(loyaltyRepo)
	orderSvc.SetWishlist(wishlistRepo)
	orderSvc.SetInsurancePct(cfg.Payments.ShippingInsurancePct)
	// payment gateways: the sandbox adapter is DEV-ONLY (its webhook secret is
	// public knowledge); Midtrans when keys configured. In production the
	// sandbox gateway must never exist so forged webhooks fail closed.
	isDev := cfg.IsDev()
	gateways := []payments.Gateway{}
	if isDev {
		if gw, err := payments.NewGateway("sandbox", cfg.Payments.SandboxBaseURL, "", "", nil); err == nil {
			gateways = append(gateways, gw)
		}
	} else if cfg.Payments.Gateway == "sandbox" {
		logger.Error("payment gateway: refusing sandbox gateway in production")
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
	paymentSvc := service.NewPaymentService(paymentRepo, orders, gateways, cfg.Payments.Gateway, cfg.App.BaseURL)
	paymentSvc.SetSandboxAutoSend(isDev)
	sellerSvc := service.NewSellerService(stores, users, products, orders, paymentRepo)
	sellerSvc.SetSessions(sessions)
	sellerSvc.SetReturnAutoApprove(cfg.Payments.ReturnAutoApproveMax)
	paymentSvc.SetPayoutGuard(sellerSvc.AssertPayoutEligible)
	sellerSvc.SetPresenceCache(cache.NewStore(deps.Redis.Client))
	engagementSvc := service.NewEngagementService(wishlistRepo, products)
	engagementSvc.SetCache(cache.NewStore(deps.Redis.Client))
	engagementSvc.SetStores(stores)
	productSvc.SetOnProductChanged(engagementSvc.InvalidateRecommended)
	sellerSvc.SetOnProductChanged(engagementSvc.InvalidateRecommended)
	analyticsSvc := service.NewAnalyticsService(analyticsRepo)
	analyticsSvc.SetOrders(orders)
	supportSvc := service.NewSupportService(supportRepo, orders)
	notificationSvc := service.NewNotificationService(notificationRepo)
	productSvc.SetNotificationService(notificationSvc)

	broker := stream.NewBroker(deps.Redis.Client)
	chatSvc := service.NewChatService(chatRepo, orders, broker)
	marketSvc := service.NewMarketService(marketRepo)
	marketSvc.SetNotificationService(notificationSvc)
	marketSvc.SetLoyalty(loyaltyRepo, disputeRepo)
	marketSvc.SetGamification(gameRepo)

	liveSvc := service.NewLiveService(liveRepo, products)
	liveSvc.SetBroker(broker)
	marketSvc.SetUsers(users)
	marketSvc.SetMailer(deps.Mailer, cfg.App.WebURL)
	supportSvc.SetUsers(users)
	supportSvc.SetMailer(deps.Mailer, cfg.App.WebURL)

	// AI assistant: knowledge base = published help articles.
	articles, _ := supportSvc.AllArticles(ctxPlaceholder())
	docs := make([]ai.Document, 0, len(articles))
	for _, a := range articles {
		if !a.IsPublished {
			continue
		}
		docs = append(docs, ai.Document{ID: a.ID, Title: a.Title, Content: a.Content, Source: a.Section})
	}
	assistant := ai.NewAssistant(ai.Config{
		BaseURL: cfg.AI.BaseURL, APIKey: cfg.AI.APIKey, Model: cfg.AI.Model,
	}, docs, logger)
	if assistant.LLMEnabled() {
		logger.Info("ai assistant: llm mode", "model", cfg.AI.Model)
	} else {
		logger.Info("ai assistant: offline retrieval mode (set AI_API_KEY for LLM)")
	}
	aiSvc := service.NewAIService(assistant, products, categories)
	aiSvc.SetContext(orders, reviews)
	orderSvc.SetBroker(broker)
	orderSvc.SetPaymentService(paymentSvc)
	orderSvc.SetNotificationService(notificationSvc)
	orderSvc.SetUsers(users)
	orderSvc.SetMailer(deps.Mailer, cfg.App.WebURL)
	// The order service has post-commit paths (loyalty debit, email send)
	// where the work is already durable; the logger is the only way their
	// failures stay visible.
	orderSvc.SetLogger(logger)
	paymentSvc.SetBroker(broker)
	paymentSvc.SetNotificationService(notificationSvc)
	paymentSvc.SetUsers(users)
	// A "split" dispute decision moves real money, so the payment service must
	// be able to claim the dispute row inside its own transaction.
	paymentSvc.SetDisputes(disputeRepo)
	paymentSvc.SetMailer(deps.Mailer, cfg.App.WebURL)
	marketSvc.SetPaymentService(paymentSvc)
	sellerSvc.SetMailer(deps.Mailer, cfg.App.WebURL)
	sellerSvc.SetNotificationService(notificationSvc)
	supportSvc.SetNotificationService(notificationSvc)

	// handlers
	health := handler.NewHealth(deps.Pool, deps.Redis.Client, cfg)
	auth := handler.NewAuth(authSvc, logger, cfg)
	oauth := handler.NewOAuth(oauthSvc)
	catalog := handler.NewCatalog(catalogSvc)
	product := handler.NewProduct(productSvc)
	cart := handler.NewCart(orderSvc)
	checkout := handler.NewCheckout(orderSvc, deps.Redis.Client)
	ordersH := handler.NewOrders(orderSvc, productSvc)
	addressesH := handler.NewAddresses(orderSvc)
	paymentsH := handler.NewPayments(paymentSvc)
	wallet := handler.NewWallet(paymentSvc)
	seller := handler.NewSeller(sellerSvc)
	buyerReturns := handler.NewBuyerReturns(sellerSvc)
	admin := handler.NewAdmin(sellerSvc)
	adminOps := handler.NewAdminOps(sellerSvc, flagRepo, deps.Redis.Client)
	adminOps.SetAuth(authSvc)
	adminReviews := handler.NewAdminReviews(productSvc)
	media := handler.NewMedia(service.NewMediaService(filepath.Join(cfg.App.UploadDir), cfg.App.BaseURL, 5<<20))
	chatH := handler.NewChat(chatSvc)
	aiH := handler.NewAI(aiSvc)
	marketH := handler.NewMarket(marketSvc)
	engagement := handler.NewEngagement(engagementSvc)
	streamH := handler.NewStream(broker, chatSvc, logger)
	streamH.SetLive(liveSvc)
	liveH := handler.NewLive(liveSvc)
	analyticsH := handler.NewAnalytics(analyticsSvc)
	support := handler.NewSupport(supportSvc)
	notificationsH := handler.NewNotifications(notificationSvc)

	// middleware
	rateLimiter := mw.NewRateLimiter(deps.Redis.Client)

	r := chi.NewRouter()
	// Metrics is registered first so it is the OUTERMOST wrapper. chi's
	// chain() applies the last-registered middleware innermost, so whichever
	// recorder wraps the handler directly is the one a handler's
	// `w.(http.Flusher)` assertion depends on. Keeping the response-writer
	// instrumentation layers innermost means streaming endpoints work even
	// if an outer wrapper forgets to forward the optional interfaces.
	r.Use(metricsReg.Middleware)
	// chi's RealIP is replaced by middleware.ClientIP. RealIP is documented as
	// vulnerable to spoofing and unconditionally takes the leftmost
	// X-Forwarded-For entry — i.e. whatever the client sent first. That value
	// keyed every per-IP rate limiter, so a fresh forged header per request
	// gave unlimited login attempts, unlimited registration, and unmetered LLM
	// spend. See middleware/requestid.go.
	mw.SetTrustedProxies(cfg.TrustedProxyCIDRs)
	r.Use(timeoutExceptStreams(60 * time.Second))
	r.Use(chimw.Recoverer)
	r.Use(chimw.Compress(5))
	r.Use(cors.Handler(cors.Options{
		AllowedOrigins:   cfg.App.SplitOrigins(),
		AllowedMethods:   []string{"GET", "POST", "PUT", "PATCH", "DELETE", "OPTIONS"},
		AllowedHeaders:   []string{"Accept", "Authorization", "Content-Type", "X-Request-ID", "X-Idempotency-Key"},
		ExposedHeaders:   []string{"X-Request-ID"},
		AllowCredentials: true,
		MaxAge:           300,
	}))
	r.Use(mw.RequestID)
	r.Use(mw.Logging(logger))

	authMw := mw.Authenticate(tokens, mw.CachedVerChecker(users, 30*time.Second))
	optionalAuthMw := mw.AuthenticateOptional(tokens, mw.CachedVerChecker(users, 30*time.Second))
	auditMw := mw.AuditMiddleware(sessions, logger)
	flagMw := adminOps.FeatureFlagMiddleware

	r.Route("/api/v1", func(r chi.Router) {
		r.Get("/health/live", health.Liveness)
		r.Get("/health/ready", health.Readiness)

		r.Route("/catalog", func(r chi.Router) {
			r.Get("/categories", catalog.Tree)
			r.Get("/brands", catalog.Brands)
			r.Get("/attributes", catalog.Attributes)
			r.With(authMw, mw.RequireRoles(domain.RoleAdmin)).
				Post("/categories", catalog.CreateCategory)
			r.With(authMw, mw.RequireRoles(domain.RoleAdmin)).
				Post("/brands", catalog.CreateBrand)
			r.With(authMw, mw.RequireRoles(domain.RoleAdmin)).
				Post("/brands/{id}/toggle", catalog.ToggleBrand)
			r.With(authMw, mw.RequireRoles(domain.RoleAdmin)).
				Post("/attributes", catalog.CreateAttribute)
		})

		r.Route("/products", func(r chi.Router) {
			r.Get("/", product.Search)
			r.Get("/{slug}", product.BySlug)
			r.Get("/{id}/related", product.Related)
			r.Get("/{id}/reviews", product.Reviews)
			r.Get("/{id}/photos", product.CustomerPhotos)
			r.With(authMw).Post("/{id}/reviews", product.CreateReview)
			r.With(authMw).Post("/reviews/{id}/helpful", product.ToggleReviewHelpful)
			r.With(optionalAuthMw, rateLimiter.Limit(60, time.Minute, userKey)).
				Post("/{id}/view", analyticsH.View)
			r.With(authMw).Post("/{id}/report", product.ReportProduct)
		})

		r.Route("/auth", func(r chi.Router) {
			r.With(rateLimiter.Limit(10, time.Minute, ipKey)).
				Post("/register", auth.Register)
			// Bucketed on (IP, email): see loginKey for why IP-only is not
			// enough against a distributed credential attack.
			r.With(rateLimiter.Limit(10, time.Minute, loginKey)).
				Post("/login", auth.Login)
			r.Get("/oauth/google/start", oauth.Start)
			r.Post("/oauth/google/callback", oauth.Callback)
			r.With(rateLimiter.Limit(30, time.Minute, ipKey)).Post("/refresh", auth.Refresh)
			r.Post("/logout", auth.Logout)

			r.Group(func(r chi.Router) {
				r.Use(authMw)
				r.Get("/me", auth.Me)
				r.Post("/logout-all", auth.LogoutAll)
				r.Get("/sessions", auth.Sessions)
				r.Post("/sessions/revoke", auth.RevokeSession)
				r.Post("/verify-email/request", auth.RequestEmailVerification)
				r.Post("/verify-email", auth.VerifyEmail)
				r.With(rateLimiter.Limit(3, time.Hour, userKey)).
					Post("/password/reset-request", auth.RequestPasswordReset)
				r.With(rateLimiter.Limit(5, time.Minute, userKey)).
					Post("/password/reset", auth.ResetPassword)
				r.Post("/password/change", auth.ChangePassword)
				r.Put("/profile", auth.UpdateProfile)
				r.Post("/2fa/setup", auth.SetupTOTP)
				r.Post("/2fa/confirm", auth.ConfirmTOTP)
				r.Post("/2fa/disable", auth.DisableTOTP)
				r.Get("/2fa/backup-codes", auth.BackupCodes)
				r.Post("/2fa/backup-codes/regenerate", auth.RegenerateBackupCodes)
			})
		})

		r.Route("/cart", func(r chi.Router) {
			r.Use(optionalAuthMw)
			r.Get("/", cart.Get)
			r.Post("/items", cart.Add)
			r.Put("/items", cart.Update)
			r.Delete("/items/{variantId}", cart.Remove)
			r.Post("/bulk-remove", cart.BulkRemove)
			r.With(authMw).Post("/bulk-move", cart.BulkMove)
			r.With(authMw).Post("/merge", cart.Merge)
		})

		r.Route("/checkout", func(r chi.Router) {
			r.Use(authMw)
			r.With(rateLimiter.Limit(30, time.Minute, userKey)).
				Post("/quote", checkout.Quote)
			r.With(rateLimiter.Limit(15, time.Minute, userKey), auditMw).
				Post("/place", checkout.Place)
			r.With(rateLimiter.Limit(15, time.Minute, userKey), auditMw).
				Post("/buy-now", checkout.BuyNow)
		})

		r.Route("/orders", func(r chi.Router) {
			r.Use(authMw)
			r.Get("/", ordersH.List)
			r.Get("/tracking/{number}", ordersH.ByNumber)
			r.Get("/{id}", ordersH.ByID)
			r.Get("/{id}/events", ordersH.Events)
			r.Get("/{id}/invoice", ordersH.Invoice)
			r.Get("/{id}/packing-slip", ordersH.PackingSlip)
			r.Post("/{id}/cancel", ordersH.Cancel)
			r.Post("/{id}/external-payment", ordersH.ExternalPayment)
			r.Post("/{id}/confirm-delivery", ordersH.ConfirmDelivery)
			r.Post("/{id}/complete", ordersH.Complete)
			r.Post("/{id}/reviews", ordersH.ReviewOrderItem)
			r.Post("/{id}/reorder", ordersH.Reorder)
		})

		r.Route("/payments", func(r chi.Router) {
			r.With(rateLimiter.Limit(60, time.Minute, ipKey)).
				Post("/webhook/{gateway}", paymentsH.Webhook)
			r.Group(func(r chi.Router) {
				r.Use(authMw)
				r.Post("/orders/{orderId}/intent", paymentsH.Initiate)
				r.Get("/orders/{orderId}/intent", paymentsH.Intent)
				r.With(mw.RequireRoles(domain.RoleAdmin, domain.RoleSupport), auditMw).
					Post("/orders/{orderId}/release", paymentsH.Release)
				r.With(mw.RequireRoles(domain.RoleAdmin, domain.RoleSupport), auditMw).
					Post("/orders/{orderId}/refund", paymentsH.Refund)
			})
			// dev-only sandbox drivers (order owner or staff) — never in production.
			if cfg.IsDev() {
				r.Group(func(r chi.Router) {
					r.Use(authMw)
					r.Post("/sandbox/orders/{orderId}/approve", paymentsH.SandboxApprove)
					r.Post("/sandbox/orders/{orderId}/fail", paymentsH.SandboxFail)
				})
			}
		})

		r.Route("/wallet", func(r chi.Router) {
			r.Use(authMw)
			r.Get("/", wallet.Get)
			r.Get("/payouts", wallet.Payouts)
			r.Post("/payouts", wallet.RequestPayout)
		})

		r.Route("/seller", func(r chi.Router) {
			r.Use(authMw)
			// Store open/view stay auth-only: that's the buyer→seller
			// onboarding funnel. Everything else requires the seller role —
			// ownership checks remain as defense-in-depth beneath this gate.
			r.Get("/store", seller.MyStore)
			r.Post("/store", seller.OpenStore)
			r.Group(func(r chi.Router) {
				r.Use(mw.RequireRoles(domain.RoleSeller, domain.RoleAdmin))
				r.Put("/store", seller.UpdateStore)
				r.Get("/kyc", seller.KYC)
				r.Post("/kyc", seller.SubmitKYC)
				r.Get("/dashboard", seller.Dashboard)
				r.Get("/low-stock", seller.LowStock)
				r.Get("/analytics", analyticsH.Seller)
				r.Get("/analytics/export.csv", analyticsH.SellerCSV)
				r.Get("/orders/export.csv", analyticsH.SellerOrdersCSV)
				r.Get("/products", seller.Products)
				r.Get("/questions", marketH.SellerQuestions)
				r.Get("/reviews", product.SellerReviews)
				r.Post("/reviews/{id}/reply", product.ReplyReview)
				r.Post("/products", seller.CreateProduct)
				r.Post("/products/import", seller.ImportProducts)
				r.Get("/products/import-template", seller.ImportTemplate)
				r.Put("/products/{id}", seller.UpdateProduct)
				r.Post("/products/{id}/status", seller.UpdateProductStatus)
				r.Post("/stock/{variantId}/adjust", seller.AdjustStock)
				r.Post("/bundles", marketH.CreateBundle)
				r.Get("/coupons", seller.SellerCoupons)
				r.Post("/coupons", seller.CreateSellerCoupon)
				r.Put("/free-shipping", seller.SetFreeShipping)
				r.Get("/returns", seller.Returns)
				r.Get("/orders", ordersH.SellerList)
				r.Post("/returns/{id}/decide", seller.DecideReturn)
				r.Post("/orders/{id}/transition", seller.FulfillOrder)
				r.Get("/live", liveH.MySessions)
				r.Post("/live", liveH.Create)
				r.Post("/live/{id}/transition", liveH.Transition)
				r.Put("/live/{id}/products", liveH.Attach)
				r.Get("/live/{id}/catalog", liveH.Catalog)
				r.Post("/live/{id}/pin", liveH.Pin)
			})
		})

		r.Route("/returns", func(r chi.Router) {
			r.Use(authMw)
			r.Get("/", buyerReturns.List)
			r.Post("/", buyerReturns.Request)
		})

		r.Route("/admin", func(r chi.Router) {
			r.Use(authMw, mw.RequireRoles(domain.RoleAdmin), auditMw)
			r.Get("/stores", admin.Stores)
			r.Get("/kyc", admin.KYCPending)
			r.Post("/stores/{id}/decide", admin.DecideStore)
			r.Post("/stores/{id}/kyc/decide", admin.DecideKYC)
			r.Post("/returns/{id}/refund", admin.RefundReturn)
			r.Get("/analytics", analyticsH.Platform)
			r.Get("/pending-counts", analyticsH.PendingCounts)
			r.Get("/analytics/export.csv", analyticsH.PlatformCSV)
			r.Get("/articles", support.AllArticles)
			r.Post("/articles", support.CreateArticle)
			r.Put("/articles/{id}", support.UpdateArticle)
			r.Delete("/articles/{id}", support.DeleteArticle)
			r.Get("/users", adminOps.Users)
			r.Post("/users/{id}/status", adminOps.SetUserStatus)
			r.Post("/users/{id}/grant-seller", adminOps.GrantSeller)
			r.Post("/users/{id}/revoke-role", adminOps.RevokeRole)
			r.Post("/users/{id}/impersonate", adminOps.Impersonate)
			r.Get("/coupons", adminOps.Coupons)
			r.Post("/coupons", adminOps.CreateCoupon)
			r.Post("/coupons/{id}/toggle", adminOps.ToggleCoupon)
			r.Get("/flags", adminOps.Flags)
			r.Post("/flags/{key}/toggle", adminOps.ToggleFlag)
			r.Get("/returns", admin.Returns)
			r.Get("/reviews/pending", adminReviews.Pending)
			r.Post("/reviews/{id}/moderate", adminReviews.Moderate)
			r.Get("/shipping", adminOps.Shipping)
			r.Post("/shipping", adminOps.CreateShipping)
			r.Post("/shipping/{id}/toggle", adminOps.ToggleShipping)
			r.Get("/orders", adminOps.Orders)
			r.Get("/audit", adminOps.Audit)
			r.Get("/commission", adminOps.Commission)
			r.Put("/commission", adminOps.SetCommission)
			r.Get("/flash-sales", marketH.FlashSales)
			r.Post("/flash-sales", marketH.CreateFlashSale)
			r.Post("/flash-sales/{id}/items", marketH.AddFlashSaleItems)
			r.Post("/flash-sales/{id}/toggle", marketH.ToggleFlashSale)
			r.Get("/users/{id}", adminOps.UserDetail)
			r.Get("/payouts", wallet.AdminPayouts)
			r.Post("/payouts/{id}/process", wallet.ProcessPayout)
			r.Get("/disputes", marketH.Disputes)
			r.Post("/disputes/{id}/resolve", marketH.ResolveDispute)
			r.Get("/reports", product.AdminReports)
			r.Post("/reports/{id}/resolve", product.ResolveReport)
		})

		r.Route("/wishlist", func(r chi.Router) {
			r.Use(authMw)
			r.Get("/", engagement.List)
			r.Post("/items/{variantId}", engagement.Add)
			r.Delete("/items/{variantId}", engagement.Remove)
		})

		r.With(flagMw("flash_sales")).Get("/flash-sales/active", engagement.FlashSale)
		r.With(optionalAuthMw, rateLimiter.Limit(60, time.Minute, userKey)).
			Get("/feed", engagement.Feed)
		r.Get("/shipping/methods", adminOps.PublicShipping)
		r.Get("/recommendations", engagement.Recommended)

		// livestream commerce
		r.With(flagMw("live_commerce")).Get("/live", liveH.List)
		r.With(flagMw("live_commerce")).Get("/live/{id}", liveH.Detail)
		r.With(flagMw("live_commerce")).Get("/live/{id}/pinned", liveH.Pinned)
		r.With(flagMw("live_commerce"), authMw).Post("/live/{id}/chat", liveH.ChatPost)
		r.With(authMw, flagMw("live_commerce")).Get("/stream/live/{id}", streamH.Live)
		r.With(optionalAuthMw).Get("/stores/{slug}", seller.PublicStore)
		r.With(authMw).Post("/stores/{id}/follow", seller.FollowStore)
		r.With(authMw).Delete("/stores/{id}/follow", seller.UnfollowStore)
		r.With(authMw).Get("/followed-stores", seller.FollowedStores)
		r.With(authMw).Get("/followed-stores/feed", seller.FollowedFeed)
		r.Get("/bundles", marketH.Bundles)
		r.Get("/products/{id}/qa", marketH.QA)
		r.With(authMw).Post("/products/{id}/qa", marketH.Ask)
		r.With(authMw).Post("/qa/{id}/answer", marketH.Answer)
		r.With(authMw).Post("/price-alerts", marketH.Watch)
		r.With(authMw).Get("/price-alerts", marketH.PriceAlerts)
		r.With(authMw).Delete("/price-alerts/{id}", marketH.CancelAlert)
		r.With(authMw).Post("/back-in-stock", marketH.WatchRestock)
		r.With(authMw).Get("/back-in-stock", marketH.BackInStock)
		r.With(authMw).Delete("/back-in-stock/{id}", marketH.CancelBackInStock)
		r.With(authMw).Post("/engagement/checkin", marketH.CheckIn)
		r.With(authMw, flagMw("games")).Get("/engagement/checkin/status", marketH.CheckInStatus)
		r.With(authMw, flagMw("loyalty_points")).Get("/loyalty", marketH.Loyalty)
		r.With(authMw).Get("/referral/code", marketH.Referral)
		// Rate limited per user, not just per IP. Without this, calling redeem
		// in a loop granted unlimited loyalty points (worth unlimited checkout
		// discount). The per-user unique index on loyalty_ledger
		// (user_id, reason) is the durable guard added in migration 00040;
		// this limiter keeps the endpoint from being a hot loop at all.
		r.With(authMw, flagMw("referrals"), rateLimiter.Limit(5, time.Hour, userKey)).
			Post("/referral/redeem", marketH.RedeemReferral)
		r.Get("/vouchers", adminOps.Vouchers)
		r.With(authMw).Get("/vouchers/claims", marketH.MyClaims)
		r.With(authMw).Post("/vouchers/claim", marketH.ClaimVoucher)
		r.With(flagMw("games"), authMw).Post("/games/spin", marketH.Spin)
		r.With(flagMw("games"), authMw).Get("/games/status", marketH.SpinStatus)
		r.With(authMw).Post("/disputes", marketH.OpenDispute)
		r.With(authMw).Get("/stream/orders", streamH.Orders)

		r.Route("/help", func(r chi.Router) {
			r.Get("/categories", support.Categories)
			r.Get("/articles", support.Articles)
			r.Get("/articles/{slug}", support.Article)
		})

		r.Route("/tickets", func(r chi.Router) {
			r.Use(authMw)
			r.Get("/", support.MyTickets)
			r.Post("/", support.OpenTicket)
			r.Get("/{id}", support.TicketDetail)
			r.Post("/{id}/messages", support.Reply)
		})

		r.Route("/support", func(r chi.Router) {
			r.Use(authMw, mw.RequireRoles(domain.RoleSupport, domain.RoleAdmin))
			r.Get("/tickets", support.Queue)
			r.Post("/tickets/{id}/status", support.SetStatus)
			r.Post("/tickets/{id}/assign", support.Assign)
			r.Get("/chat/queue", chatH.Queue)
			r.Post("/chat/sessions/{id}/claim", chatH.Claim)
			r.Post("/chat/sessions/{id}/messages", chatH.Send)
		})

		r.Route("/notifications", func(r chi.Router) {
			r.Use(authMw)
			r.Get("/", notificationsH.List)
			r.Get("/unread-count", notificationsH.Unread)
			r.Post("/read", notificationsH.MarkRead)
			r.Get("/preferences", notificationsH.Preferences)
			r.Put("/preferences", notificationsH.SetPreference)
		})

		r.Route("/media", func(r chi.Router) {
			r.Use(authMw)
			// Uploads stay open to all authenticated users (buyer avatars,
			// review photos) but are throttled per-user to prevent the
			// endpoint becoming a free file host. The body itself is capped by
			// handler.MaxUploadBytes via http.MaxBytesReader.
			r.With(rateLimiter.Limit(20, time.Hour, userKey)).
				Post("/upload", media.Upload)
		})

		r.Route("/chat", func(r chi.Router) {
			r.Use(authMw)
			r.Get("/sessions", chatH.List)
			r.Post("/sessions", chatH.Open)
			r.Get("/sessions/{id}", chatH.Detail)
			r.With(rateLimiter.Limit(30, time.Minute, userKey)).
				Post("/sessions/{id}/messages", chatH.Send)
			r.Post("/sessions/{id}/close", chatH.Close)
			r.Get("/orders/{orderId}", chatH.SellerChatForOrder)
			r.Post("/orders/{orderId}", chatH.OpenSellerChat)
		})

		r.Route("/ai", func(r chi.Router) {
			r.Use(authMw)
			r.With(flagMw("ai_assistant"), rateLimiter.Limit(20, time.Minute, userKey)).Post("/ask", aiH.Ask)
			// review-summary renders on the public product page, so any
			// authenticated user may call it — the per-user limiter keeps
			// LLM cost bounded.
			r.With(rateLimiter.Limit(30, time.Minute, userKey)).Post("/review-summary", aiH.ReviewSummary)
			r.With(mw.RequireRoles(domain.RoleSeller, domain.RoleAdmin), rateLimiter.Limit(20, time.Minute, userKey)).
				Post("/describe-product", aiH.DescribeProduct)
			r.With(mw.RequireRoles(domain.RoleSeller, domain.RoleAdmin), rateLimiter.Limit(20, time.Minute, userKey)).
				Post("/title-suggest", aiH.TitleSuggestions)
		})

		r.Get("/search/suggestions", aiH.Suggest)
		r.With(authMw).Get("/stream/chat", streamH.Chat)

		r.Route("/account", func(r chi.Router) {
			r.Use(authMw)
			r.Get("/addresses", addressesH.List)
			r.Post("/addresses", addressesH.Create)
			r.Put("/addresses/{id}", addressesH.Update)
			r.Delete("/addresses/{id}", addressesH.Delete)
		})
	})

	// static media: nosniff + no directory listing (uploads are immutable raster images)
	uploads := http.FileServer(http.Dir(filepath.Join(cfg.App.UploadDir)))
	r.Handle("/uploads/*", http.StripPrefix("/uploads/", http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
		if strings.HasSuffix(req.URL.Path, "/") {
			http.NotFound(w, req)
			return
		}
		w.Header().Set("X-Content-Type-Options", "nosniff")
		w.Header().Set("Cache-Control", "public, max-age=604800")
		uploads.ServeHTTP(w, req)
	})))

	// SEO
	seo := handler.NewSEO(products, categories, cache.NewStore(deps.Redis.Client), cfg.App.WebURL)
	r.Get("/robots.txt", seo.Robots)
	r.Get("/sitemap.xml", seo.Sitemap)

	// API documentation (OpenAPI + Swagger UI)
	r.Handle("/docs", SwaggerUIHandler())
	r.Handle("/docs/*", SwaggerUIHandler())
	r.Handle("/docs/openapi.json", OpenAPIHandler(cfg.App.BaseURL+"/api/v1"))

	// observability: /metrics is admin-only (scrape via internal network with a token)
	r.Group(func(r chi.Router) {
		r.Use(authMw, mw.RequireRoles(domain.RoleAdmin))
		r.Get("/metrics", metricsReg.Handler().ServeHTTP)
	})

	return r
}

// ipKey produces the rate-limit bucket for an unauthenticated request.
//
// It uses the resolved client IP WITHOUT the port. The previous version used
// r.RemoteAddr directly, which is "ip:port" whenever no proxy header is
// present — and the source port is ephemeral, so every new connection landed
// in a fresh bucket. A trivial client that opens one socket per request
// defeated /auth/login, /auth/register and the webhook limiter completely.
//
// It also no longer trusts a client-supplied X-Forwarded-For: see
// middleware.ClientIP, which only honours the header when the direct peer is
// a configured trusted proxy.
func ipKey(r *http.Request) string { return "ip:" + mw.ClientIP(r) }

// loginKeyFn is the rate-limit key for credential endpoints. See loginKey.
// loginKey buckets login attempts on (client IP, normalised email).
//
// IP-only limiting is not enough for credential attacks: a botnet spraying one
// account from thousands of addresses gets 10 attempts per address, which is
// effectively unlimited against a single account. Pairing the IP with the
// target email bounds the per-account rate regardless of how the attempts are
// distributed, while the IP component still bounds a single-source spray.
//
// The email is hashed rather than stored so an address is not recoverable from
// a Redis key dump (KEYS * on a shared box is a data-exfiltration primitive).
func loginKey(r *http.Request) string {
	ip := ipKey(r)

	email := peekEmail(r)
	if email == "" {
		return ip
	}
	sum := sha256.Sum256([]byte(email))
	return ip + ":" + hex.EncodeToString(sum[:8])
}

// maxPeekBody bounds how much of a request body loginKey will read. Login
// payloads are tiny; a larger body is not a login, so the peek stops and the
// remainder is still forwarded intact.
const maxPeekBody = 4 << 10

// peekedBody re-joins a buffered prefix with the rest of the original stream.
type peekedBody struct {
	*bytes.Reader
	rest  io.ReadCloser
	close bool
}

func (p *peekedBody) Read(b []byte) (int, error) {
	n, err := p.Reader.Read(b)
	if err == io.EOF && p.rest != nil {
		m, rerr := p.rest.Read(b[n:])
		if m > 0 {
			return n + m, rerr
		}
		return n, nil
	}
	return n, err
}

func (p *peekedBody) Close() error {
	if p.close && p.rest != nil {
		return p.rest.Close()
	}
	return nil
}

// peekEmail extracts the email from a JSON body WITHOUT consuming it.
//
// The previous version replaced r.Body with a reader over the capped prefix
// only, so both failure paths truncated the request: a body over 4 KiB reached
// the handler as its first 4097 bytes, and a mid-body read error delivered a
// partial payload. Either way auth.Login failed with a JSON parse error rather
// than the real 413/400 — and the doc comment claimed the opposite.
//
// Here the prefix is re-joined to the untouched remainder, so the handler
// always sees the complete original body.
func peekEmail(r *http.Request) string {
	if r.Body == nil || r.Method != http.MethodPost {
		return ""
	}
	orig := r.Body
	prefix, err := io.ReadAll(io.LimitReader(orig, maxPeekBody))
	// Always re-attach, including on error: a rate-limit key function must
	// never break the request it is measuring.
	r.Body = &peekedBody{Reader: bytes.NewReader(prefix), rest: orig, close: true}
	if err != nil || len(prefix) == 0 {
		return ""
	}

	var payload struct {
		Email string `json:"email"`
	}
	// A body at or over the cap was truncated, so the JSON is incomplete and
	// cannot be trusted to be the whole object. Fall back to the IP bucket.
	if err := json.Unmarshal(prefix, &payload); err != nil {
		return ""
	}
	return strings.ToLower(strings.TrimSpace(payload.Email))
}

// timeoutExceptStreams applies a request timeout to every route EXCEPT the
// long-lived SSE streams, which would otherwise be killed mid-flight at the
// deadline (causing reconnect storms).
func timeoutExceptStreams(d time.Duration) func(http.Handler) http.Handler {
	inner := chimw.Timeout(d)
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
			if strings.HasPrefix(req.URL.Path, "/api/v1/stream/") {
				next.ServeHTTP(w, req)
				return
			}
			inner(next).ServeHTTP(w, req)
		})
	}
}

func userKey(r *http.Request) string {
	if u := mw.UserFrom(r.Context()); u != nil {
		return "user:" + u.ID
	}
	return ipKey(r)
}
