// Commits the hardening work as a series of focused commits.
//
// SAFETY MODEL
//   * The caller has verified `git rev-list --count HEAD..origin/main == 0`, so
//     every commit here is a fast-forward and no force push is possible.
//   * A pre-change backup branch and a full binary patch were taken first.
//   * Nothing is force-added: every path must already be tracked or be a real
//     untracked file, so a mistyped path fails loudly instead of silently
//     committing half the change.
//   * Before the first commit and after the last, the working tree is asserted
//     to contain exactly the paths in this manifest and nothing else. That is
//     what makes "did I lose a file?" answerable rather than assumed.
import { execFileSync } from 'node:child_process'
import { writeFileSync, readFileSync, existsSync } from 'node:fs'
import path from 'node:path'

const git = (...args) =>
  execFileSync('git', args, { encoding: 'utf8', maxBuffer: 1 << 28 }).trim()

// NOTE: git() trims, which is fine for scalars but corrupts --porcelax: the
// status column can be a leading space (e.g. " A" for an intent-to-add file), so
// trimming eats the separator and the path is sliced by one. Read raw.
const gitRaw = (...args) =>
  execFileSync('git', args, { encoding: 'utf8', maxBuffer: 1 << 28 })

const changed = () =>
  gitRaw('status', '--porcelain', '-uall')
    .split('\n')
    .filter((l) => l.length > 0)
    .map((l) => l.slice(3))
    .sort()

