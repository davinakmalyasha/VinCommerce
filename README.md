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

## Corrections to earlier claims in this file

* **`2814c52` claimed "39/39 mutations caught". It was 38 of 39.** M35 -- the
  mutation for the `NOT EXISTS` that stops a payout sitting in two remittance files
  -- never ran. Its anchor was one tab out of place, and the harness printed `SKIP`
  and then `continue`d without counting it, so the summary read `38 caught, 0
  missed, 39 total` and exited 0. The arithmetic invites the reader to conclude
  39/39. Fixed in `3b9ce88`; M35's anchor and two false passes in the test that was
  supposed to catch it are fixed here. The claim was uncovered while the report said
  otherwise.

* **Mutations outside a hardcoded file list were never tested at all.** The runner
  chose the package with `if m.file == repoFile`, so any other file fell through to
  `./internal/service`; a mutation that rewrote `shipment_repo.go` ran only tests
  that never read it and was reported `SURVIVED`. Fixed in `d5d3e13`, which runs
  every package -- deriving the package from the path was tried first and is wrong,
  because this project's tests are largely source-text assertions that live in a
  different package from the file they inspect.

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
   the balance is only ever credited by refunds. **The previous version of this item
   claimed it was "closed by the gift-card / voucher-as-product work". That was
   false and is retracted.** Vouchers are coupons — `ClaimVoucher` attaches a
   `domain.Coupon` to an account, and they discount an order rather than funding a
   balance — and there is no top-up path, no payment-intent for one, and no handler
   for one (`grep -i 'topup|top-up|AddFunds|FundWallet'` over the backend: zero
   hits).
 Every credit to a `wallets` row still comes from a refund, an escrow
   release, a dispute split or a COD collection. "Pay with wallet balance" therefore
   has exactly one way to ever have a balance to spend, and it is being refunded.
   closing this needs a real top-up: a payment intent whose gateway leg funds the
   wallet rather than an order, an idempotent ledger posting for it, and a seller
   or buyer surface to trigger it.
   Precisely, since a seller's wallet is not quite the same case: the only credits
   to any `wallets` row are `escrow_release` (a seller's earnings),
   `commission` (the platform's own), `refund` (a buyer, via the refund path or a
   dispute split), `adjustment` (a failed withdrawal being returned), and the new
   `seller_held` release. A BUYER's wallet is credited only by a refund. So
   "pay with wallet balance" has one way to ever hold a balance: be refunded, or
   lose a dispute.
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
   **[closed] …and it could not post a single line that touches a seller.** Two
   defects, both latent, both hidden by the fact that no database has ever run
   these migrations. First, `ledger_accounts.code`, `ledger_entries.account_code`
   and `account_balances.account_code` are `VARCHAR(48)`, while a personal account
   code is `"seller_available:" + uuid` = **53 characters** — so every posting that
   debits or credits a personal account failed on width. Second,
   `EnsurePersonalAccount` had **zero callers**, so the row it would have created
   did not exist and the foreign key on `ledger_entries.account_code` rejected the
   insert instead. And `postLedger` logs a failed journal and returns nothing, so in
   both cases the wallet movement committed with no journal behind it: escrow release
   credited the seller, the commission was booked, and the ledger recorded neither.
   The deferred zero-sum trigger was never reached, so it never complained. The
   consequence is that the ledger was not double-entry for any personal-account
   posting, and `reconcileWallets` was comparing `wallets.balance` against an
   account that could never exist — reporting clean because both sides were zero.
   Fixed by migration 00046: widen to `VARCHAR(64)`, and add a `purpose` column so
   one user can hold both a `seller_available:` and a `seller_held:` account (the
   old `UNIQUE (user_id) WHERE NOT is_system` permitted exactly one).
`ledger_account_codes_test.go` now composes every account code the service can
   produce and asserts each fits the width the schema *ends up* with after all 47
   migrations, read from the files rather than hardcoded — so re-narrowing fails a
   test that names the code, verified by reverting the widening.
   **[closed] …and then, even with the width fixed, the accounts still did not
   exist.** `ledger_entries.account_code` and `account_balances.account_code` both
   have a **foreign key** to `ledger_accounts(code)`, and `EnsurePersonalAccount` had
   zero callers — so a posting to a seller who had no account row was rejected by
   the FK, and `postLedger` swallowed that. Five call sites were affected and each
   had to remember to ensure the account; none did:

   | posting | touches `PersonalAccount` | ensured the account |
   |---|---|---|
   | `ReleaseEscrow` | credits the seller's net | no |
   | `RequestPayout` | debits the seller | no |
   | `postPayoutSettled` | debits the seller | no |
   | `failPayout` | credits the seller | no |
   | post-release refund | debits the seller | no |

   So **the previous commit closed half of this**, and its "[closed]" was not true
   when written. The fix belongs where the failure mode was: `postLedger` and
   `postLedgerStrict` now scan their own entries for the personal namespaces and
   ensure each distinct account before posting, so no call site *can* forget. That is
   the arrangement that survives the next function somebody adds — as opposed to
   adding `EnsurePersonalAccount` to five call sites, which is how it was missed
   once already.
   Getting the classification wrong fails in both directions, and both directions
   are tested by value: missing a personal account reintroduces this defect, while
   treating a *system* account as personal violates `ledger_accounts_user_side`
   (a system account must have no `user_id`) and would fail on the very first
   capture. Also closes a long-standing false claim — `ledger_repo.go` asserted the
   two packages' copies of the namespace prefixes "are asserted against each other
   by a repository test", and no such test existed; there are two namespaces now and
   a third could be added without either side noticing. Mutation-verified as M30
   (the ensure removed from the posting path — caught on the *swallowing* helper,
   which is `ReleaseEscrow` and every payout transition) and M31 (a failed ensure
   swallowed on the strict path).
   **[closed] …and a seller hold moved no money at all.** `HoldSellerFunds` wrote a
   `seller_reservations` row and stopped. `RequestPayout` debits `wallets.balance`
   and reads nothing else, so the "fraud control" left the money fully withdrawable
   while looking like it had held it — and `postLedger` logs a failed journal and
   returns nothing, so it could not even complain. `wallets.held_balance` had no
   writer anywhere in Go, `AccSellerHeld` was never posted to, and
   `reconcileWallets` was comparing those two against each other at zero and
   reporting clean. Three of the design's four moving parts were never built.
   A hold is now one transaction of four writes — ensure both personal accounts,
   journal `debit seller_available / credit seller_held`, move
   `balance -= amount, held_balance += amount`, insert the reservation — and the
   release is the exact mirror. Because the hold *transfers* the value,
   `RequestPayout` needs no new predicate: the existing `balance >= amount` check
   becomes correct the moment holds are real, which is why there is no
   `balance - held_balance` arithmetic anywhere. The journal posts **strictly** on
   this path, unlike `postLedger`, because it is one event rather than a movement
   that has already committed. Migration 00047 makes one live hold per order per
   kind a database guarantee, so two concurrent claims cannot hold the money twice.
   Verified by mutating the hold to stop debiting the spendable balance, and the
   release to move money in the hold direction; both fail by name.
   **[new] Held money is reported, because a hold nobody can see is enforced and
   invisible at the same time.** `GET /admin/ledger/held` returns the total and the
   rows behind it. Escrow is money held for *orders*; a hold is money a *seller* has
   earned and may not yet spend, so the two move independently and an operator
   watching only `escrow_outstanding` sees a number that does not move when a
   return hold is taken. `?live=false` includes released holds. The total excludes
   them — a released hold is money the seller already has, and counting it would
   overstate the platform's restriction on itself.
   Mutations added for the hold path: **M24** (hold stops debiting the spendable
   balance), **M25** (release moves in the hold direction), **M26** (release moves
   without claiming first), **M27** (a failed journal stops stopping the money).
   M27 is the interesting one: it SURVIVED at first, because the test asserted
   `postLedgerStrict` was *called* and the mutation left the name in place while
   turning the body back into the swallowing version. It is now caught by driving
   the propagation directly — an unbalanced journal, which `Post` rejects before it
   touches the repository, so no database is needed. Suite is 26 mutations, 26
   caught, 0 missed, 0 skipped.
   **[closed] COD cash was never booked.** `onPaid` posts a capture journal;
   `captureIntentOnly` did not — and `onPaid` is reachable only from
   `HandleWebhook`, so only gateway payments were ever recorded. COD capture happens
   through a different door, at delivery. So a COD order's buyer paid cash to a
   courier, `escrow_held` was never debited, and `cod_receivable` — seeded in 00043
   precisely because that cash is the courier's and not a gateway's — sat at zero
   forever. The books showed Rp0 of captured COD against Rp100,000 of goods
   delivered, and the reconciliation had nothing to disagree with because the journal
   was never written.
   `TestCODCaptureGoesToTheCourierNotTheGateway` existed, passed, and its comment
   said it pinned this. It pinned the *helper*: `captureEntries` did route COD
   correctly, and nothing ever called it with a COD method. A test of the classifier,
   not of the wiring — the fifth instance of that pattern in this workstream, and the
   reason the fix asserts the call. The journal is now posted strictly, inside the
   capture transaction, under the same `capture:<intent id>` idempotency key as the
   gateway path. Mutation-verified as M28 (the whole block removed, which is what
   makes it compile — `if false` leaves the `err` binding dangling).
   **[partly closed] Per-seller payout lag.** The platform default (`PAYOUT_LAG_DAYS`)
   applies to every seller; a marketplace needs both ends, so migration 00048 adds
   `stores.payout_lag_days INT NULL`, where **NULL means "use the platform default"** —
   not merely for consistency with `free_shipping_threshold`, but because a column
   default would make "seven" permanent and silently skip every seller when the
   default changes. `ReleasableReservations` binds `COALESCE(st.payout_lag_days, $1)`
   over a **LEFT** join on `stores.owner_id`: an inner join would exclude every seller
   with no store row, which fails *safe* but *silently* — nothing is ever released
   and the hold queue just grows. Mutable by **admin only**, deliberately: a seller
   choosing their own payout term is a conflict of interest, since a one-day lag makes
   their money withdrawable while a return is open. That is the whole difference
   between this and `SetFreeShippingThreshold`, which is seller self-service — a
   shipping threshold is a commercial choice, a payout term is a risk parameter the
   platform owns. `PUT /admin/stores/{id}/payout-lag`, the first admin surface in the
   codebase that writes a `stores` setting at all. The bounds are duplicated across
   the service constants, the migration's CHECK and `config.Validate` (none can import
   each other), and a test compares them. Mutation-verified as M32/M33/M34 — and M34
   survived first because the test checked *ordering* while the mutation only changed
   the *argument*, which is the sharpest reminder yet that "is it in the right order"
   and "is it the right value" are different questions.
   **[closed] A COD seller could withdraw money a refused delivery would reverse.**
   `ReleaseEscrow` is the moment COD money first becomes withdrawable, so it is the
   only place a COD hold can bite — and nothing took one. The obvious spot,
   `CaptureCOD`, would actively fail: at capture the money is still in `escrow_held`
   and the seller's balance is zero, so `WalletHeldTxOn` would refuse with
   `INSUFFICIENT_BALANCE` on every COD order. The hold is taken on the seller's
   **net**, inside the release transaction, because a refused delivery reverses the
   seller's net and not the platform's commission — holding the gross would freeze
   money the platform is entitled to keep and make a bad COD look like a worse one.
   Verified as M29.
   **A note on a recurring failure mode, now a shared check.** Three separate
   mutations in this workstream survived by being *disabled* rather than removed —
   `if false` on the release handler, and `if false && …` on the COD hold — leaving
   the guard's text exactly where a substring assertion expected to find it. A guard
   that is present, in the right place, with the right text and no behaviour is
   indistinguishable from a working one to a reviewer and to a test alike.
   `assertNotDisabled` now rejects a short-circuited condition wherever one is
   asserted, so it is a helper rather than an incidental check at each site.
   **[closed] `PAYOUT_LAG_DAYS` was configured by nobody.** `SetPayoutLag` existed
   with a full range check and had **no caller**, so the lag was always the Go
   constant of 7 regardless of how the deployment was configured — an operator could
   set the variable and watch nothing happen, which is the worst shape a
   configuration option can have. Now read by `config.Validate` (which range-checks
   it, so a bad value stops the boot rather than quietly falling back) and applied
   in `app.Build`, with an error returned rather than logged-and-ignored. The bounds
   are duplicated between the two packages because `config` cannot import `service`,
   so a test compares the copies by value; duplication with nothing comparing it is
   how a limit stops being enforced in one place.





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
   **[closed] The batch run, and the remittance file.** `payout_batches` and
   `payout_batch_items` existed since 00043 with zero Go references. They now have
   a daily `TaskBuildPayoutBatches` run that groups the withdrawals past their lag
   into a **draft**, `POST /admin/payout-batches/{id}/approve` records an operator's
   approval, and `GET /admin/payout-batches/{id}/remittance.csv` produces the file
   the bank consumes.
   **A batch groups and never pays.** It must not write `payouts.status`, because
   `PayoutForUpdate`, `MarkPayoutSentTx` and `MarkPayoutFailedTx` all require
   `status = 'pending'`: a batch that claimed its rows into `processing` would make
   them un-settleable *and* un-failable at once, stranding the `seller_pending`
   balance and the `payout` reservation with no repair in the codebase. Each
   withdrawal is still settled individually through the existing
   `POST /admin/payouts/{id}/process`, which is where the transfer reference and the
   failure handling live. The remittance file **refuses an unapproved batch**: the
   file *is* the instruction to move money, and an unattended daily job must not be
   able to produce one.
   Two DDL gaps in 00043 would have made the batch dangerous, so 00049 closes them.
   `payout_batch_items` had `PRIMARY KEY (batch_id, payout_id)`, which permits the
   same payout in two batches — a payout paid twice from two remittance files; the
   claim is now enforced by `UngroupedPayouts`' `NOT EXISTS`, which excludes only
   payouts in a *non-cancelled* batch so a cancelled batch releases its withdrawals.
   And there was no receipt, so a retried run would build a second batch for the
   same cutoff; `batch_ref` is that receipt, with the same partial unique index
   `settlement_imports` got in ff15503 and 00044:27-34 describes.
   *Still open, and it is the important half: nothing actually MOVES the money.* The
   remittance file is executed by a human at a bank, and `ProcessPayout` is called
   per payout afterwards. There is no payment-rail integration, so settlement still
   depends on someone uploading the CSV and typing the transfer references. That is
   a deliberate boundary, not an oversight — but it does mean `approved` batches sit
   until a person acts, and no job notices or reports that.
   *A COD hold is also still unwritten — the release job knows how to release one,
   but nothing takes it yet.*
   **[closed] An order could only ever be one parcel.** `orders` has carried a single
   `tracking_number` and a single `carrier` since 00004:3-4, so one order was one
   parcel, and two real situations could not be recorded at all:

   * **Split shipping.** One seller order leaving in two parcels -- different
     warehouses, a heavy item going separately. The second parcel had nowhere to
     record its tracking number, so it overwrote the first.
   * **Partial shipment of a line.** `order_items` has `quantity` and no shipped
     counter, so shipping 2 of 3 units was not expressible. The only honest option
     was to mark the whole line shipped, which tells the buyer three are coming when
     two are, and tells the seller to ship three.

   00050 adds `shipments` (one row per parcel) and `shipment_items` (what is in it,
   and how many), plus `order_items.shipped_quantity` and a `partially_shipped`
   state on both the order and its lines. The state machine in
   `domain/order.go` now has `packed -> partially_shipped -> shipped`; without it
   `CanTransition` (enforced at order_service.go:918) would have rejected every
   split shipment, and 00050's tables would have sat there holding nothing -- the
   same shape as 00043's payout batches and `TaskReleasePayoutReservations`.

   **Over-shipping is prevented by two things that are not interchangeable.**
   `order_items.shipped_quantity <= quantity` is a CHECK: the invariant, in the
   database. But it only fires when the ITEM row is written, so two concurrent
   parcels would each pass it and jointly overshoot. The mechanism is the
   conditional update `WHERE shipped_quantity + $2 <= quantity`, whose zero
   rows-affected *is* the refusal, decided atomically against the row lock. The sum
   of `shipment_items.quantity` across live shipments is the property that actually
   matters and is maintained by that update and nothing else -- a CHECK cannot see
   other rows and a UNIQUE cannot sum. M44 pins the predicate.

   Existing tracking data is backfilled into parcel 1 of each shipped-or-later
   order, with the lines attached at full quantity, because the old single-tracking
   model meant exactly that. Orders shipped with **no** tracking number are included
   with carrier `manual`, because COD and local deliveries routinely have no AWB and
   `00004` made tracking optional -- excluding them would invent parcels that were
   never dispatched.

   A parcel's weight is derived from its contents and never accepted from the
   seller: a typed weight is the weight the carrier charges for.

   *Still open, F2 onwards: no carrier integration exists yet, so labels and
   tracking numbers are typed in by hand and nothing polls a carrier. `manual` is a
   first-class carrier precisely so those orders stay recordable in the meantime.*

   **[closed] A split order could never finish shipping.** 00050 made parcels
   expressible but left FulfillOrder(to=shipped) accepting packed and refusing
   everything else. So the moment the first parcel of a two-parcel order was
   recorded the order read partially_shipped — and the SECOND parcel could never be
   recorded at all. The seller was left with half an order and no way to finish it.

   That is not a subtle interaction. It is the schema and the state machine
   disagreeing about what a parcel means, and it was only visible once both existed.

   ShipmentService now records the parcel, its contents and the order status in ONE
   commit. That is why TransitionOrderTx was added: TransitionOrder opens its own
   transaction, so deriving the status separately leaves a window where the boxes
   exist and the order still reads packed. Same reasoning that deleted
   FailPayout/CompletePayout — a repository method that cannot join a transaction
   cannot compose.

   **The order status is DERIVED from what is in the boxes, never requested.** No
   shipment method accepts a status. DeriveShippingStatus counts
   shipped_quantity against quantity — excluding cancelled and 
