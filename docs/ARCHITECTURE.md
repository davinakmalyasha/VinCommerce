# VinCommerce Architecture

## 1. Overview

Multi-vendor marketplace (Shopee/Tokopedia-class) built as a monorepo:

```
VinCommerce/
├── backend/   Go 1.26 — API server + background worker
├── web/       React 19 + Vite — storefront, seller center, admin console
├── infra/     local tooling (scripts, compose)
└── docs/      this documentation (OpenAPI is served live at /docs)
```

## 2. Backend design (clean architecture)

Layers, dependency direction inward (`handler -> service -> repository`):

| Layer       | Package              | Responsibility                              |
|-------------|----------------------|---------------------------------------------|
| Transport   | `internal/httpapi`   | chi router, handlers, middleware            |
| Application | `internal/service`   | use cases, transactions, orchestration      |
| Domain      | `internal/domain`    | pure types & interfaces (no I/O)            |
| Persistence | `internal/repository`| pgx SQL access                              |
| Infra       | `internal/db`, `internal/config` | pool, migrations, env config     |

### Cross-cutting concerns

- **Database**: PostgreSQL 17 (pgx v5 pool, goose migrations embedded & run at boot).
- **Cache & queues**: Redis 8. asynq workers handle email, payouts, timeouts.
- **Logging**: `log/slog` JSON structured, request IDs propagated.
- **Errors**: typed `domain.Error` with stable codes mapped to HTTP statuses.
- **Idempotency**: `X-Idempotency-Key` header enforced on payment/order writes; gateway intents replay their stored checkout token instead of erroring.
- **Events**: in-process publishers -> Redis pub/sub (SSE). At-least-once semantics for worker jobs via asynq retries.
- **Security**: Argon2id password hashing, JWT access + rotating refresh tokens with reuse detection (family revocation), RBAC, per-route rate limits, feature flags, audit log middleware on privileged routes, request-body caps.
- **Payments**: adapter pattern (`payments.Gateway`) — sandbox simulator + Midtrans Snap with SHA512-signed webhooks; escrow ledger splits commission at release.
- **Observability**: /health/live, /health/ready (admin-authenticated Prometheus `/metrics`).

## 3. Domain model (core entities)

```
User --1:N--> Store (seller profile)
Store --1:N--> Product
Product --1:N--> Variant (SKU, price, stock)
Product --N:M--> Category (tree)
Variant --1:N--> StockLedgerEntry
User --1:N--> Address | Cart | Order
Order --1:N--> OrderItem --> Variant
Order --1:N--> OrderEvent (state machine timeline)
Order --1:1--> PaymentIntent (escrow)
Store --1:1--> Wallet; Wallet --1:N--> WalletTransaction
Order --1:N--> ReturnRequest --1:1--> Dispute
```

Order lifecycle (state machine, every transition emits OrderEvent):

```
pending -> paid(escrow held) -> packed -> shipped -> delivered -> completed
              |-> cancelled (restock)        |-> return_requested -> returned/refunded
```

Payment lifecycle: `initiated -> authorized -> captured (escrow) -> released_to_seller | refunded`

## 4. API surface (`/api/v1`)

Interactive OpenAPI/Swagger UI is served by the API at `/docs`. Groups:

- `auth/*` — register, login, refresh, 2FA, sessions
- `catalog/*` — categories, products, variants, search, reviews
- `cart/*`, `checkout/*` — cart, coupons, shipping, place order
- `orders/*` — buyer orders, tracking, cancellation
- `payments/*` — intents (sandbox / Midtrans Snap), webhooks, wallets, refunds
- `seller/*` — store, KYC, products, orders, returns, coupons, analytics
- `admin/*` — moderation, disputes, users, commission, analytics
- `account/*` — profile, addresses, wishlist

## 5. Frontend design

- Vite + React 19 + TS, Tailwind 4 (hand-built components).
- TanStack Query server state; Zustand client state (session, theme).
- Access token memory-only; httpOnly refresh cookie with single-flight silent refresh.
- Three shells behind one router: storefront `/`, seller center `/seller`, admin `/admin`.
- Real-time order status via SSE (`/api/v1/stream/orders`) with live query invalidation.

## 6. Observability & operations

- Prometheus `/metrics` (admin-authenticated); Go runtime + process collectors.
- Audit log table for privileged actions (admin routes, checkout place, refunds).
- Feature flags via `feature_flags` table gating flash-sales/referrals/AI endpoints.
- Docker images ship HEALTHCHECKs; compose wires service healthchecks.

## 7. Testing strategy

- Unit: token/crypto + commission math + Midtrans adapter (signature table, status matrix, request shape via httptest).
- CI smoke: migrate + seed + readiness probe against real Postgres+Redis services; `go test -race`.
- Web: typechecked build (`tsc -b`) + oxlint in CI.
- E2E: Playwright suite (~21 tests) against the running stack.

## 8. Deployment shape (production sketch)

API + worker as separate containers; Postgres + Redis managed or containerized;
MinIO/S3 for media; nginx/Caddy TLS termination; CI: lint -> test -> build -> image.
