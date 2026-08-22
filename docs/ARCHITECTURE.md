# VinCommerce Architecture

## 1. Overview

Multi-vendor marketplace (Shopee/Tokopedia-class) built as a monorepo:

```
VinCommerce/
├── backend/   Go 1.26 — API server + background worker
├── web/       React 19 + Vite — storefront, seller center, admin console
├── infra/     local tooling (scripts, compose)
└── docs/      this documentation, OpenAPI spec
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
- **Idempotency**: `X-Idempotency-Key` header enforced on payment/order writes.
- **Events**: transactional **outbox** table + dispatcher -> Redis (asynq), giving at-least-once semantics.
- **Security**: Argon2id password hashing, JWT access + rotating refresh tokens, RBAC, per-route rate limits, audit log.
- **Observability**: /health/live, /health/ready, Prometheus metrics, OpenTelemetry tracing (enabled by config).

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

## 4. API surface (`/api/v1`, OpenAPI in docs/)

- `auth/*` — register, login, refresh, 2FA, sessions
- `catalog/*` — categories, products, variants, search, reviews
- `cart/*`, `checkout/*` — cart, coupons, shipping, place order
- `orders/*` — buyer orders, tracking, cancellation
- `payments/*` — intents, webhooks, wallets, refunds
- `seller/*` — store, products, sales, payout
- `admin/*` — moderation, disputes, users, analytics
- `account/*` — profile, addresses, wishlist

## 5. Frontend design

- Vite + React 19 + TS, Tailwind 4, shadcn/ui components.
- TanStack Query server state; Zustand client state (cart, session).
- Three shells behind one router: storefront `/`, seller center `/seller`, admin `/admin`.
- Real-time order status via SSE (`/api/v1/stream/orders`).

## 6. Observability & operations

- Prometheus `/metrics`; dashboards in docs/grafana.
- Audit log table for privileged actions.
- Feature flags via `feature_flags` table + Redis cache (TTL).

## 7. Testing strategy

- Unit: domain + services (mock repositories via interfaces).
- Integration: real Postgres+Redis on CI (compose services), transactional test harness.
- API contract: httptest against router.
- E2E: Playwright against running stack.

## 8. Deployment shape (production sketch)

API + worker as separate containers; Postgres + Redis managed or containerized;
MinIO/S3 for media; nginx/Caddy TLS termination; CI: lint -> test -> build -> image.