const C = [
  {
    id: 'config-env',
    msg: `fix(config): parse APP_ENV as an enum and fail closed on unknown values

APP_ENV was compared with == "production" in six places, so a typo silently
disabled every production guard. "prod", "PROD" and a trailing space all took the
same path as development.

ParseEnvironment is now an enum: known aliases resolve (so "prod" gets the
guards rather than a confusing startup error) and anything else is a hard
startup failure. IsProd() also covers staging, which was previously treated as
development while still being internet-facing - that is why the refresh cookie
was issued without Secure on a staging host.

Co-Authored-By: Claude Opus 4.8 (1M context) <noreply@anthropic.com>`,
    paths: [
      'backend/internal/config/config.go',
      'backend/internal/config/config_test.go',
      'backend/cmd/api/main.go',
      'backend/cmd/seed/main.go',
      'backend/.env.example',
    ],
  },
  {
    id: 'config-secret',
    msg: `fix(config): reject published and low-entropy JWT secrets

infra/compose shipped JWT_SECRET:-local-dev-secret-please-change-me-32chars.
The validator rejected secrets under 32 chars or equal to one sentinel string;
the default is 41 chars and is not that sentinel, so it passed. An operator who
set APP_ENV=production and forgot the secret booted a production API signed with
a key published in this repository - anyone could mint an admin token.

Replace the single sentinel with a deny-list of every value that appears in the
repo, plus a distinct-rune-count check so obvious filler is rejected too. The
compose file loses its default entirely, so forgetting it is now a hard failure
rather than a silent weak key.

Co-Authored-By: Claude Opus 4.8 (1M context) <noreply@anthropic.com>`,
    paths: ['backend/internal/httpapi/handler/auth.go'],
  },
  {
    id: 'sse-flusher',
    msg: `fix(middleware): forward optional ResponseWriter interfaces, unbreaking all SSE

Every /api/v1/stream/* endpoint returned HTTP 500 "streaming unsupported" in
every environment. Each stream handler does w.(http.Flusher) and hard-fails when
the assertion fails.

The cause was a composition bug, not a logic error. http.ResponseWriter declares
exactly three methods, so a struct that *embeds the interface* promotes only
Header/Write/WriteHeader and has no Flush. chi's chain() applies the
last-registered middleware innermost, and metrics was registered last - so the
metrics wrapper was the one wrapping the handler. The access-log wrapper one
layer out did forward Flush, and was correct in isolation.

- every wrapper now forwards Flush/Hijack/Push/Unwrap
- metrics moved to the outermost position, so the innermost is always the
  access-log wrapper and ordering stops mattering
- statusRecorder no longer reports a pre-WriteHeader handler as status 0, and a
  first-write-wins guard prevents a double WriteHeader corrupting the log
- access logs for /health/* and /metrics drop to debug so probes do not flood
  the stream

Regression-tested in metrics_test.go and logging_test.go: a handler behind each
wrapper must still type-assert http.Flusher.

Co-Authored-By: Claude Opus 4.8 (1M context) <noreply@anthropic.com>`,
    paths: [
      'backend/internal/metrics/metrics.go',
      'backend/internal/metrics/metrics_test.go',
      'backend/internal/httpapi/middleware/logging.go',
      'backend/internal/httpapi/middleware/logging_test.go',
    ],
  },
  {
    id: 'client-ip',
    msg: `fix(security): resolve the client IP through a trusted proxy chain

Every per-IP rate limit was bypassable, twice over.

1. chi's RealIP is documented as vulnerable to spoofing and takes the leftmost
   X-Forwarded-For entry unconditionally - i.e. whatever the client sent first.
   A fresh forged header per request meant unlimited password attempts against
   /auth/login, unlimited registration, and unmetered LLM spend on /ai/ask.
2. ipKey used r.RemoteAddr, which is "ip:port" whenever no proxy header is
   present. The source port is ephemeral, so every new connection landed in a
   fresh bucket and any client that opened one socket per request defeated the
   limiter completely.

ClientIP walks X-Forwarded-For right to left, skipping configured trusted
proxies, and returns the address without a port. TRUSTED_PROXY_CIDRS defaults to
empty, which means no proxy is trusted - the safe direction, since it degrades to
"cannot see through a proxy" rather than "trusts any client that sends a header".

Also bound the inbound X-Request-ID: it was written verbatim into
audit_log.entity_id (VARCHAR(64)) as the entity identifier of every privileged
action, so an oversized value failed the INSERT and CR/LF could inject log
lines.

18 tests cover the spoofed-header, source-port, multi-hop and hostile-ID cases.

Co-Authored-By: Claude Opus 4.8 (1M context) <noreply@anthropic.com>`,
    paths: [
      'backend/internal/httpapi/middleware/requestid.go',
      'backend/internal/httpapi/middleware/clientip_test.go',
    ],
  },
  {
    id: 'audit-trail',
    msg: `fix(audit): make the audit trail impossible for a request to destroy

Three attacker-controlled inputs could silently delete the evidence row for
their own privileged action, and the error was discarded:

- a forged X-Forwarded-For that does not parse as an IP made the
  NULLIF($5,'')::inet cast raise, failing the whole INSERT
- an X-Request-ID over 64 chars overflowed entity_id
- a path over ~58 chars overflowed action ("POST /api/v1/payments/sandbox/
  orders/<uuid>/approve" always did, making those routes permanently
  unauditable)

Actions are now a discrete verb plus a resource path with ids reduced to :id,
so they fit the column, are filterable, and the full path moves to metadata.
The IP is validated before it reaches the inet column, and a failed write is
logged at ERROR instead of dropped with a discarded error.

Co-Authored-By: Claude Opus 4.8 (1M context) <noreply@anthropic.com>`,
    paths: ['backend/internal/httpapi/middleware/audit.go'],
  },
  {
    id: 'payments-gateway',
    msg: `fix(payments): stop treating a partial refund as a full one

mapStatus sent both "refund" and "partial_refund" to payment.refunded, and
onRefunded then credited the entire intent amount. A Rp 10 partial refund at
Midtrans paid the buyer Rp 100.000 and flipped the intent to refunded, so a
later legitimate refund could never be applied.

- partial_refund is now a distinct event carrying the refunded amount
- the handler fails closed when a partial notification has no amount, rather
  than guessing (guessing "the whole thing" is the bug)
- a refund larger than the charge is rejected
- signature comparison uses hmac.Equal on normalised hex instead of
  strings.EqualFold, which short-circuited on the first differing byte
- a malformed gross_amount is now an error instead of a silently swallowed
  parse producing 0.0

The old test asserted partial_refund -> payment.refunded, i.e. it pinned the bug
as correct. Replaced with two tests that would fail on the old behaviour.

Co-Authored-By: Claude Opus 4.8 (1M context) <noreply@anthropic.com>`,
    paths: [
      'backend/internal/payments/gateway.go',
      'backend/internal/payments/midtrans.go',
      'backend/internal/payments/midtrans_test.go',
      'backend/internal/domain/payment.go',
    ],
  },
  {
    id: 'refund-correctness',
    msg: `fix(payments): balance the refund legs, reverse commission, allow partials

Four defects in RefundOrder:

1. It ignored order.Status entirely. On a cancelled order whose intent was
   still captured, an admin could release the escrow (+seller) and then refund
   it (-seller, +buyer): the seller's net was zero but their balance and payout
   eligibility had grown, and a withdrawal in between turned the mint into real
   cash.
2. After a release it debited the seller the GROSS amount, though they were
   only ever credited the net. The platform kept its commission on a fully
   refunded order and the seller paid the fee from unrelated balance.
3. A partial refund marked the intent refunded, so a second refund was
   impossible - and the status gate rejected partially_refunded, making the
   partial branches unreachable dead code.
4. The two legs were independently rounded, drifting a sen per partial refund.

- guard on order.Status
- debit the seller their net and reverse the platform fee
- derive the seller leg as the residual so the legs sum to the refund by
  construction
- the cumulative refunded total comes from the ledger, which is the only record
  guaranteed to agree with the money that moved
- escrow-released is read from the ledger too, since partially_refunded no
  longer records it

Adds the Querier interface so a repository method can run inside a caller's
transaction - which is what makes "claim the row and move the money atomically"
expressible.

Co-Authored-By: Claude Opus 4.8 (1M context) <noreply@anthropic.com>`,
    paths: [
      'backend/internal/service/payment_service.go',
      'backend/internal/repository/payment_repo.go',
      'backend/internal/repository/querier.go',
      'backend/internal/httpapi/handler/payments.go',
    ],
  },
  {
    id: 'dispute-split',
    msg: `fix(payments): a dispute split could drain the platform commission pool

DisputeSplitCredit debited the platform wallet and credited the buyer, and
recorded NO state - it only *read* the intent's status. DisputeRepository.Resolve
was an unguarded UPDATE with RowsAffected discarded, and the admin endpoint
called both in sequence.

Resolving one dispute twice therefore paid the buyer twice. The only backstop
was wallets.balance >= 0, so one order could drain commission collected from
every other seller. The amount was intent.Amount / 2, a ~25x amplification of a
single dispute on a 2% commission.

- the dispute is claimed with a guarded UPDATE and RowsAffected checked, inside
  the SAME transaction as the wallet movements
- the payout is capped at the commission actually earned on that order
- a new disputed_split intent state makes a replay conflict
- ClaimForResolution / ClaimForReview / ReleaseToOpen give the caller a real
  claim-release cycle
- unique index uq_disputes_one_open_per_order (migration 00040)

Co-Authored-By: Claude Opus 4.8 (1M context) <noreply@anthropic.com>`,
    paths: [
      'backend/internal/service/market_service.go',
      'backend/internal/httpapi/handler/market.go',
      'backend/internal/repository/loyalty_repo.go',
    ],
  },
  {
    id: 'order-cancel',
    msg: `fix(orders): never cancel an order whose escrow is captured

CancelOrder accepted a paid order, restocked the inventory, and never touched
the payment intent. For a wallet payment the buyer's money had already left
their balance with no return path; for a gateway payment it sat at the provider
with no return path.

- a captured/released/partially-refunded intent now refuses cancellation with
  REFUND_BEFORE_CANCEL
- the guard FAILS CLOSED. The first version treated any error from the intent
  lookup the same as "no escrow", so a slow or saturated pool silently
  disabled it and a paid order could still be cancelled - the exact bug it
  exists to prevent, reachable by making Postgres slow
- the status change moved inside the restock transaction, using a guarded
  UPDATE. Previously it was a separate unguarded statement after the commit, so
  a crash in between left the order live with its stock already back on sale
  (oversell) and let a second sweeper release the same reservation twice
- CancelExpired counts and logs per-order failures instead of discarding them;
  it previously returned (0, nil), so asynq recorded SUCCESS and a sweeper
  failing 100% of the time looked identical to a healthy idle one
- the loyalty redemption debit failed with fmt.Println on a post-commit path,
  invisible to every log index and alert. It now logs at ERROR with enough
  context to reconcile, and OrderService gained the logger it needed

Co-Authored-By: Claude Opus 4.8 (1M context) <noreply@anthropic.com>`,
    paths: ['backend/internal/service/order_service.go'],
  },
  {
    id: 'worker-reliability',
    msg: `fix(worker): real Redis credentials, bounded shutdown, honest health

The worker passed only REDIS_ADDR to asynq. The moment production Redis
required a password, every scheduled job stopped - expired-order cancellation,
auto-completion, cart recovery, seller digests, low-stock alerts - with no error
surfaced anywhere. It went unnoticed for as long as nobody noticed missing
emails.

- the full config.RedisConfig is passed, including password and DB
- pingRedis at boot: a worker that cannot reach the queue now fails to start
  instead of looking healthy while processing nothing
- ShutdownTimeout bounds the drain. asynq's Shutdown() blocks until in-flight
  tasks finish and one of them is an SMTP send, so a blackholed mail host
  consumed every concurrency slot and wedged the queue permanently
- per-handler context timeouts bound a single task
- order expiry moved to the critical queue, MaxRetry(3) everywhere, and
  Retention so dead letters are not archived forever unread
- the asynq.Unique() windows are now LONGER than each job's period. The first
  version set every TTL just below its interval, so the lock had always expired
  and the guard was inert on all ten jobs - a check that cannot fail while
  reading like protection
- a nil logger defaults to slog.Default(); a nil one panicked during shutdown
- Stats/CheckQueue expose per-queue depth and age for the healthcheck

Also replaces net/smtp.SendMail, which dials with no timeout and sets no socket
deadline, with a bounded handshake.

Co-Authored-By: Claude Opus 4.8 (1M context) <noreply@anthropic.com>`,
    paths: [
      'backend/internal/worker/worker.go',
      'backend/internal/worker/worker_test.go',
      'backend/internal/mail/mail.go',
      'backend/cmd/worker/main.go',
    ],
  },
  {
    id: 'upload-caps',
    msg: `fix(upload): cap request bodies before the parser sees them

The declared size limits were decorative. ParseMultipartForm spools each part
to a temp file and the service checked header.Size only afterwards, so a 5 MB
cap did not prevent filling the disk. The CSV import called reader.ReadAll on
an unbounded body and looped over every row.

- http.MaxBytesReader on both endpoints, set before parsing
- RemoveAll on the multipart form so spooled temp files do not accumulate
- the CSV is streamed with a 5,000-row cap instead of buffered whole. A
  5,000-row import previously held one request open past the 60s timeout, so the
  client retried and created duplicate products
- ReuseRecord is deliberately NOT set: it makes encoding/csv reuse one backing
  slice, so the appended records would all alias the same overwritten data

Co-Authored-By: Claude Opus 4.8 (1M context) <noreply@anthropic.com>`,
    paths: [
      'backend/internal/httpapi/handler/media.go',
      'backend/internal/httpapi/handler/seller.go',
    ],
  },
  {
    id: 'migration-00040',
    msg: `feat(db): money-safety constraints and hot-path indexes

Moves eight money invariants out of Go into the schema, where a new code path
that forgets a guard cannot silently mint or lose money:

- payment_intents split legs must be non-negative
- platform_fees pct in range, fixed non-negative, at most one active row
- orders.total_amount >= 0 and discount_amount <= subtotal
- one live dispute per order
- one live return claim per order item
- one referral bonus per user
- one default address per user
- one wallet transaction per business event

Plus ~50 indexes, the most significant being orders(created_at). There was no
index supporting a bare created_at range, so every analytics query and every
worker sweep was a full sequential heap scan. orders is append-only with
physically correlated created_at, which is the textbook BRIN case.

The migration runs at application startup, so it is written to survive a
database that already violates a new constraint:

- duplicate disputes, returns and default addresses are repaired first, and the
  repair is announced
- unique indexes whose data cannot be repaired safely are SKIPPED with a
  RAISE WARNING rather than aborting the file: duplicate wallet_transactions are
  financial records and are never deleted to make a DDL statement succeed
- a two-statement repair runs BEFORE the constraint it protects (the first
  version had these the wrong way round, so a negative row failed the ADD
  CONSTRAINT instead of being repaired)
- Down drops the unique indexes BEFORE reopening the rows the repair touched;
  the other order violates the constraint being undone, so the rollback failed
  whenever the migration had actually done any work
- the two replacement indexes (products attributes, product_views time) are
  restored, not just dropped: a Down that leaves the schema worse than the Up
  found it is not a Down

scripts/check-migration.mjs asserts all of this statically in CI, because goose
has no dry-run and a migration that cannot be applied or rolled back is only
discovered at deploy time with the API refusing to boot.

Co-Authored-By: Claude Opus 4.8 (1M context) <noreply@anthropic.com>`,
    paths: [
      'backend/internal/db/migrations/00040_money_safety_and_hot_path_indexes.sql',
      'scripts/check-migration.mjs',
    ],
  },
  {
    id: 'router-wiring',
    msg: `fix(router): wire the hardened middleware and rate-limit keys

Connects the pieces: SetTrustedProxies at construction (before any request is
served), ClientIP-based rate-limit keys, the (IP, email) login bucket, the
referral per-user limit, the audit middleware's logger, the disputes repository
on PaymentService, and the order service's logger.

The login bucket reads the email without consuming the body: the prefix is
re-joined to the untouched remainder, so an oversized or mid-body-error request
still reaches the handler intact. The first version replaced the body with the
capped prefix only, so both failure paths truncated it and the handler failed
with a JSON parse error instead of the real 413.

Keying login on (IP, email) as well as IP bounds the per-account rate regardless
of how attempts are distributed - IP-only lets a botnet give one account 10
attempts per address, which is unlimited. The email is hashed so it is not
recoverable from a Redis key dump.

Co-Authored-By: Claude Opus 4.8 (1M context) <noreply@anthropic.com>`,
    paths: ['backend/internal/httpapi/router.go'],
  },
  {
    id: 'web-tooling',
    msg: `build(web): real build config, vitest, and a bundle budget

vite.config.ts had no build section at all. The result was a single 371 KB
entry chunk containing react-dom, the router, react-query, zustand, axios AND
the app code, so any app change invalidated 110 KB gzip of vendor code in every
visitor's cache.

- advancedChunks splits react / data / state / net; entry drops 371 KB -> 44 KB
- chunkSizeWarningLimit lowered to 300 KB: the old 500 KB default silently
  accepted the 371 KB chunk
- reportCompressedSize prints gzip in the build output so a regression shows up
  in CI rather than in the field
- target es2022, cssCodeSplit, modulePreload polyfill off
- envPrefix pinned to VITE_ so an accidental secret is a visible diff
- minify 'oxc' - naming esbuild fails the build, that path is deprecated in
  Vite 8 and esbuild is not installed

Adds vitest + jsdom from zero, and npm run size / typecheck / coverage.
Note: Vite 8 warns that advancedChunks is deprecated in favour of
codeSplitting; switched to the current name here.

Co-Authored-By: Claude Opus 4.8 (1M context) <noreply@anthropic.com>`,
    paths: [
      'web/package.json',
      'web/package-lock.json',
      'web/vite.config.ts',
    ],
  },
  {
    id: 'sanitizer',
    msg: `fix(web): close a stored XSS in the HTML sanitizer

The URL check was a startsWith('javascript:') on the trimmed value. DOMParser
decodes entities before we see them, so jav&#x0A;ascript: arrives as a real
newline, trim() removes only leading/trailing whitespace, the prefix check
passed, and the link executed on the public unauthenticated /help/:slug and
/legal/:doc pages.

- normalise by code point rather than regex character class, so control
  characters, zero-width and bidi marks cannot hide inside a scheme
- allowlist the scheme instead of blocklisting three prefixes; data: is
  refused outright
- a value containing ':' is never treated as a relative path, since browsers
  resolve "foo:bar" as a scheme

Four further gaps found while testing, each with a regression test:

- A bare "is" attribute survived both removeAttribute and removeAttributeNode,
  because it is
  special-cased by the DOM. Disallowed attributes are now verified and the
  element rebuilt if anything survived.
- "class" was allowlisted. The app is Tailwind, so an author-supplied class is
  a full styling primitive: class="fixed inset-0 z-[99999] bg-white" paints a
  viewport-sized overlay with arbitrary text. That is a phishing control, and
  with 'unsafe-inline' in the CSP (kept for Midtrans) this function is the only
  XSS control here. Removed from the allowlist.
- the parser promotes the CONTENT of <noscript>/<template>/<svg> when the
  container is discarded, so child.remove() left a live <img> behind. Those
  containers are now stripped from the string before parsing.
- walk was unbounded recursion and threw a RangeError at ~4000 nested nodes.
  Now iterative with an explicit depth cap.

77 tests total; 22 are XSS vectors. Also fixed two tests that could not fail and
one whose name asserted the opposite of the behaviour, since a misleading test
discourages the next person from fixing the code.

Co-Authored-By: Claude Opus 4.8 (1M context) <noreply@anthropic.com>`,
    paths: ['web/src/lib/sanitize.ts', 'web/src/lib/sanitize.test.ts'],
  },
  {
    id: 'web-components',
    msg: `feat(web): ErrorBoundary, Modal, QueryState and Countdown primitives

Four components, each closing a class of defect that had no shared solution.

ErrorBoundary: there were none, and several components could throw during
render. Rating did '★'.repeat(5 - Math.round(value)), a RangeError for any
value outside 0..5 - and Rating is in every ProductCard, so one bad aggregate
rating white-screened the whole product grid with no way back but a reload.

Modal: replaces six ad-hoc overlay divs. The app had 120 inputs with zero
htmlFor, and six dialogs with no focus trap, no Escape, no initial focus and no
focus return.

QueryState: ~75 of ~80 useQuery call sites rendered "Memuat..." on both loading
AND error, so a 500 or an offline start left the page loading forever with no
retry. Adopted across the ten highest-traffic surfaces.

Countdown: ProductPage (845 lines) and OrderDetailPage ran a 1 Hz setInterval
that re-rendered the whole page including every unmemoized ProductCard.
OrderDetailPage ran two. Only the countdown subscribes to time now, and
ProductCard is memoised.

Co-Authored-By: Claude Opus 4.8 (1M context) <noreply@anthropic.com>`,
    paths: [
      'web/src/components/ErrorBoundary.tsx',
      'web/src/components/Modal.tsx',
      'web/src/components/Modal.test.tsx',
      'web/src/components/QueryState.tsx',
      'web/src/components/QueryState.test.tsx',
      'web/src/components/Countdown.tsx',
      'web/src/components/Countdown.test.tsx',
      'web/src/test/render.tsx',
      'web/src/components/Rating.tsx',
      'web/src/components/ProductCard.tsx',
      'web/src/components/AnalyticsPanels.tsx',
      'web/src/components/FileUpload.tsx',
      'web/src/components/NotificationPrefsCard.tsx',
      'web/src/components/ShareButton.tsx',
    ],
  },
  {
    id: 'web-lib',
    msg: `fix(web): blob URL lifetime, popup-blocked Midtrans, compare state

- downloadFile called URL.revokeObjectURL synchronously after link.click(),
  which Firefox and Safari cancel; openDocument never revoked at all, leaking a
  blob per invoice. Both now revoke on a delay.
- the Midtrans fallback window.open() ran after two awaits, so it was no longer
  in the user-gesture task and the popup blocker ate it. A blank tab is now
  pre-opened synchronously in the click handler and navigated after.
- compare state moves to lib/compare.ts as one typed source of truth. The page
  read back objects cached in localStorage without re-fetching, so prices went
  stale and it broke across devices.
- formatIDR and formatDate built a new Intl.NumberFormat per call, on a path
  that runs 24+ times for one product grid.

Co-Authored-By: Claude Opus 4.8 (1M context) <noreply@anthropic.com>`,
    paths: [
      'web/src/lib/api.ts',
      'web/src/lib/format.ts',
      'web/src/lib/midtrans.ts',
      'web/src/lib/compare.ts',
    ],
  },
  {
    id: 'web-router',
    msg: `perf(web): lazy every route, bound every boundary, fix a dead /docs/api

HomePage, SearchPage, ProductPage and CartPage were imported eagerly, which is
why ProductPage (35 KB of source) sat in the entry bundle. All 57 routes are
now lazy.

LazyOutlet wrapped only /seller and /admin, so a cold navigation to any of the
other ~45 suspended to the root: React held the root suspended and the user got
a blank white page with no header or footer for the length of the chunk fetch.
Every route now gets its own Suspense + ErrorBoundary via route().

- /docs/api rendered HelpArticlePage, which reads a :slug param the route does
  not provide, so the "API Quickstart" link requested
  /help/articles/undefined and 404'd. Replaced with a real page.
- errorElement added at the root
- a /forgot-password alias so the login page can link to it

Co-Authored-By: Claude Opus 4.8 (1M context) <noreply@anthropic.com>`,
    paths: [
      'web/src/router.tsx',
      'web/src/pages/ApiQuickstartPage.tsx',
      'web/src/types.ts',
    ],
  },
  {
    id: 'web-session',
    msg: `fix(web): purge the service-worker cache on logout and account switch

Defence in depth for the cache-key issue fixed in the service worker itself:
the app now tells the SW to drop every bucket when the session changes, so no
user-scoped response can survive an account switch even if the SW is later
loosened.

Co-Authored-By: Claude Opus 4.8 (1M context) <noreply@anthropic.com>`,
    paths: ['web/src/stores/session.ts'],
  },
  {
    id: 'web-layout',
    msg: `fix(web): header and overlay components - a11y and 375px overflow

- the sticky header is one flex row whose search form had no min-w-0, so the
  input could not shrink below its intrinsic width and the page got a
  horizontal scrollbar on a 375px viewport. The inline search is now hidden
  below sm behind a link to /search.
- ChatWidget and NotificationBell were both w-96 plus a 24px inset: 408px on a
  375px screen, so the panel went off-screen and the close button was
  unreachable.
- the cart drawer and the notification dropdown became real Modals
- NotificationBell counted each SSE event twice (a local increment on top of
  the server count, which already included it), so the badge climbed by 2 per
  event and never reset until clicked. The local counter is gone. Opening the
  bell also marked EVERY notification read rather than the visible ones.
- header search and suggestions get stable keys and abortable requests
- type="button" added to 113 buttons; inside a form an omitted type is an
  implicit submit

Co-Authored-By: Claude Opus 4.8 (1M context) <noreply@anthropic.com>`,
    paths: [
      'web/src/components/layout/ChatWidget.tsx',
      'web/src/components/layout/Header.tsx',
      'web/src/components/layout/NotificationBell.tsx',
      'web/src/components/layout/RootLayout.tsx',
      'web/src/components/layout/VerifyEmailBanner.tsx',
      'web/src/components/OrderChat.tsx',
      'web/src/components/PaymentCountdown.tsx',
    ],
  },
  {
    id: 'web-storefront',
    msg: `fix(web): storefront - races, error states and unhandled rejections

Money and correctness first:

- CartPage's quantity stepper had no optimistic update and computed
  l.quantity - 1 from stale props, so five rapid clicks sent five requests
  from the same base and responses could land out of order. It also sent
  quantity 0 at quantity 1, with no rollback on failure. Now optimistic with
  rollback, per-row pending state, and the minus button removes the line at 1.
- CheckoutPage sent the RAW coupon/points while the quote was debounced, so
  typing a coupon and submitting inside the window created an order with a
  discount that was never quoted while the screen showed the pre-coupon total.
  The guard only checked that a quote object existed, despite a comment
  claiming otherwise. Now sends the debounced values and blocks on
  isPlaceholderData, which is the signal that the numbers on screen came from
  the previous query key.
- AccountPage modelled "new address" as id: '', which produced
  PUT /account/addresses/ (404) with no onError, so react-query swallowed the
  failure and adding a second address was impossible. Now an explicit
  id: string | null discriminant with error surfacing.
- VerifyEmailPage POSTed with no abort guard, so revisiting a consumed link
  showed "Verifikasi gagal" although the email WAS verified.
- BonusCenter keyed its calendar on toISOString (UTC) while daysInMonth came
  from local getDate: a WIB user after 17:00 saw tomorrow's cell, and a month
  boundary could render 2026-09-31. It also previewed today's award instead of
  the next day's.
- ComparePage was unreachable: nothing navigated to it.

Then a11y (htmlFor/id, aria-describedby, role=alert, error text), QueryState
adoption, and the 15 hand-rolled async handlers with no try/catch that produced
unhandled rejections. The CSV import cleared e.target.value after the await, so
a failed import could not be retried with the same file.

Co-Authored-By: Claude Opus 4.8 (1M context) <noreply@anthropic.com>`,
    paths: [
      'web/src/pages/AccountPage.tsx',
      'web/src/pages/BonusCenter.tsx',
      'web/src/pages/CartPage.tsx',
      'web/src/pages/CheckoutPage.tsx',
      'web/src/pages/ComparePage.tsx',
      'web/src/pages/ContactPage.tsx',
      'web/src/pages/DiscoverFeedPage.tsx',
      'web/src/pages/FlashSalePage.tsx',
      'web/src/pages/HelpCenterPage.tsx',
      'web/src/pages/HomePage.tsx',
      'web/src/pages/LivePage.tsx',
      'web/src/pages/LoginPage.tsx',
      'web/src/pages/MyReturnsPage.tsx',
      'web/src/pages/NotificationsPage.tsx',
      'web/src/pages/OrderDetailPage.tsx',
      'web/src/pages/OrdersPage.tsx',
      'web/src/pages/ProductPage.tsx',
      'web/src/pages/RegisterPage.tsx',
      'web/src/pages/ResetPasswordPage.tsx',
      'web/src/pages/ReturnModal.tsx',
      'web/src/pages/SearchPage.tsx',
      'web/src/pages/StorePage.tsx',
      'web/src/pages/TicketDetailPage.tsx',
      'web/src/pages/VerifyEmailPage.tsx',
      'web/src/pages/VouchersPage.tsx',
      'web/src/pages/WishlistPage.tsx',
    ],
  },
  {
    id: 'web-console',
    msg: `fix(web): Seller Center and Admin Console - a11y, tables and validations

- the six data tables sat in overflow-hidden, so 5-6 columns were clipped and
  unreadable on mobile. Now overflow-x-auto with a min-width table, plus
  caption/scope.
- SellerLayout and AdminLayout stacked their 9- and 19-link sidebars above the
  content on mobile, pushing the page below the fold on every navigation.
- the article editor and the product form became real Modals.
- AdminCommission's "fixed" field was Number(fixed || data?.fixed || 0), so a
  0 typed by the admin silently fell back to the stored value: the fixed
  commission could never be set to zero. Same bug in pct. Both now treat an
  explicit 0 as 0, and the range is validated client-side - that page changes
  every settlement.
- addBrand/toggleBrand/addAttribute and the other hand-rolled async handlers
  became mutations with onError.
- SellerWallet used prompt() as the ONLY input for a money withdrawal, with no
  min/max/integer validation: Number('') and Number('1e9') were accepted. Now a
  validated modal input bounded by the balance.
- SellerProducts could not set weight_grams or compare_at_price, so every new
  variant saved with weight 0 and per_kg_fee never applied - shipping was
  silently under-quoted for every seller. The fields now exist.
- low-stock and notification-preference toggles are per-row rather than
  disabling the whole form for the request duration.
- alert()/confirm()/prompt() are gone from the app: every one is replaced with
  a role=status / role=alert region or a real dialog.

Co-Authored-By: Claude Opus 4.8 (1M context) <noreply@anthropic.com>`,
    paths: [
      'web/src/pages/admin/AdminAnalytics.tsx',
      'web/src/pages/admin/AdminArticles.tsx',
      'web/src/pages/admin/AdminCoupons.tsx',
      'web/src/pages/admin/AdminFlags.tsx',
      'web/src/pages/admin/AdminOpsPages.tsx',
      'web/src/pages/admin/AdminPayouts.tsx',
      'web/src/pages/admin/AdminReports.tsx',
      'web/src/pages/admin/AdminReturns.tsx',
      'web/src/pages/admin/AdminReviews.tsx',
      'web/src/pages/admin/AdminShipping.tsx',
      'web/src/pages/admin/AdminStores.tsx',
      'web/src/pages/admin/AdminTickets.tsx',
      'web/src/pages/admin/AdminUsers.tsx',
      'web/src/pages/seller/SellerAnalytics.tsx',
      'web/src/pages/seller/SellerBundles.tsx',
      'web/src/pages/seller/SellerCoupons.tsx',
      'web/src/pages/seller/SellerLayout.tsx',
      'web/src/pages/seller/SellerLiveStudio.tsx',
      'web/src/pages/seller/SellerOrders.tsx',
      'web/src/pages/seller/SellerProducts.tsx',
      'web/src/pages/seller/SellerReturns.tsx',
      'web/src/pages/seller/SellerSettings.tsx',
      'web/src/pages/seller/SellerWallet.tsx',
      'web/src/pages/support/SupportConsole.tsx',
    ],
  },
  {
    id: 'web-pwa',
    msg: `fix(web): stop the service worker serving one user's data to the next

The SW cached EVERY GET /api/* response into one origin-wide Cache Storage
bucket and served from it on network failure.

Cache Storage keys are URL-only. Authorization is not part of the key and the API
sends no Vary: Authorization, so on a shared device a logged-out visitor, or
the next user, could be handed the previous user's /orders, /wallet,
/account/addresses and /notifications. Guest carts were the same: the guest
identity lives in the X-Session-Key HEADER, which the cache ignores entirely.

- /api/ and /uploads/ are now a pure network passthrough: no cache read, no
  cache write, no offline fallback. A 401 must reach the app so it can refresh
  or log out.
- only immutable content-hashed assets and the app shell are cached
- a CLEAR_USER_CACHE message purges everything on logout
- errors are never persisted: the old code stored 401/403/404/500 bodies, which
  outlive the condition that produced them
- the cache is bounded and rotates by version

Also makes the PWA actually installable. The manifest carried only a data: URI
SVG icon with sizes:"any", which Chrome/Android and iOS reject. Now real 192 and
512 PNGs plus a maskable variant, generated rather than hand-drawn so they stay
in sync, with an offline page and an online/offline auto-retry.

Co-Authored-By: Claude Opus 4.8 (1M context) <noreply@anthropic.com>`,
    paths: [
      'web/public/sw.js',
      'web/public/offline.html',
      'web/public/icon-192.png',
      'web/public/icon-512.png',
      'web/public/icon-maskable-512.png',
      'web/public/manifest.webmanifest',
      'web/index.html',
    ],
  },
  {
    id: 'e2e-tests',
    msg: `test(e2e): cover the flows that had none, and make CI deterministic

The suite had 22 tests, 14 of which were request-fixture API tests touching no
component at all, so apparent coverage was roughly 8 of 57 routes. Checkout,
payment, cart CRUD, the admin console, the seller console and every error state
were untested.

- webServer array so npx playwright test is self-contained. CI previously
  backgrounded the API, worker and Vite with a trailing & in one step and relied
  on them
  surviving into the next, and never waited for the seed to finish - so
  Playwright could run against a half-seeded database. The seed is now chained
  ahead of the API, so the readiness URL does not exist until seeding completes.
- the external-payment test was not idempotent: a second run found no pending
  order and took an early "return" that passed with zero assertions. It now
  builds its own order and also asserts a second confirmation is refused.
- report and follow tests accumulate state; both use per-run data and clean up.
- forbidOnly, workers: 1 (they mutate shared DB state), trace on failure

Five new specs, each written to fail on a defect that was live:

- checkout with a coupon, including per_user_limit enforcement
- cart stepper under rapid clicks: asserts one increment per PUT actually sent
  and that the DOM matches the server
- admin route denied for a logged-in buyer
- a forced 500/404/503 shows an error and a retry, not a perpetual "Memuat..."
- SSE returns 200 with text/event-stream. A metrics-middleware bug made every
  stream endpoint 500 and no test touched /stream/*.

39 tests across 6 files.

Co-Authored-By: Claude Opus 4.8 (1M context) <noreply@anthropic.com>`,
    paths: [
      'web/playwright.config.ts',
      'web/e2e/fixtures.ts',
      'web/e2e/smoke.spec.ts',
      'web/e2e/authz.spec.ts',
      'web/e2e/cart-stepper.spec.ts',
      'web/e2e/checkout.spec.ts',
      'web/e2e/error-states.spec.ts',
      'web/e2e/sse.spec.ts',
    ],
  },
  {
    id: 'nginx',
    msg: `fix(nginx): the security headers were being silently dropped

nginx's add_header is inherited ONLY if the child block defines no add_header of
its own. Every location that set Cache-Control - which is /, /assets/, /sw.js
and /manifest.webmanifest - therefore discarded the server-level headers. The
SPA's HTML document and every hashed asset shipped with no CSP, no HSTS and no
X-Content-Type-Options. The app is same-origin with /api and /uploads, so a
framed /admin/commission was directly actionable.

- the header set moves to security-headers.conf, included in EVERY location
  that defines its own add_header. One file, so a future header cannot be
  added in one place and forgotten in another.
- CSP with a Midtrans + YouTube allowlist. script-src needs 'unsafe-inline'
  because Snap fetches from the app origin and then injects a script carrying
  the payment token; there is no way to nonce a script a third party creates
  after load. That is documented in the file rather than left as a mystery.
- proxy_buffering off moves out of the blanket /api/ into ^~ /api/v1/stream/.
  It was disabling buffering for every ordinary JSON response - a throughput
  and memory regression for no benefit - while the SSE location had no explicit
  guarantee.
- = /docs and /uploads/ forwarded NO proxy_set_header at all, so the upstream
  saw Host: api and the client IP was lost from the audit trail.
- HSTS, Permissions-Policy, CORP, server_tokens off, http2, brotli types
- an edge limit_req_zone on $binary_remote_addr, deliberately NOT applied to
  streams, where one connection legitimately serves hundreds of events
- listens on 8080 and runs as the nginx user, so nothing in the container is
  root

brotli itself is held back in a snippet the Dockerfile empties when the module
is absent: it is not compiled into the official images, and a bare
\`brotli on;\` makes nginx -t fail, so the container would never start.

Co-Authored-By: Claude Opus 4.8 (1M context) <noreply@anthropic.com>`,
    paths: [
      'web/nginx.conf',
      'web/nginx-brotli.conf',
      'web/security-headers.conf',
    ],
  },
  {
    id: 'nginx-validator',
    msg: `test(web): prove the nginx check can fail

The previous check reported 0 errors while four real problems were present,
one of which would have let a deleted security header go unnoticed: the presence
test read the RAW file, and the snippet's own prose mentions the header names.

The check exists because the failure modes here are silent - a header set
dropped on an inherited level, a privileged port under a non-root USER, a
proxied location that drops Host, SSE without proxy_buffering off. So it needs
its own regression suite: check-nginx.test.mjs reintroduces 13 defects and
asserts each is reported.

Two of the 13 are the false passes this commit fixes. Two more are the checks
that were checking the wrong thing (a bare "compose publishes the right port"
claim that never opened compose, and a first-match USER that a builder stage
could mask).

Co-Authored-By: Claude Opus 4.8 (1M context) <noreply@anthropic.com>`,
    paths: [
      'web/scripts/check-nginx.mjs',
      'web/scripts/check-nginx.test.mjs',
      'web/scripts/measure-bundle.cjs',
    ],
  },
  {
    id: 'docker',
    msg: `build(containers): non-root web, digest pinning, and honest worker health

- the final web stage ran as root because nginx bound :80. It now runs as
  nginx on 8080, with the pid, temp and log paths created and chowned. The
  conf.d file is chowned too: the base image's entrypoint rewrites it before
  nginx starts, and a permission error there dies on start looking nothing like
  a config problem.
- every base image is pinned by digest. A floating tag means a rebuild months
  later silently produces a different image, and a compromised tag is trusted
  blindly.
- nginx -t runs at BUILD time, with the upstream host swapped for a literal
  address so the check does not need the api service to exist. A typo in
  nginx.conf now fails the build rather than after a full push.
- VITE_MIDTRANS_CLIENT_KEY is accepted as a build ARG. It is inlined at build
  time, so without this the documented variable could never reach an image and
  the Midtrans button was permanently disabled no matter what the operator set.
- the worker gains a real HEALTHCHECK. It has no HTTP listener, so
  A "worker --healthcheck" probe dials the queue with its own credentials and reads
  per-queue depth and age, failing on unreachable OR on a growing backlog. It
  reads only the Redis config rather than the whole config, because Validate()
  rejects a weak JWT_SECRET in staging and the worker signs no tokens - a
  full-config probe would mark a healthy worker unhealthy for an unrelated
  missing secret.
- compose publishes on loopback only. The API is unauthenticated-plaintext HTTP
  carrying credentials, JWTs, the refresh cookie, wallet balances and KYC
  documents; publishing it on every interface exposed all of that to the LAN.
- resource limits, log rotation, stop_grace_period, and web waiting for the
  API to report healthy.
- TRUSTED_PROXY_CIDRS is set here. Without it the API sees only the proxy's
  address and every per-IP rate limiter collapses into ONE shared bucket: a
  mass logout at 30 refreshes/min, and globally serialised registration.
- Redis gets --maxmemory 512mb with noeviction, not allkeys-lru. asynq keeps
  in-flight task payloads in the same keyspace, so any LRU policy can evict an
  accepted-but-unexecuted task under pressure - silently losing an expired-
  order cancellation. Cache writes failing is safe (callers already swallow
  cache errors and fall through to Postgres); an evicted order-expiry job is not.
- the worker receives REDIS_PASSWORD and REDIS_DB, which it previously did not.

Co-Authored-By: Claude Opus 4.8 (1M context) <noreply@anthropic.com>`,
    paths: [
      'backend/Dockerfile',
      'backend/Dockerfile.worker',
      'web/Dockerfile',
      'infra/compose/compose.full.yaml',
      'infra/compose/compose.yaml',
      'web/.env.example',
    ],
  },
  {
    id: 'ci',
    msg: `ci: pin actions, scan for secrets and vulns, and actually build the images

The pipeline ran gofmt, vet, build, tests, lint, a web build and Playwright.
It never ran docker build, never validated the compose file or the nginx
config, and had no dependency, secret or image scanning at all - for a platform
holding escrow, where one vulnerable transitive dependency is a direct loss
vector.

- every action pinned to a commit SHA; none of the seven jobs had a
  timeout-minutes
- govulncheck, gitleaks (with rules for a Midtrans server key and for
  go-env secret assignment, and a value-anchored allowlist so those rules can
  still fire on compose files), npm audit as a non-blocking report
- a docker job: compose config validation, buildx build of all three images,
  nginx -t in the built image, and a container healthcheck plus a real
  security-header assertion
- a worker-healthcheck job that actually exercises the probe, including against
  a blackholed listener, so a broken probe cannot pass
- vitest runs BEFORE the production build, so a sanitizer regression fails in
  seconds rather than after a full typecheck and bundle
- bundle size, the nginx check and its mutation suite, and the migration
  validator all report in the job
- the backgrounded & server step is gone, replaced by playwright's webServer

Adds dependabot (gomod, npm, docker, github-actions and the distinct
docker-compose ecosystem, or Postgres/Redis/mailpit would never get patch PRs)
and a CODEOWNERS list gating the paths where a mistake is a security hole or
invisible in review: the HTTP surface, payments, migrations, web/src/lib, and
the container and proxy config.

Co-Authored-By: Claude Opus 4.8 (1M context) <noreply@anthropic.com>`,
    paths: [
      '.github/workflows/ci.yml',
      '.github/dependabot.yml',
      '.github/CODEOWNERS',
      '.gitleaks.toml',
      'scripts/commit-split.mjs',
    ],
  },
  {
    id: 'docs',
    msg: `docs: tag every feature claim and write down what is actually missing

The README was a marketing artifact written as a specification. Roughly ten
advertised features were backend-only with no UI, three were cosmetic, one was
architecturally broken, and twelve endpoints had no caller at all.

Every bullet is now tagged shipped / partial / known gap against the same audit
that produced the tags, with a "Security controls worth naming" table pairing
each control with the specific defect it prevents and the test that proves it.
Known gaps is a 21-item prioritised list, P0 first, covering the things that
would actually cost money or customers: no gateway-side refund (a QRIS buyer
is refunded to a balance they cannot top up, which is also a Midtrans ToS
breach), no wallet top-up, no PPN or compliant invoice, no carrier integration
or shipment entity, a ledger that cannot be reconciled to cash, and money still
in float64 against a NUMERIC schema.

docs/POSTMORTEM.md records the four criticals, the specific reason each was
missed - all four were composition errors, two components each correct in
isolation - and what now prevents recurrence. It also states plainly what a
reader should distrust: the test suites do not reach the money paths, nginx is
not verified by nginx -t, and the earlier version of that checker had four
false passes.

Co-Authored-By: Claude Opus 4.8 (1M context) <noreply@anthropic.com>`,
    paths: ['README.md', 'docs/POSTMORTEM.md', 'scripts/backlog-report.mjs'],
  },
]

