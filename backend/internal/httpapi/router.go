package httpapi

import (
	"context"
	"log/slog"
	"net/http"
	"path/filepath"
	"strings"
	"time"

	"github.com/go-chi/chi/v5"
	chimw "github.com/go-chi/chi/v5/middleware"
	"github.com/go-chi/cors"
	"github.com/redis/go-redis/v9"
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
	// payment gateways: sandbox always available; Midtrans when keys configured.
	gateways := []payments.Gateway{}
	if gw, err := payments.NewGateway("sandbox", cfg.Payments.SandboxBaseURL, "", "", nil); err == nil {
		gateways = append(gateways, gw)
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
	sellerSvc := service.NewSellerService(stores, users, products, orders, paymentRepo)
	sellerSvc.SetSessions(sessions)
	engagementSvc := service.NewEngagementService(wishlistRepo, products)
	engagementSvc.SetCache(cache.NewStore(deps.Redis.Client))
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
	paymentSvc.SetBroker(broker)
	paymentSvc.SetNotificationService(notificationSvc)
	paymentSvc.SetUsers(users)
	paymentSvc.SetMailer(deps.Mailer, cfg.App.WebURL)
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
	analyticsH := handler.NewAnalytics(analyticsSvc)
	support := handler.NewSupport(supportSvc)
	notificationsH := handler.NewNotifications(notificationSvc)

	// middleware
	rateLimiter := mw.NewRateLimiter(deps.Redis.Client)

	r := chi.NewRouter()
	r.Use(chimw.RealIP)
	r.Use(chimw.Timeout(60 * time.Second))
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

	authMw := mw.Authenticate(tokens)
	optionalAuthMw := mw.AuthenticateOptional(tokens)
	auditMw := mw.AuditMiddleware(sessions)
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
			r.With(authMw).Post("/{id}/reviews", product.CreateReview)
			r.With(authMw).Post("/reviews/{id}/helpful", product.ToggleReviewHelpful)
			r.With(optionalAuthMw, rateLimiter.Limit(60, time.Minute, userKey)).
				Post("/{id}/view", analyticsH.View)
			r.With(authMw).Post("/{id}/report", product.ReportProduct)
		})

		r.Route("/auth", func(r chi.Router) {
			r.With(rateLimiter.Limit(10, time.Minute, ipKey)).
				Post("/register", auth.Register)
			r.With(rateLimiter.Limit(10, time.Minute, ipKey)).
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
				r.Post("/password/reset-request", auth.RequestPasswordReset)
				r.Post("/password/reset", auth.ResetPassword)
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
			r.Post("/webhook/{gateway}", paymentsH.Webhook)
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
			if cfg.Environment == "" || cfg.Environment == "development" {
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
			r.Get("/store", seller.MyStore)
			r.Post("/store", seller.OpenStore)
			r.Put("/store", seller.UpdateStore)
			r.Get("/kyc", seller.KYC)
			r.Post("/kyc", seller.SubmitKYC)
			r.Get("/dashboard", seller.Dashboard)
			r.Get("/low-stock", seller.LowStock)
			r.Get("/analytics", analyticsH.Seller)
			r.Get("/analytics/export.csv", analyticsH.SellerCSV)
			r.Get("/orders/export.csv", analyticsH.SellerOrdersCSV)
			r.Get("/products", seller.Products)
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
		r.Get("/recommendations", engagement.Recommended)
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
		r.With(authMw).Get("/loyalty", marketH.Loyalty)
		r.With(authMw).Get("/referral/code", marketH.Referral)
		r.With(authMw, flagMw("referrals")).Post("/referral/redeem", marketH.RedeemReferral)
		r.Get("/vouchers", adminOps.Vouchers)
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
		})

		r.Route("/media", func(r chi.Router) {
			r.Use(authMw)
			r.Post("/upload", media.Upload)
		})

		r.Route("/chat", func(r chi.Router) {
			r.Use(authMw)
			r.Get("/sessions", chatH.List)
			r.Post("/sessions", chatH.Open)
			r.Get("/sessions/{id}", chatH.Detail)
			r.Post("/sessions/{id}/messages", chatH.Send)
			r.Post("/sessions/{id}/close", chatH.Close)
			r.Get("/orders/{orderId}", chatH.SellerChatForOrder)
			r.Post("/orders/{orderId}", chatH.OpenSellerChat)
		})

		r.Route("/ai", func(r chi.Router) {
			r.Use(authMw)
			r.With(flagMw("ai_assistant")).Post("/ask", aiH.Ask)
			r.Post("/review-summary", aiH.ReviewSummary)
			r.With(mw.RequireRoles(domain.RoleSeller, domain.RoleAdmin)).
				Post("/describe-product", aiH.DescribeProduct)
			r.With(mw.RequireRoles(domain.RoleSeller, domain.RoleAdmin)).
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

func ipKey(r *http.Request) string { return "ip:" + r.RemoteAddr }

func userKey(r *http.Request) string {
	if u := mw.UserFrom(r.Context()); u != nil {
		return "user:" + u.ID
	}
	return "ip:" + r.RemoteAddr
}

var _ = redis.Nil
