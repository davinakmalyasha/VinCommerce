# VinCommerce

Enterprise-grade, multi-vendor e-commerce marketplace platform (Shopee / Tokopedia / Shopify-class) — Go + React monorepo.

## Stack (newest stable)

| Component | Version | Role |
|-----------|---------|------|
| Go | 1.26.x | API server + background worker (clean architecture) |
| PostgreSQL | 17.x | Primary DB: transactions, FTS search, JSONB, triggers |
| Redis | 8.x | Cache, job queue (asynq), rate limits, realtime pub/sub |
| React | 19.x + Vite 8 | Storefront, Seller Center, Admin Console |
| Tailwind | 4.x | Styling |
| Mailpit | 1.29 | Local SMTP mock (UI at http://localhost:8025) |

## Architecture

```
backend/            Go API + worker
├── cmd/api         HTTP server (chi) — port 8080
├── cmd/worker      asynq background jobs (order timeouts, sweeps)
├── cmd/seed        Idempotent demo data (users, stores, 30 products, reviews)
└── internal/
    ├── config      env-based config
    ├── domain      pure domain models + order state machine
    ├── repository  pgx persistence
    ├── service     business logic (auth, catalog, orders, payments, seller, engagement, analytics)
    ├── httpapi     handlers + middleware (JWT, RBAC, rate limit, audit, SSE)
    ├── payments    gateway adapter pattern (sandbox gateway + webhooks)
    ├── worker      asynq tasks
    ├── stream      Redis pub/sub realtime broker
    └── metrics     Prometheus /metrics

web/                React app (port 5173)
├── src/pages       storefront (home, search, product, cart, checkout, orders, account)
├── src/pages/seller  Seller Center (dashboard, products, orders, returns, wallet, analytics)
└── src/pages/admin   Admin Console (store approval, platform analytics)
```

## Features

- **Identity**: register/login, JWT + rotating refresh tokens, Argon2id, TOTP 2FA (**with one-time backup codes**), RBAC (buyer/seller/admin/support), session management, email verification (**in-app banner + resend**) & password reset (Mailpit), Google OAuth (config-gated), audit log, Redis rate limiting. **Refresh token lives in an httpOnly cookie; the access token stays in memory with automatic silent refresh on 401.** Profile photo upload (avatar in header).
- **Catalog**: category tree, brands, attribute facets, variants/SKU/stock, full-text search with `ts_vector` ranking, price/rating/attribute filters, reviews with moderation + aggregate triggers, related products
- **Cart & Checkout**: guest + user carts with merge (**seller-grouped cart view**), coupons (percent/fixed, limits, per-user), weight-based shipping per seller, **multi-seller order splitting** (Shopee-style), inventory reservation with 30-min timeout auto-release, order state machine with event timeline, idempotency keys, **flash-sale pricing applied at checkout**, **save-address-to-book option**
- **Payments**: **Midtrans Snap checkout** (QRIS, GoPay/OVO/DANA, bank VA, cards — set `MIDTRANS_SERVER_KEY`/`MIDTRANS_CLIENT_KEY` in env, sandbox by default) with SHA512-verified webhooks and idempotent capture; buyers can also pay outside the app via their own bank transfer / e-wallet and record the external reference (no. ref, amount, date); order moves pending→paid and the normal fulfillment flow continues. Escrow, wallets, payouts and the sandbox gateway remain in the backend for development only. HMAC-signed sandbox webhooks, idempotent processing, wallets + double-entry ledger, seller payouts, full refunds
- **Marketplace**: store onboarding + KYC verification, admin approval queue, seller dashboard KPIs, product activate/deactivate, return/refund claims (buyer → seller → admin)
- **Engagement**: wishlists, flash sales, recommendations, **real-time SSE order updates** via Redis pub/sub, **persisted notification center** (bell + unread badge, **deep-linked**), **store following** (follow buttons, follower counts, followed-stores page + **new-product feed**, new-product alerts to followers), **back-in-stock alerts** (worker-driven, product-page button + account management)
- **Support**: help center (admin-editable articles with FTS search + view counts), FAQ, contact form → **support tickets** (priority, status workflow, message threads with internal staff notes, agent queue), legal pages (ToS/privacy/refund/shipping)
- **Real-time chat + AI**: floating **chat widget** (AI-first → escalate to human agent) over Redis pub/sub + SSE, staff queue with claim/close, **AI assistant** with offline TF-IDF retrieval over the knowledge base and optional **LLM mode** (drop `AI_API_KEY`/`AI_BASE_URL`/`AI_MODEL` into env for RAG answers), AI product-description generator, search autocomplete
- **Profile & media**: **file upload endpoint** (magic-byte validated) wired into products/avatars/reviews, profile edit, **change password** (revokes sessions), **2FA setup UI with QR**, address book editing, checkout picks saved addresses
- **Marketplace economics**: **platform commission engine** (configurable % + fixed, split at escrow release, platform wallet income, admin settings), **pay with wallet balance**, **COD + e-wallet** payment methods (sandbox), seller proceeds tracked per intent
- **Community**: **product Q&A** (ask/answer by sellers), **seller review replies**, **buyer↔seller order chat** (order-scoped, realtime), **price-drop alerts** (worker-driven), **loyalty points** (earned on completion, with Account ledger UI), **referral program** (mutual bonuses, shareable code + WhatsApp link, code field at registration), **dispute escalation** from returns with admin resolution, **"Toko Resmi" verified badge** (KYC-approved stores)
- **Commerce UX**: reviews from completed orders (duplicate-protected, **image galleries + star-distribution histogram**), order timeline, **tracking numbers** entered by seller at ship, **public order tracking page**, **Beli Lagi reorder**, **store coupons + My Vouchers page**, free-shipping thresholds, **bundles**, **multi-address checkout**, **compare products**, public store pages, recently-viewed strip, printable invoice, **store ratings** aggregated from seller reviews, **cart bulk actions** (select-all, bulk remove, move-to-wishlist), **search UX** (URL-persisted filters, recent & saved searches, skeleton loading, **typo-tolerant pg_trgm fallback**), **abandoned cart recovery** (worker emails + notifications with one-click checkout), **flash sale on product page** (sale price, live countdown, sold progress), **clickable notifications** (deep links to orders/products/cart), 404 page
- **Admin console**: store approval, coupon manager, user management + **user detail stats**, article editor, review moderation, returns queue, ticket queue, shipping-method manager, **flash-sale manager**, **catalog manager** (categories/brands/attributes), **platform order search**, **audit log viewer**, **commission settings**, **dispute resolution**, **product moderation queue** (buyer reports with takedown), feature flags, platform analytics
- **Seller tools**: product CRUD + **bulk CSV import with row-level errors + template download**, **low-stock alerts** (worker-driven notifications/emails + dashboard warning list), **logo/banner upload**, **order CSV export**, **store coupons**, free-shipping setting, order fulfillment with tracking, AI description generator, wallet + payouts
- **AI**: assistant (offline retrieval + optional LLM RAG) is now **order-aware** for logged-in users, **review sentiment summaries** per product, **title suggestions** for sellers, **search autocomplete** (header dropdown with products/categories/brands)
- **Enterprise hardening**: enforced checkout idempotency, per-user rate limits, Redis caching, feature-flag middleware, auto-complete worker task, dark mode, **PWA manifest**, Prometheus metrics, structured JSON logs, Dockerfiles + full-stack compose, **Playwright E2E suite** (11 tests) in CI
- **Analytics & SEO**: conversion funnel (views → carts → orders → paid, via product-view beacons), sales by category, payment-method split, new-vs-returning buyer cohorts, **platform commission earned**, **top-seller leaderboard + coupon performance**, **robots.txt + sitemap.xml** (cached), per-page meta/OG tags, **PWA service worker** (offline shell, network-first API)
- **Email (Mailpit in dev)**: welcome, verification, password reset, order confirmed/paid/shipped/completed, **cart abandoned**, **return updates**, **store/KYC decision**, **dispute outcome**, **ticket replies**, **seller daily digest** (worker `@daily`), **low-stock alert**

## Quickstart (Windows / local)

```powershell
# 1. Start dependencies: PostgreSQL (service), Redis, Mailpit
infra/scripts/start-deps.ps1

# 2. Seed the database (idempotent — safe to re-run)
cd backend
go run ./cmd/seed

# 3. Start API + worker + web
go run ./cmd/api          # :8080 — auto-migrates, no SMTP needed for local demo
go run ./cmd/worker       # background jobs
cd ../web && npm run dev  # :5173
```

Open http://localhost:5173

### Demo accounts (seeded)

| Role | Email | Password |
|------|-------|----------|
| Buyer | buyer.sample@vincommerce.com | BuyerPass123! |
| Seller | seller.elektro@vincommerce.com | SellerPass123! |
| Admin | admin@vincommerce.com | AdminPass123! |

Demo coupons: `WELCOME10` (10%, min Rp50.000) · `FLAT50K` (Rp50.000, min Rp200.000).
Payments: enable **Midtrans Snap** by dropping your sandbox `MIDTRANS_SERVER_KEY` (backend `.env`) and `VITE_MIDTRANS_CLIENT_KEY` (web env) in — a "Bayar Sekarang (Midtrans)" button then opens the Snap popup (QRIS/e-wallet/VA/cards). Without keys, payment happens outside the app: checkout → order placed → pay via your own bank transfer/e-wallet → enter the payment reference (number, amount, date) → order becomes "paid" and the seller processes it. The sandbox gateway (dev-only) is still reachable via the API for development.

## API surface (v1, under `/api/v1`)

```
auth/*  catalog/*  products/*  cart/*  checkout/*  orders/*  payments/*  wallet/*
seller/*  admin/*  support/*  tickets/*  chat/*  ai/*  returns/*  wishlist/*  notifications/*
media/*  loyalty/*  referral/*  disputes/*  price-alerts/*  back-in-stock/*  bundles/*  vouchers/*
flash-sales/*  recommendations/*  stores/*  help/*  search/suggestions  followed-stores  followed-stores/feed
auth/2fa/backup-codes (GET, regenerate)  orders/{id}/external-payment  products/{id}/view  products/{id}/report
seller/low-stock  seller/orders/export.csv
stream/orders (SSE)  stream/chat (SSE)  health/*  /metrics  /docs (Swagger UI + OpenAPI)  /uploads/*
/robots.txt  /sitemap.xml  /sw.js (PWA)
```

Interactive API docs: **http://localhost:8080/docs** · AI: set `AI_API_KEY` in env for LLM mode (offline retrieval works without it) · Payment methods: `midtrans_snap` (Midtrans popup — QRIS/e-wallet/VA/cards; enable by setting `MIDTRANS_SERVER_KEY` + `VITE_MIDTRANS_CLIENT_KEY`, see `.env.example`), bank_transfer, e_wallet, wallet (saldo), cod

## Tests & CI

- `go test ./internal/...` — unit tests (crypto, tokens, search)
- `.github/workflows/ci.yml` — format check, vet, build, tests, migrate+seed smoke test, web build
- `cd web && npx playwright test` — e2e suite (storefront, auth, **follow, restock alerts, external payment, product reports, analytics, SEO, 404**)

## Production sketch

API + worker containers behind nginx/Caddy; Postgres + Redis managed or containerized; MinIO/S3 for media; gateway swaps from `sandbox` to a real provider via `PAYMENT_GATEWAY`; Prometheus + Grafana scraping `/metrics`.
