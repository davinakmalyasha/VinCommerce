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

> **Status as of the latest audit pass.** The P0 items below are being closed in
> dependency order: checkout correctness, then the ledger, then the regulatory
> and fulfilment subsystems. Items marked **[closed]** were fixed in this
> series with a named regression test; see the commit history for the failure
> mode each one fixes. The list is deliberately kept rather than rewritten, so
> the before/after is visible.

### P0 — blocks real money

1. **[partly closed] No gateway-side refund.** `payments.Gateway` had no
   `Refund()` method, so every refund was an internal book transfer: a buyer who
   paid QRIS or bank VA was refunded to a wallet balance they cannot top up
   (there is no top-up flow either). This is also a Midtrans ToS breach.
   Now implemented: `Gateway.Refund` with the Midtrans
   `POST /v2/{type}/{id}/refund` call, a `refunds` table, and the state machine an
   operator needs — `pending` / `submitted` / `succeeded` / `failed` / `manual`,
   where `manual` means a human must transfer the money out of band. A refund that
   silently failed is worse than one that is visibly stuck.
   Three details that are easy to get wrong and are covered by tests:
   a 200 means the refund was **accepted, not settled**, so it maps to `pending`
   and the durable signal is a later provider notification; a 404 is reported as
   the order-id-instead-of-transaction-id mistake rather than a bare 404; and a
   409 conflict is `manual`, not retryable, because a conflict usually means a
   refund already went through under another attempt and retrying double-refunds
   the buyer. `GatewayStatusProvider.RefundStatus` reads the provider's own view
   for reconciliation, and `ReconcileRefunds` polls it: a refund the provider
   accepted stays `submitted` until somebody asks, and without the job it would
   sit pending forever while the operator queue fills with refunds that may or may
   not have paid out. The reserve/submit/settle split is deliberate — a gateway
   call is network I/O and cannot live inside the transaction that books the
   refund, so the `refunds` row is committed **before** the provider is touched
   and a crash in that gap leaves a row reconciliation can resolve.
   *Still open: the return-refund path still credits a wallet rather than calling
   the gateway, because it runs inside a caller-owned transaction. Fixing it means
   splitting the return approval and the refund into two steps, which changes the
   admin flow and the return state machine.*
   **[closed] A returned item was invisible to the cap, so an ordinary return plus
   an ordinary refund over-refunded the buyer.** `refundInTx` — the path
   `SellerService.RefundReturn` delegates to — locked the intent, derived the
   capped plan, debited the seller, reversed commission, credited the buyer and
   posted a ledger journal. It wrote no `refunds` row, and the cumulative cap reads
   that table. So return a Rp50,000 item of a Rp100,000 order, then refund the
   order: the cap still reads zero and authorises the full Rp100,000. Rp150,000 out
   on a Rp100,000 charge, with no race and no provider retry required — it is what
   a returned item followed by a refund does on any ordinary order. The path now
   writes a `succeeded` row inside the caller's transaction, so a refund that is
   counted is a refund that happened. Mutation-verified as M16.
   **[closed] The double-refund this work introduced.** Unifying the refund paths
   had routed the gateway's *refund notification* through the path for refunds we
   *initiate*, so every `payment.refunded` webhook asked Midtrans to refund the
   same money again. A full refund is merely noisy — Midtrans caps cumulative
   refunds at the charge and rejects it. A partial one is not: charge Rp100,000,
   provider refunds Rp30,000 and notifies us, we request Rp30,000 again,
   30,000 + 30,000 is still under the charge, so it is **accepted** and the buyer
   has been paid Rp60,000. A second, compounding defect: the cumulative cap
   summed `wallet_transactions` credits, but a gateway refund never credits the
   buyer's wallet — the money goes to their card — so the cap read zero after
   every gateway refund and the guard built to stop over-refunding measured
   nothing on the path that actually refunds people. Both are fixed, and the
   mutation suite now genuinely covers the cap's source table and its state set
   (M3/M4/M5) — see the commit history for the full accounting. Those three
   mutations had been reporting success without running a test, so the coverage
   they claimed did not exist until the harness was fixed to reject a failure
   that names no witness.
   **[closed] The replay guard was asked the wrong question.** The webhook path
   looks up an already-recorded refund by the provider's own refund reference, and
   it did so with a literal empty payment-intent id against a `UUID NOT NULL`
   column. That is not "a payment with no refunds" — it is an invalid uuid, so the
   query *errored* rather than matching nothing. Every provider refund notification
   failed before recording anything, and Midtrans retried for 24 hours against an
   error that named nothing about the wiring. The intent is already loaded, so the
   lookup is now scoped to it, and a blank intent id is refused with
   `REFUND_LOOKUP_WITHOUT_INTENT` before it reaches Postgres. Mutation-verified as
   M17/M18.
   The    unit tests could not see it because the stub declared both string arguments
   unnamed and discarded them. The stub now records what it was asked, which is the
   only reason the defect is visible at all.
   **[closed] …and the reference it matches on was never stored.** `CreateRefund`
   bound a literal empty string to `refunds.gateway_ref`, ignoring the field on the
   struct it was handed. The reasoning behind that literal was sound for a refund
   *we* initiate — the provider has not assigned an id yet — and wrong for one the
   provider has already performed and reported, which is exactly the notification
   path. So every provider refund was recorded with `gateway_ref` NULL, while the
   replay lookup filters `AND gateway_ref IS NOT NULL`: no row could ever match.
   The guard was inert for two independent reasons at once, and fixing the scoping
   alone would have left a guard that still never fires while every test stayed
   green. The first test in `internal/repository` now drives `CreateRefund` and
   asserts the bound reference. Mutation-verified as M19.
   **[closed] …and none of it was the path production ran.** `NewPaymentService`
   never assigned the refund lookup, and the refund path quietly built the
   repository adapter itself on the way past when it found the field nil. So every
   test drove a stub, production ran a different implementation, and the production
   path had *no* coverage — which is the structural reason all three defects above
   could sit behind a green suite simultaneously. The constructor now wires the
   lookup, so there is exactly one implementation and whatever the tests exercise is
   what ships; a missing lookup now refuses with `REFUND_LOOKUP_UNWIRED` instead of
   falling back. Mutation-verified as M20.
   **[closed] The cap's state rule existed twice, and one copy was dead.**
   `refundStateCountsTowardCap` in the service layer encoded which refund states
   count toward the cumulative cap — and nothing in production called it. The rule
   actually enforced is a literal list inside `SumRefundedByOrder`, under a comment
   claiming the states "are read from the same Go constants the service writes, so
   the query and the writer cannot disagree". They were not read from anything
   shared. Worse, mutation **M9** attacked the dead copy and was reported as
   coverage of the cap: a mutation attesting to something it never touched. The
   rule now lives in `domain.RefundStatesCountingTowardCap`, where both packages
   can reach it, the service mirror is deleted, and a test compares the query's
   literals against the domain rule — so adding a state fails a test that names the
   query instead of silently producing a cap that reads the wrong rows. M9 now
   mutates the query.
   **[closed] Replay is a database guarantee, not an application convention.**
   Migration 00045 adds a unique index on `(gateway, gateway_ref) WHERE gateway_ref
   IS NOT NULL`, and `ClaimProviderRefund` records a provider refund with a single
   `INSERT … ON CONFLICT DO NOTHING RETURNING` against it. Two things were wrong
   with the old arrangement. The `refunds` table had no unique index on the provider
   reference at all — its own column comment claimed "a refund is never issued
   twice against the same gateway reference" while creating no constraint that said
   so. And the check and the write were separate statements, so two identical
   webhooks arriving together both read "not recorded" and both inserted; Midtrans
   retries for up to 24 hours without serialising them, and that race is winnable.
   A correct lookup does not help — the window is between the SELECT and the INSERT.
   The migration refuses to build over existing duplicate references rather than
   de-duplicating, because deleting a refund row to satisfy an index would delete
   the record of money that left. Mutation-verified as M21/M22/M23.
   **Unverified:** 00045 has never executed against a real database, like 00043 and
   00044. The SQL is checked structurally by `check-migration.mjs` and the
   Go↔SQL coupling is asserted by test — the `ON CONFLICT` predicate must match the
   index's partial predicate or Postgres silently stops deduplicating while still
   returning a row — but no statement here has been run by Postgres.


   **[closed] Every full refund failed, after the provider had paid.** The cap
   counts `submitted`, and the reserve step commits its row as `submitted` before
   the gateway is called. So when settlement re-derived the cap, the row being
   settled was counted against itself: a full Rp100,000 refund read
   `already = 100,000`, computed `remaining = 0`, and refused *itself* with
   `ALREADY_REFUNDED` — rolling back the reversal legs and the ledger journal
   while the buyer's money had already gone back. This was the most common refund
   on the platform and it failed every time, on both the initiate path and the
   provider-notification path. The cap now excludes the row being settled, by id.
   Excluding by *state* instead (inserting as `pending`) would also stop the
   self-count, but it would drop every in-flight refund out of the cap and reopen
   the concurrent-double-refund race; an accepted refund must still count against
   a *different* refund, just not against its own. Mutation-verified as M13/M14/M15.
