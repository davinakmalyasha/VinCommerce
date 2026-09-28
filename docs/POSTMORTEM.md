# Postmortem: the audit of August 2026

A six-agent independent audit of this repository found **four verified
criticals and roughly thirty high-severity defects**, several of which had been
live in the codebase since its first commit. This document records what was
found, why it was missed, and what now prevents it from recurring.

It exists because "I found and fixed these" is worth much less than "here is how
you would know if they came back".

---

## The four criticals

### 1. Every SSE endpoint returned HTTP 500 in every environment

`/api/v1/stream/orders`, `/api/v1/stream/chat` and `/api/v1/stream/live/{id}`
all failed with `streaming unsupported`.

**Cause.** Go's `http.ResponseWriter` interface declares exactly three methods.
The metrics middleware wrapped it in a struct that *embedded the interface*,
which promotes only `Header`/`Write`/`WriteHeader` — so the wrapper had no
`Flush()`. The stream handlers do `w.(http.Flusher)` and hard-fail when the
assertion fails.

What made it non-obvious: chi's `chain()` applies the **last-registered**
middleware **innermost**, so the metrics wrapper — registered last — was the one
wrapping the handler. The access-log wrapper directly beneath it *did* forward
`Flush`, and did so correctly. Both wrappers were correct in isolation.

**Why it survived.** Nothing tested streaming. The E2E suite covered 39 tests
and none touched `/stream/*`. The feature had been hand-verified once, before
the metrics middleware was appended.

**Now prevented by:** every response-writer wrapper in the chain forwards
`Flush`/`Hijack`/`Push`/`Unwrap`; metrics is registered *first* so it is the
outermost layer and the innermost is always the access-log wrapper; and
`web/e2e/sse.spec.ts` asserts `200` with `Content-Type: text/event-stream`.

### 2. A published JWT signing key passed the production guard

`infra/compose/compose.full.yaml` shipped
`JWT_SECRET: ${JWT_SECRET:-local-dev-secret-please-change-me-32chars}`. The
validator in `config.go` rejected secrets shorter than 32 characters **or** equal
to one specific sentinel string.

The default is 41 characters and is not that sentinel, so it passed. An operator
who set `APP_ENV=production` and forgot the secret booted a production API
signed with a key published in this repository — from which anyone could mint an
`admin` token and reach every `/admin/*` route.

Compounding it, `APP_ENV` was compared with `== "production"`, so `APP_ENV=prod`
silently disabled *every* production guard, including this one.

**Now prevented by:** the compose file has **no default** (`${JWT_SECRET:?...}`,
so compose refuses to start); `config.ParseEnvironment` is an enum and an
unrecognised value is a startup error; and `config.knownPublicSecrets` is a
deny-list checked with a length check *and* a distinct-rune-count check.
`config_test.go` asserts the exact committed string is rejected.

### 3. A dispute resolution drained the platform's commission pool

`DisputeSplitCredit` debited the platform wallet and credited the buyer — and
recorded **no state whatsoever**. It only *read* the payment intent's status.
`DisputeRepository.Resolve` was an unguarded `UPDATE` with `RowsAffected`
discarded. The admin endpoint called both, in sequence.

Resolving one dispute twice therefore paid the buyer twice. The only backstop
was `wallets.balance >= 0`, so **one order could drain commission collected from
every other seller on the platform.** The amount was `intent.Amount / 2` — on a
Rp 500.000 order with a 2% fee, a 25× amplification of a single dispute.

**Now prevented by:** the dispute is claimed with a guarded `UPDATE ... WHERE
status IN ('open','under_review')` and `RowsAffected` checked, **inside the same
transaction as the wallet movements**; the payout is capped at the commission
the platform actually earned on that order; and a new `disputed_split` intent
state makes a replay conflict. Backed by
`uq_disputes_one_open_per_order` in migration 00040.

### 4. The service worker served one user's private data to the next

`web/public/sw.js` cached **every** `GET /api/*` response into one
origin-wide Cache Storage bucket and served from it on network failure.

Cache Storage keys are **URL-only**. `Authorization` is not part of the key, and
the API sends no `Vary: Authorization`. So on a shared device, going offline —
or via the stale-while-revalidate branch — the next user could read the previous
user's `/orders`, `/wallet`, `/account/addresses` and `/notifications`. Guest
carts were the same: the guest identity lives in the `X-Session-Key` *header*,
which the cache ignores entirely.

**Now prevented by:** the service worker never reads from or writes to the cache
for `/api/` or `/uploads/`; the logout and account-switch path posts a
`CLEAR_USER_CACHE` message that purges every bucket; and only immutable
content-hashed assets are cached.

---

## The pattern behind all of them

None of the four was a logic error. Every one was a **composition** error: two
components each behaving correctly, interacting incorrectly.

| Defect | Components that each looked right |
|---|---|
| SSE 500 | metrics wrapper + access-log wrapper + chi's innermost-wins ordering |
| Published JWT key | compose default + a validator with an exact-match sentinel |
| Commission drain | an unguarded `UPDATE` + a money function that recorded nothing |
| SW data leak | a cache-first strategy + the assumption that the cache key includes auth |

**The lesson applied throughout:** the fix for each is a *structural* guard —
an enum, a CHECK constraint, a partial unique index, a test that asserts a
status code — rather than a comment. The four are covered by
`config_test.go`, `clientip_test.go`, `logging_test.go`, `metrics_test.go`,
`sse.spec.ts` and a new `disputed_split` state, and none of them can regress
silently.

## Money invariants moved into the schema

Eight invariants that previously lived only in Go application code are now
enforced by migration `00040`. Each is a case where any new code path that
forgot a guard could mint or lose money:

- `payment_intents` split legs must be non-negative
- `platform_fees` pct in range, fixed non-negative, **at most one active row**
- `orders.total_amount >= 0` and `discount_amount <= subtotal`
- **one** live dispute per order
- **one** live return claim per order item
- **one** referral bonus per user
- **one** default address per user
- **one** wallet transaction per business event `(wallet, ref_id, kind, reason)`

The migration repairs pre-existing violations before adding each constraint and
`RAISE WARNING`s rather than failing when a violation is not safely repairable
(duplicate ledger rows are financial records and are never deleted to make a DDL
statement succeed).

## What a reader should distrust

Stated plainly, because "all tests pass" is the claim most likely to mislead:

- **`go test` and `npm test` do not reach the money paths.** There is no test
  for `PlaceOrder`, `RefundOrder`, `CancelOrder`, `ResolveDispute` or any
  migration against a real database. They are covered by guards and
  constraints, not by tests. This is the largest remaining gap in the test
  posture and is tracked in [M1-*].
- **The nginx config is not verified by `nginx -t` here** — no docker, WSL or
  nginx binary was available. `scripts/check-nginx.mjs` is a structural check
  and `scripts/check-nginx.test.mjs` proves it detects 13 specific defects, but
  the CI `docker` job is the real gate.
- **The earlier version of that checker had four false passes**, one of which
  would have let a deleted security header go unnoticed. It was found by
  reviewing the checker itself, which is why the mutation suite exists.
- **`README.md`'s original feature list overclaimed** — roughly ten features
  were backend-only with no UI and three were cosmetic. The list is now tagged
  `✅ shipped` / `⚠️ partial` / `❌ known gap` against the same audit, and
  [Known gaps](../README.md#known-gaps) is generated from the same findings.
