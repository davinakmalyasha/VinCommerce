# VinCommerce

Enterprise-grade, multi-vendor e-commerce marketplace platform (Shopee / Tokopedia / Shopify-class) — Go + React monorepo.

> **Read this first.** The feature list below is tagged with its real status.
> `✅ shipped` means wired end to end (backend + UI + test where it matters),
> `⚠️ partial` means something is missing or works only in a narrow case, and
> `❌ known gap` means it is deliberately not done yet. See
> [Known gaps](#known-gaps) at the bottom for the full list — it is written by
> the same audit that produced the tags, so it is accurate as of the last commit.

## Stack (newest stable)

| Component | Version | Role |
|-----------|---------|------|
| Go | 1.26.x | API server + background worker (clean architecture) |
| PostgreSQL | 17.x | Primary DB: transactions, FTS search, JSONB, triggers, BRIN |
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
    ├── config      env-based config + production guards + environment enum
    ├── domain      pure domain models + order state machine
    ├── repository  pgx persistence
    ├── service     business logic (auth, catalog, orders, payments, seller, engagement, analytics)
    ├── httpapi     handlers + middleware (JWT, RBAC, rate limit, audit, SSE)
    ├── payments    gateway adapter pattern (sandbox gateway + Midtrans webhooks)
    ├── worker      asynq tasks
    ├── stream      Redis pub/sub realtime broker
    └── metrics     Prometheus /metrics

web/                React app (port 5173)
├── src/pages       storefront (home, search, product, cart, checkout, orders, account)
├── src/pages/seller  Seller Center (dashboard, products, orders, returns, wallet, analytics)
├── src/pages/admin   Admin Console (store approval, payouts, moderation, analytics)
├── src/lib         api client, sanitizer, formatting (unit-tested)
└── src/components  shared UI, ErrorBoundary, Modal, QueryState, Countdown
```

## Features

Status key: ✅ shipped · ⚠️ partial · ❌ known gap

- **Identity**: ✅ register/login, JWT + rotating refresh tokens **with reuse detection & family revocation**, Argon2id, TOTP 2FA **with one-time backup codes**, RBAC (buyer/seller/admin/support) + **admin impersonation with audit attribution**, email verification + resend, password reset, audit log, Redis rate limiting, profile photo upload. ✅ **Refresh token in an httpOnly cookie; access token in memory with silent refresh on 401.** ⚠️ **Session management** (list/revoke devices) is backend-only — no UI. ⚠️ **Google OAuth** is config-gated but the callback flow is not wired to a real redirect; treat as scaffolding.
- **Catalog**: ✅ category tree, brands, attribute facets, variants/SKU/stock, full-text search with ranking, price/rating/attribute filters, reviews with moderation + aggregate triggers, related products. ⚠️ Search is stemmed with the **English** config, so Indonesian synonyms ("hp" for "handphone") do not match; a trigram fallback catches typos only.
- **Cart & Checkout**: ✅ guest + user carts with merge (seller-grouped), coupons (percent/fixed, limits, per-user), weight-based shipping per seller, **multi-seller order splitting**, inventory reservation with 30-min auto-release, order state machine with event timeline, idempotency keys, flash-sale pricing at checkout, save-address-to-book. ⚠️ Multi-address checkout is honoured by the backend but the UI sends one address.
- **Payments**: ✅ **Midtrans Snap checkout** (QRIS, GoPay/OVO/DANA, bank VA, cards) with SHA512-verified, constant-time-compared webhooks and idempotent capture; buyers can also pay outside the app and record the external reference; escrow, wallets, double-entry ledger, seller payouts, refunds. ⚠️ **Refunds credit the internal wallet only** — the gateway interface has no `Refund()`, so a QRIS/VA buyer is refunded to a balance they cannot top up. See Known gaps.
- **Marketplace**: ✅ store onboarding + KYC, admin approval queue, seller dashboard KPIs, product activate/deactivate, return/refund claims (buyer → seller → admin), dispute escalation. ⚠️ No return-shipment loop: a refund is an admin click regardless of whether the goods came back.
- **Engagement**: ✅ wishlists, flash sales, **real-time SSE order updates** via Redis pub/sub, persisted notification center (deep-linked, unread badge), store following + new-product feed and alerts, back-in-stock alerts. ⚠️ "Recommendations" are **global bestsellers, not personalised** — see Known gaps.
- **Support**: ✅ help center (admin-editable articles with FTS + view counts), FAQ, contact form → support tickets with priority, status workflow, threaded messages, staff queue, legal pages, order chat.
- **Real-time chat + AI**: ✅ floating chat widget (AI-first), AI assistant with offline TF-IDF retrieval over the knowledge base plus optional LLM RAG mode, AI product-description generator, search autocomplete, livestream chat. ⚠️ **Agent replies in the buyer's widget arrive by polling, not SSE** — `GET /stream/chat` is implemented but has no client consumer.
- **Profile & media**: ✅ file upload with a raster-only allow-list and magic-byte validation (SVG rejected), profile edit, change password (revokes sessions), 2FA setup UI with QR, address book, checkout picks saved addresses.
- **Marketplace economics**: ✅ **platform commission engine** (configurable % + fixed, applied at escrow release, reversed on refund), platform wallet income, admin settings, pay with wallet balance, COD.
- **Community**: ✅ product Q&A, seller review replies, buyer↔seller order chat, price-drop alerts, loyalty points with ledger, referral program (one bonus per user, enforced by a unique index), "Toko Resmi" verified badge.
- **Commerce UX**: ✅ reviews from completed orders (duplicate-protected, image galleries, star histogram), order timeline, tracking numbers, public order tracking, reorder, store coupons + My Vouchers, free-shipping thresholds, bundles, compare, recently-viewed, printable invoice, cart bulk actions, URL-persisted search filters, **typo-tolerant pg_trgm fallback**, abandoned-cart recovery, flash-sale countdown, deep-linked notifications, 404 page. ⚠️ **Bundle pricing is display-only** — "Ambil Paket" adds items at full price.
- **Admin console**: ✅ store approval, coupon manager, user management, article editor, review moderation, returns queue, ticket queue, shipping-method manager, flash-sale manager, catalog manager, order search, **discrete, filterable audit actions**, commission settings, dispute resolution, product moderation queue, feature flags, platform analytics, payout processing. ⚠️ **User detail stats** is backend-only.
- **Seller tools**: ✅ product CRUD + **bulk CSV import with row-level errors and a 5,000-row cap**, low-stock alerts, logo/banner upload, order CSV export, store coupons, free-shipping setting, fulfilment with tracking, AI description generator, wallet + payouts.
- **AI**: ✅ assistant (offline retrieval + optional LLM RAG) that is order-aware for logged-in users, review summaries per product, title suggestions for sellers, search autocomplete. ⚠️ Review "sentiment" is **arithmetic on the star histogram**, not model output; title suggestions fall back to string concatenation without `AI_API_KEY`.
- **Enterprise hardening**: ✅ checkout idempotency, per-(IP,email) rate limits keyed on a **trusted-proxy-resolved** client IP, Redis caching, feature-flag middleware, auto-complete worker, dark mode, **installable PWA** (real 192/512/maskable icons + offline page), Prometheus metrics, structured JSON logs, request-body caps, upload type allow-listing, **refresh-token reuse detection**, **non-root containers**, digest-pinned base images.
- **Analytics & SEO**: ✅ conversion funnel, sales by category, payment-method split, new-vs-returning cohorts, commission earned, top-seller leaderboard, coupon performance, robots.txt + cached sitemap, per-page meta/OG, JSON-LD, product view tracking.
- **Email (Mailpit in dev)**: ✅ welcome, verification, password reset, order confirmed/paid/shipped/completed, cart abandoned, return updates, store/KYC decision, dispute outcome, ticket replies, seller daily digest, low-stock alert. All sending is **synchronous within the request/worker that triggers it** — see Known gaps.

### Security controls worth naming

These exist because a specific, described defect was found and fixed, and each has a regression test:

| Control | What it prevents |
|---|---|
| `middleware.ClientIP` walks `X-Forwarded-For` right-to-left against `TRUSTED_PROXY_CIDRS` | The previous code trusted the **leftmost** forwarded value unconditionally, and keyed rate limits on `ip:port`. One header per request meant unlimited login brute force, unlimited registration, and unmetered LLM spend. |
| Per-response-writer `Flush`/`Hijack`/`Push`/`Unwrap` on every instrumentation wrapper | A wrapper that dropped `http.Flusher` made `w.(http.Flusher)` fail, so **every SSE endpoint returned 500**. A regression test asserts a 200 + `text/event-stream`. |
| `APP_ENV` parsed as an enum; unknown values are a startup error | `APP_ENV=prod` silently disabled every production guard, including the JWT-secret check. |
| Secret deny-list (`config.knownPublicSecrets`) | A committed compose default satisfied the *length* check but not the sentinel check, so a production API booted with a published signing key. |
| Allowlist HTML sanitizer with a code-point URL normaliser | `DOMParser` decodes `jav&#x0A;ascript:`, which survived a `startsWith('javascript:')` check and executed on public pages. 22 obfuscation vectors are tested. |
| `class` stripped from sanitized HTML | The app is Tailwind, so an author-supplied class is a styling primitive — a viewport-sized overlay with arbitrary text is a phishing control, not a cosmetic issue. |
| Service worker never caches `/api/`, and purges on logout | Cache Storage keys are URL-only, so cached `/orders` and `/wallet` responses were served to the next user on a shared device. |
| `http.MaxBytesReader` on uploads and CSV import | The declared size limit was checked *after* `ParseMultipartForm` had already spooled the body to disk. |
| Validated audit `clientIP`, bounded request ID, discrete action | A forged `X-Forwarded-For` made the `::inet` cast fail and silently destroyed the audit row; an oversized `X-Request-ID` overflowed `entity_id`. |
| Money invariants as DB constraints (migration 00040) | Every one previously lived only in Go, so any new code path that forgot a guard could mint or lose money. |


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
Payments: enable **Midtrans Snap** by setting `MIDTRANS_SERVER_KEY` in the backend
`.env` and passing `VITE_MIDTRANS_CLIENT_KEY` as a **build arg** to the web image
(`docker-compose build --build-arg VITE_MIDTRANS_CLIENT_KEY=...`, or
`build.args` in compose) — a "Bayar Sekarang (Midtrans)" button then opens the
Snap popup (QRIS/e-wallet/VA/cards). The key is inlined into the bundle at build
time, so it must be set when the image is built, not when it is run.

Without keys, payment happens outside the app: checkout → order placed → pay
via your own bank transfer/e-wallet → enter the payment reference (number,
amount, date) → the order becomes "paid" and the seller processes it. The
sandbox gateway is reachable via the API **in development only**; the routes are
not registered when `APP_ENV` is `staging` or `production`.

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

The numbers below are real, measured by the last full run. They are here so a
reader can calibrate how much to trust the rest of this document.

| Suite | Count | What it covers |
|---|---|---|
| `go test ./internal/... -race` | 6 packages | config guards, response-writer passthrough (SSE), metrics routing, Midtrans adapter, commission/loyalty math, worker |
| `npm test` (vitest) | 77 | HTML sanitizer (22 XSS vectors), Modal focus trap, Countdown, QueryState |
| `npm run test:nginx` | 13 mutations | proves the nginx structural checker actually detects the 13 defects it claims to |
| `npx playwright test` | 39 | checkout, cart-stepper races, authz denial, error states, SSE, storefront |

```powershell
# backend
gofmt -l ./internal ./cmd   # must print nothing
go vet ./...
go test ./internal/... -count=1 -race -shuffle=on

# web
npm run typecheck && npm test && npm run lint && npm run build
npm run check:nginx && npm run test:nginx && npm run size
```

CI (`.github/workflows/ci.yml`) runs, all actions pinned to commit SHAs and all
jobs with a `timeout-minutes`: gofmt · vet · build · `go test -race` ·
`govulncheck` · seed+readiness smoke · `npm test` · oxlint · typecheck · build ·
bundle-size report · nginx structural check + mutation suite · Playwright ·
gitleaks secret scan · `npm audit` (reporting) · `docker compose config` ·
`docker buildx build` ×3 · `nginx -t` in the built image · container
healthcheck + security-header assertion.

**Honest gaps in the test posture:** there is no test that exercises
`PlaceOrder`, `RefundOrder`, `CancelOrder`, `ResolveDispute` or any migration
against a real database. Those are the highest-risk paths in the codebase and
they are covered by guards and constraints rather than by tests. The money
invariants are enforced in the schema specifically because they are not
test-covered.

## Known gaps

Written by the audit that produced the status tags above. Ordered by how much
they would cost you in a real deployment.

### P0 — blocks real money

1. **No gateway-side refund.** `payments.Gateway` has no `Refund()` method, so
   every refund credits the internal wallet. A buyer who paid QRIS/VA receives
   a balance they cannot top up (there is no top-up flow either). This is also
   a Midtrans ToS breach. *Fix: add `Refund()` to the adapter, implement
   Midtrans `POST /v2/{type}/{id}/refund`, and a `refunds` table with a manual
   fallback.*
2. **No wallet top-up.** "Pay with wallet balance" is unreachable in practice;
   the balance is only ever credited by refunds.
3. **No PPN / tax and no compliant invoice.** `handler/invoice.go` emits HTML
   with no tax line, no seller NPWP/NIB and no invoice number sequence.
   Indonesian sellers cannot expense a marketplace invoice without that.
4. **The ledger is not reconcilable to cash.** `wallets.held_balance` is never
   written, capture writes no ledger rows, and gateway inflow / bank outflow
   are unrecorded — so `SUM(wallets.balance)` cannot be tied back to real
   money and the escrow "hold" phase has no balance-sheet representation. The
   individual money paths are now guarded and constrained, but a genuine double
   entry needs a real escrow/clearing account.
5. **Money is `float64` in Go** against a `NUMERIC(14,2)` schema.
   `moneyRound` is applied where it matters and the split legs are derived to
   sum to the refund amount, but the correct fix is `decimal.Decimal`
   end to end.

### P1 — needed to be credible at scale

6. **No carrier integration.** Shipping is `base + kg × per_kg_fee` with
   integer-kg rounding; there is no RajaOngkir/Shipper rate lookup, no
   volumetric weight, and no zone pricing. Remote-area orders are mispriced.
7. **No shipment entity.** Fulfilment is whole-order and all-or-nothing: no
   split shipping, no partial shipment, no return labels, no carrier
   webhooks. This one gap blocks return logistics, bulk label printing,
   pick/pack boards and SLA reporting.
8. **Email is sent synchronously** inside the request or worker that triggers
   it, so checkout waits on 1–3 SMTP round trips. It should be enqueued.
9. **No payout schedule.** Payouts are seller-initiated and on demand, KYC-gated
   only. Real marketplaces run a T+2/T+7 batch with a reserve for COD and a
   disputes window, which is also the main fraud control.
10. **No COD reconciliation.** COD is implemented with no fee, no aging report
    and no remittance file — so uncollected cash is invisible.
11. **Search relevance is English-stemmed** and recommendations are global
    bestsellers. Both are the largest single levers on marketplace GMV and both
    are currently placeholders.
12. **WhatsApp / Web Push.** `notifications` is in-app only and worker alerts are
    email-only. In Indonesia, cart recovery over WhatsApp outperforms email by
    roughly 3:1.
13. **No fraud or abuse detection** — no review-fraud scoring, no fake-order or
    brushing detection, no refund-abuse tracking, and no risk scoring gating
    payouts.

### P2 — correctness and operability

14. **External payment is self-asserted.** A buyer can mark any pending order
    paid with a free-text reference and an amount checked against the total the
    same API just returned. It is now rate-limited and one-shot, but there is no
    receipt, no gateway record and no admin verification gate, and it creates
    no `payment_intents` row.
15. **Several endpoints are backend-only with no UI**: session management,
    admin user-detail stats, CSV template download, admin refund/release
    buttons, multi-address checkout, return evidence upload.
16. **Bundle pricing is display-only.** "Ambil Paket" adds the items at full
    per-item price.
17. **No PWA offline queue.** The cart is server-side, so add-to-cart fails
    offline even though the shell is cached.
18. **Accessibility is partial.** Modals, forms and tables were brought up to
    keyboard/screen-reader basics, but there is no automated a11y gate in CI.
19. **No rate limit falls back to a local limiter** when Redis is down — limits
    fail *open*, so a Redis blip returns `/auth/login` to unlimited attempts.
20. **No database backup or PITR ships in-repo.** `SECURITY.md` says so. For a
    platform holding escrow and a wallet ledger this is the single largest
    operational risk.
21. **No TLS terminator ships in-repo.** Compose publishes HTTP on loopback and
    relies on the operator placing a proxy in front.


## Production sketch

```
                      ┌─ TLS terminator (Caddy / nginx / LB)
                      │
   client ──HTTPS──►  web:8080  ──HTTP──►  api:8080  ──►  Postgres
                      (nginx, non-root)     (chi)          Redis
                                             │            (cache + asynq)
                                             └──► worker (asynq) ──► SMTP
```

- Set `JWT_SECRET` to a unique value (`openssl rand -base64 48`). There is
  **no default**: compose fails fast without it, and the value cannot be any
  string published in this repository.
- Set `TRUSTED_PROXY_CIDRS` to the network your reverse proxy lives on. Leave
  it **empty** if there is no proxy. This is what makes per-IP rate limiting
  correct — trusting too wide a range lets a client forge its bucket.
- `DB_MIGRATE_ON_START` defaults to `true` for convenience. For more than one
  replica, run migrations as a separate one-shot step and set it to `false`, or
  the replicas will race applying DDL at boot.
- The worker's Redis credentials must match the API's. If they differ, every
  scheduled job silently stops with no error — the worker now pings the queue at
  boot and refuses to start if it cannot reach it, precisely because that
  failure used to be invisible.
- Prometheus scraping `/metrics` needs an admin bearer token today; in
  production put it behind an internal-network ACL instead of a long-lived
  admin credential.
- Back up Postgres (and rehearse a restore) before holding real escrow. No
  backup tooling ships in this repo.