2. **No wallet top-up.** "Pay with wallet balance" is unreachable in practice;
   the balance is only ever credited by refunds. *Closed by the gift-card /
   voucher-as-product work; until then a refund is the only way in.*
3. **[partly closed] No PPN / tax and no compliant invoice.** `handler/invoice.go`
   emits HTML with no tax line, no seller NPWP/NIB and no invoice number sequence.
   Indonesian sellers cannot expense a marketplace invoice without that. The
   invoice now **foots**: the shipping-insurance fee is added to the order total
   and stored on `Order.InsuranceFee`, but no line was rendered for it, so
   `subtotal − discount + shipping` came to less than the printed total — a
   document that fails an audit on sight. The fee is now printed, and the rendered
   lines are asserted to sum to the printed total
   (`TestInvoiceLinesFootTheTotal`). The previous entry claimed this was fixed; the
   arithmetic was, the presentation was not, and the claim outlived the code.
   *Still open: PPN itself, seller NPWP/NIB, and an invoice number sequence.*
4. **[closed] The ledger is not reconcilable to cash.** `wallets.held_balance`
   was never written, capture wrote no ledger rows, and gateway inflow / bank
   outflow were unrecorded — so `SUM(wallets.balance)` could not be tied back to
   real money and the escrow "hold" phase had no balance-sheet representation.
   Tracing one Rp100,000 order at 2% commission, the old books said Rp2,000 while
   the platform held Rp100,000; the missing Rp98,000 was a real liability to a
   seller and appeared on no balance sheet.
   Now there is a real double-entry ledger: `ledger_journals` / `ledger_entries`
   with a named chart of accounts, escrow and clearing accounts, a whole-rupiah
   invariant, and settlement / payout-batch / refund tables. Every journal sums to
   zero, enforced by a **deferred constraint trigger at COMMIT** rather than by Go
   code, so it holds for the code path somebody writes next year as well as this
   one. `account_balances` and `wallets` are retained as derived caches and
   `LedgerService.Reconcile` asserts they agree with the entries — a cache nobody
   checks against its source is not a cache, it is a second opinion.
   The money paths post journals **inside their existing transactions**, so a
   journal and the wallet movement it describes commit or roll back together:
   capture credits `escrow_held` and debits a clearing account (COD goes to
   `cod_receivable`, because the courier holds that cash, not a gateway); release
   debits escrow for the **gross** and credits the seller's personal account and
   `platform_commission`; a refund reverses whichever of those two shapes actually
   applied. A post-release refund never touches escrow — that is how escrow
   acquires a negative balance that reads as a receivable from nobody — and it
   reverses the commission as well as the seller's leg, so a refunded sale earns
   no commission.
   *The `refunds` table now has a writer and every refund path is durable; the
   remaining gap is the return path, which still credits a wallet (item 1).*