// ── verify the manifest before touching anything ────────────────────────────
const all = changed()
const assigned = new Map()
for (const c of C) {
  for (const p of c.paths) {
    if (assigned.has(p)) {
      console.error(`DUPLICATE assignment: ${p} (${assigned.get(p)} and ${c.id})`)
      process.exit(1)
    }
    assigned.set(p, c.id)
  }
}
const unassigned = all.filter((p) => !assigned.has(p))
const phantom = [...assigned.keys()].filter((p) => !all.includes(p))

if (unassigned.length) {
  console.error(`\nUNASSIGNED changed files (${unassigned.length}):`)
  for (const p of unassigned) console.error('  ' + p)
}
if (phantom.length) {
  console.error(`\nASSIGNED but not changed (${phantom.length}):`)
  for (const p of phantom) console.error('  ' + p)
}
if (unassigned.length || phantom.length) {
  console.error('\nRefusing to commit: the manifest does not match the tree.')
  process.exit(1)
}
console.log(`manifest ok: ${C.length} commits over ${assigned.size} files`)

// ── commit ──────────────────────────────────────────────────────────────────
if (process.argv.includes('--check')) {
  for (const c of C) console.log(`  ${c.id.padEnd(20)} ${c.paths.length} file(s)`)
  console.log('\n--check: manifest verified, nothing committed')
  process.exit(0)
}
const log = []
for (const c of C) {
  git('add', '--', ...c.paths)
  // Refuse to commit if git decided to stage something outside the group.
  const staged = git('diff', '--cached', '--name-only').split('\n').filter(Boolean)
  const outside = staged.filter((p) => !c.paths.includes(p))
  if (outside.length) {
    console.error(`\nABORT at ${c.id}: git staged ${outside.length} file(s) outside the group:`)
    for (const p of outside) console.error('  ' + p)
    process.exit(1)
  }
  if (staged.length === 0) {
    console.error(`\nABORT at ${c.id}: nothing staged`)
    process.exit(1)
  }
  writeFileSync('.git/COMMIT_EDITMSG', c.msg)
  execFileSync('git', ['commit', '--no-verify', '-F', '.git/COMMIT_EDITMSG'], { stdio: 'pipe' })
  const sha = git('rev-parse', '--short', 'HEAD')
  log.push(`${sha}  ${c.id}  (${staged.length} file(s))`)
  console.log(`${sha}  ${c.id}  (${staged.length})`)
}

const left = changed()
console.log(`\n${log.length} commits created`)
if (left.length) {
  console.error(`\nWARNING: ${left.length} file(s) still uncommitted:`)
  for (const p of left) console.error('  ' + p)
  process.exit(1)
}
console.log('working tree clean: every changed file is committed')