eturned lines,
   which is the whole reason it is a query and not arithmetic on the parcel rows. An
   order of three units with one line cancelled has two to ship; counting the
   cancelled one leaves it permanently one unit short of shipped, and every retry
   re-derives the same wrong answer. M49 pins that predicate.

   A derivation the state machine refuses is reported as SHIPMENT_STATUS_NOT_REACHABLE
   and the parcel is still recorded. Silently ignoring it would leave the boxes and
   the status disagreeing; blaming the seller would be wrong, because they did not
   create the state.

   The buyer is notified when the LAST parcel is dispatched, not the first —
   announcing parcel one of two tells a buyer their order is on its way while most of
   it is in a warehouse, which produces exactly the ticket that one email instead of
   two would have avoided. ShipmentService sends no mail at all: emailBuyer
   already resolves the buyer, and a first draft declared a Mailer interface whose
   argument order was the REVERSE of mail.Client.Send (subject before template),
   which would have compiled and swapped every subject line in production.

   *Still open: there is no HTTP surface for parcels yet, and
   emailBuyer discards every error it can return — so a buyer whose shipped mail
   fails to send is not told, and neither is the platform.*

   **[closed] The platform told the buyer to send the goods back and gave them no
   way to.** `SellerDecideReturn` emails "Silakan kirim barang kembali" on approval
   — please send the goods back — and until 00051 there was no return label, no
   return parcel, no RMA, and nothing at all that wrote `shipments.kind = 'return'`,
   a value 00050 had carried unused since the day it was added. A buyer complying with
   that email had to find a courier themselves and guess the seller's address off the
   order page.

   Three things turned out to be missing, not one:

   1. **A destination.** `stores` had *no address column at all* — `00006_marketplace.sql`
      created it with name, slug, description, logo, banner, status and counters. So a
      return label had nowhere correct to be sent. Both available fallbacks are wrong:
      reusing `orders.shipping_address` is the BUYER's address, which posts the returned
      goods back to the buyer at the seller's expense as a second parcel of the same
      items; inventing one from the store's city is an address nobody chose. So the
      seller states it (`PUT /seller/return-address`), and an unset address makes the
      label REFUSE rather than guess.

   2. **The parcel.** `return_requests` gains `return_shipment_id` and
      `return_label_url`. The label URL is a pointer, not state — it is signed and
      expiring in every real carrier; the durable record is the parcel's `label_format`
      and `label_created_at`. The FK is `ON DELETE SET NULL`, because `shipments` is
      `CASCADE` from `orders` and a plain FK would make deleting an order fail on a
      constraint in a table nobody was thinking about.

   3. **The counter that must NOT move.** `shipped_quantity` is the over-shipment
      counter, and `DeriveShippingStatus` compares it against `quantity` to decide
      `partially_shipped` vs `shipped`. Incrementing it for goods travelling the OTHER
      direction would make a delivered order re-assert `shipped` and count one unit
      twice — once out, once back. So the return path writes a bare `shipment_items`
      row and does not go near `AddShipmentItems`, whose entire job is the reservation.
      That is the same disagreement 00050 had to fix for the order status, one level
      down and through the return door.

   **No new value on `return_requests.status`.** The journey lives on the parcel; the
   return row says whether the return was approved and whether the refund happened. An
   `in_transit` value there would duplicate the journey in two tables that can then
   disagree. A return reaches `returned` only when its parcel is actually `delivered`
   (M63, M66) — a seller clicking "it arrived" is not evidence, and a return marked
   returned with the box still in a depot is a refund paid for goods nobody has.

   **Ownership is split, and that is the fraud control.** The BUYER creates the parcel
   — the party that physically hands it over is the party that records it. The SELLER
   issues the label, because the seller pays for the carriage and owns the
   destination. A seller who could create and dispatch a return parcel could mark goods
   as returned without them ever leaving the buyer's house, and the refund that follows
   is real money. M65 pins the ownership check.

   **Nothing on this path moves money.** A return label is a label: no escrow release,
   no journal, no wallet debit. The refund is `RefundReturn`, an explicit seller action,
   and the seller hold is released by the existing reservation job. Asserted over the
   whole file, the way `payout_batch_test.go` asserts that no batch path writes
   `payouts.status`.

   *Still open: no real carrier adapter, and `return_address` has no validation beyond
   "is it empty" — a seller can set a street that does not exist, and the parcel will
   simply fail to arrive.*

   **[closed] There was no carrier integration, and `manual` is a real one.**
   `internal/carrier` is the boundary between a parcel and whatever physically moves
   it, shaped like the payment `Gateway` interface that already existed: one required
   interface, and optional capability interfaces for the paths that need them
   (`TrackingCarrier`, exactly as `RefundStatusProvider` is separate from `Gateway`).

   **`manual` is implemented, not stubbed.** It mints a tracking number, issues a
   printable label and answers tracking queries -- it just does none of that by
   talking to anybody. That is the correct adapter for COD and local delivery, which
   routinely have no AWB, and it means the parcel path is fully exercised today with no
   credentials at all. A stub would have been the obvious first step and would have
   been useless; refusing to record a parcel until a real carrier exists would have
   left every COD order unrepresentable, which is the exact defect `00050` removed.

   Two decisions worth naming:

   - Tracking numbers are `MNL`-prefixed and derived from the parcel id, not random.
     A random number is undiagnosable when a buyer reads it out over the phone, and
     one that looks like a courier's makes someone wait on a courier for a parcel
     that was never handed to one.
   - The manual carrier reports `pending`, never `in_transit` or `delivered`. It
     knows nothing, and a reconciliation path that stored a guessed state would be
     storing a lie as fact. A parcel moved by hand is delivered by someone TELLING
     the platform.

   **An unconfigured carrier is NAMED, never answered with the manual one.** The
   registry is a map, not a switch with a default branch, precisely so that a seller
   asking for `jnE` is told it is not configured rather than handed a self-minted
   number and left waiting on a courier (M51).

   **Buying a label and dispatching a parcel are separate endpoints.** A label costs
   money and is often irreversible at the carrier; handing the box over is free and
   happens later. Collapsing them means a seller who buys a label and then cannot get
   a colleague to the depot has spent money he cannot get back. The label is
   idempotent on the PARCEL, so a retry after a timeout returns the label already
   paid for (M54, M58).

   A label is refused before any money is spent if the parcel has no destination, no
   weight, or no format -- checked in one place at the boundary rather than in each
   adapter, because three carriers re-implementing "a label needs a destination" is
   three chances to forget one and the failure at the far end is a paid label
   addressed to nowhere (M56).

   **Still open: there is no real carrier adapter.** `manual` covers COD and local
   delivery, and nothing polls a carrier, so a parcel moved by a courier is marked
   delivered by hand. F4 (return labels) and F5 (carrier webhooks) are not started.

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