5. **Money is `float64` in Go** against a `NUMERIC(14,2)` schema.
   [closed for the checkout engine] All checkout arithmetic now lives in pure
   functions in `internal/service/money.go` with property tests, rounds to whole
   rupiah at write time (IDR has no sen, and the 2dp value the gateway cannot
   charge was the cause of an AMOUNT_MISMATCH that cancelled paid orders), and
   the multi-seller discount allocation can no longer produce a negative share.
   The remaining work is `decimal.Decimal` end to end.

### P1 — needed to be credible at scale

6. **No carrier integration.** Shipping is `base + kg × per_kg_fee`; there is
   no RajaOngkir/Shipper rate lookup, no volumetric weight, and no zone pricing.
   Remote-area orders are mispriced. [partly closed] The billable weight is now
   accumulated in grams and rounded up once. It previously divided **per line**
   and summed, so six 500g items (3.0kg) billed as **0kg** — free shipping on
   every order from a seller with a sub-kilogram catalogue — and a 2.2kg bundle
   billed as 1kg. Volumetric weight and zone pricing still need the carrier
   integration.
7. **No shipment entity.** Fulfilment is whole-order and all-or-nothing: no
   split shipping, no partial shipment, no return labels, no carrier
   webhooks. This one gap blocks return logistics, bulk label printing,
   pick/pack boards and SLA reporting.
8. **Email is sent synchronously** inside the request or worker that triggers
   it, so checkout waits on 1–3 SMTP round trips. It should be enqueued. The
   asynq infrastructure already exists, so this is a queue and a dispatch.
9. **[partly closed] No payout schedule.** Payouts were seller-initiated and on
   demand, KYC-gated only, and a request debited the wallet with no reservation:
   a seller could request a payout and then have a return come in against the same
   balance, where the reversal found nothing to claw back because the money was
   already marked as sent.
   Now every payout request writes a `seller_reservations` row and posts a
   journal, and the payout lifecycle posts in the transaction that moves the money:
   request debits the seller and credits `seller_pending` (the money is *scheduled*,
   not sent — booking it to `bank_clearing` would report a transfer that has not
   happened); settlement debits `seller_pending` and credits `bank_clearing`; a
   failure is the exact mirror of the request and releases the reservation. The
   repository methods that used to do this in their own transaction are **deleted**,
   not deprecated, because a repository cannot reach the ledger and a caller using
   them would move money with no accounting record and no way to add one.
   *Still open: the batch run itself, and the per-seller override. `payout_batches`
   and `payout_batch_items` exist but nothing writes them, so there is no T+2/T+7
   grouping of withdrawals and no remittance file. A COD hold is also still
   unwritten — the release job knows how to release one, but nothing takes it yet.*
   **[closed] The release job had no handler.** `TaskReleasePayoutReservations` was
   a string constant with a comment describing the fraud control it represented, no
   handler on the mux and no entry in the schedule — so the worker started, reported
   healthy, and never released a single hold. Nothing about that state is visible
   from outside: a hold that is never released is a permanent deduction from a
   seller's balance, and the seller experiences it as the platform keeping their
   money. It now has a handler, an hourly entry on the `default` queue, and a log
   line that says how many holds were released — including zero, because "released
   0" and "this job does not exist" have identical seller-visible symptoms.
   A hold is released only when four things are all true: past the lag, the order
   **completed** (not delivered — the return window opens on delivery), no open
   return and no open dispute. That is a conjunction over four tables and it lives
   in the query, because deciding it in Go means deciding it on a stale read.
   The general fix is a test that derives the expected task list from the `Task*`
   constants in the source rather than from a hand-written list, and requires every
   one of them to be both scheduled and handled. A hand-written list is a list
   nobody remembers to update, and this defect is precisely something nobody did.
   Removing the schedule entry now fails that test by name.
   **[closed] …and nothing was taking the holds it releases.** The only
   `seller_reservations` rows in the system were `payout` rows, written when a
   seller requested a withdrawal. So the release rule was a schedule for holds that
   did not exist, and the exposure it was built for — a seller withdrawing money
   that a return or dispute is about to reverse — was unguarded: the reversal would
   hit an empty wallet, fail `balance >= 0`, and strand the buyer's refund with no
   path forward. Return claims now hold the **item's** value and open disputes hold
   the order, both taken *before* the event is recorded — a stray hold self-releases
   after the lag, whereas an event recorded without a hold has no automatic repair.
   A test pins the ordering, the kind, and the amount, and a fourth assertion
   exists because a mutation that rewrote the wiring check to `if false` otherwise
   survived: the code stayed exactly where it was and became unreachable. A source
   assertion still cannot prove reachability at runtime — both call sites open their
   own transactions — and the test says so rather than implying otherwise.
10. **No COD reconciliation.** COD is implemented with no fee, no aging report
    and no remittance file — so uncollected cash is invisible.
11. **Search relevance is English-stemmed** and recommendations are global
    bestsellers. Both are the largest single levers on marketplace GMV and both
    are currently placeholders. (Note: `sold_count`, which the bestseller ranking
    sorts on, is never written outside the seed, so that ranking currently reads
    a constant.)
12. **WhatsApp / Web Push.** `notifications` is in-app only and worker alerts are
    email-only. In Indonesia, cart recovery over WhatsApp outperforms email by
    roughly 3:1. (Also fixed: the cart-recovery job was a permanent no-op — the
    worker had no mailer wired, so `RecoverAbandonedCarts` returned success and
    sent nothing, every 30 minutes.)
13. **No fraud or abuse detection** — no review-fraud scoring, no fake-order or
    brushing detection, no refund-abuse tracking, and no risk scoring gating
    payouts.

### P2 — correctness and operability

14. **External payment is self-asserted.** A buyer can mark any pending order
    paid with a free-text reference and an amount checked against the total the
    same API just returned. It is one-shot (the order leaves `pending`, so a
    replay fails on status) and the amount is bounds-checked, but there is no
    receipt, no gateway record and no admin verification gate, and it creates no
    `payment_intents` row.
15. **Several endpoints are backend-only with no UI**: session management,
    admin user-detail stats, CSV template download, admin refund/release
    buttons, multi-address checkout, return evidence upload. *(The README
    previously listed five; there are eleven — see the audit in the commit
    history.)*
16. **Bundle pricing is display-only.** "Ambil Paket" adds items at full price.
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
    relies on the operator placing a proxy in front. The edge rate limiter has
    been fixed for that case (`real_ip` now resolves the real client before
    bucketing; without it, every visitor behind a terminator would have shared
    one bucket).



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

