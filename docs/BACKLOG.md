# Known gaps and open work

Carried out of the repository README, where it was crowding out every
other thing a reader needs. The order is deliberate: **P0 blocks real
money**, P1 is what is needed to be credible at scale, and P2 is
correctness and operability.

> **Open only (reviewer shortcut).** The rest of this file keeps the
> before/after history; this table is what is still true.
>
> | # | Gap | Status |
> |---|-----|--------|
> | P0-1 | Return-refund credits wallet, not gateway | Open — needs approve/refund split |
> | P0-2 | No wallet top-up (refund-only balance) | Closed — `833aa5c` |
> | P0-3 | PPN/NPWP/invoice sequence | Partly closed — PPN breakdown + invoice number `0f88c5e`; NPWP capture still open |
> | P0-4 | Money `float64` vs `NUMERIC(14,2)` end-to-end | Open — needs decimal |
> | P1-1 | No carrier label/rate adapter (only `manual`) | Partly closed — signed tracking webhook `424963f`; label+rate still open |
> | P1-2 | Payouts group but never pay (CSV human step) | Open |
> | P1-3 | Search English-stem, bestsellers not personalised | Search config closed — `09a8351`; personalised bestsellers still open |
> | P2-1 | No backup/PITR, no TLS terminator in-repo | Open — largest ops risk |
> | P2-2 | Return evidence had no UI and no validation | Closed — `9e250e6` |

* **[closed] Migrations `0032`-`00051` had never been executed.** They now apply
  cleanly against a scratch database, verified by `backend/scripts/migration-dryrun.py`.
* **[closed] `Migrate()` replayed `00001` against an already-migrated database**, so the
  application could not boot at all: goose keeps its own `goose_db_version` while this
  project's earlier history lived in a custom `schema_migrations`, and goose started at
  version 0. `adoptLegacyMigrationHistory` now adopts the legacy history, guarded by an
  evidence check -- seeding 31 onto an empty database would make goose skip 31
  migrations, so the adoption is refused unless `orders.insurance_fee` is actually
  there. Still needs one run against the real 53-table database.

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

   **[closed] Zero was doing duty as a sentinel for three different things.**
   `payment_repo.go` substituted the active rate when a caller passed
   `(pct 0, fixed 0)`, and `ReleaseEscrow` fell back to the active rate when the
   stored snapshot was `(0, 0)` — both reading "0" as "not provided". The columns
   are `NOT NULL DEFAULT 0`, so a 0% rate and a missing snapshot were the same two
   values, and an order priced while the active rate was 0% was charged the NEW
   rate if an admin raised it before delivery.

   Migration 00052 adds `commission_snapshot_present`, and the release and refund
   paths test it instead of the rate (`09496af`). The backfill marks only rows
   whose rate is non-zero — a non-zero rate could only have been written on
   purpose — and deliberately leaves the ambiguous `(0, 0)` rows on the old
   fallback rather than baking a guess into the schema.

   The remaining gap — a caller unable to request an explicit 0% per-order — is
   also closed (`4ee61e2`): the flag on the INPUT is now the signal, so a caller
   setting it says "these rates are mine, use them as given", `(0, 0)` included.
   No schema change; existing callers set neither rates nor the flag and take the
   default path unchanged.

   Worth keeping as a lesson rather than deleting: the tempting fix at every step
   was to let a missing `(0, 0)` mean 0%, which would have silently stopped
   charging commission on every intent predating 00042. The flag exists so that
   choice never has to be made.

### P1 — needed to be credible at scale

6. **No carrier integration.** Shipping is `base + kg × per_kg_fee`; there is
   no RajaOngkir/Shipper rate lookup, no volumetric weight, and no zone pricing.
   Remote-area orders are mispriced. [partly closed] The billable weight is now
   accumulated in grams and rounded up once. It previously divided **per line**
   and summed, so six 500g items (3.0kg) billed as **0kg** — free shipping on
   every order from a seller with a sub-kilogram catalogue — and a 2.2kg bundle
   billed as 1kg. Volumetric weight and zone pricing still need the carrier
   integration.
7. **[mostly closed] Shipments exist; carrier integration does not.** This entry
   said fulfilment was whole-order and all-or-nothing with no split shipping, no
   partial shipment and no return labels. None of that is true any more, and it
   had been false since before this entry was last read: `00050_shipments.sql` added
   `shipments` with per-parcel lines, sequence numbers and a derived order status
   (`shipped` / `partially_shipped`), so split and partial shipping work and the
   order status is derived from the boxes rather than set by hand;
   `00051_return_label.sql` added return labels, which is what made the return
   journey possible at all.
   *Still open:* no carrier integration — no rate lookup, no tracking poll and no
   shipment webhooks — which is why parcel status stops at `dispatched` and why
   `MarkShipmentDelivered` has no caller (see the P2 entry). Bulk label printing,
   pick/pack boards and SLA reporting are unbuilt, and the return journey depends on
   a seller manually recording that the goods arrived.
8. **[closed] Email was sent synchronously** inside the request or worker that triggers
   it, so checkout waits on 1–3 SMTP round trips. It should be enqueued. The
   asynq infrastructure already exists, so this is a queue and a dispatch.
   **[closed] The above understated it, and checking properly is what found that
   out.** There is **no `asynq.Client` anywhere in the repository** —
   only the worker's `Server` and `Scheduler`. The enqueue has to happen in the API
   process (that is where checkout runs), so this is not "add a task type": it means
   wiring a queue PRODUCER into a process that currently has none, plus a
   `TaskSendEmail` constant, a handler, a `mail.Client` on the worker's `Deps`, and
   an adapter so the service layer keeps its import boundary.
   *Done in the right order on the second attempt:* the producer first (`worker`'s
   `EmailEnqueuer`, over a real `asynq.Client`), then the consumer
   (`TaskSendEmail` + a handler whose nil-mail-client case FAILS rather than
   succeeding), then the wiring in both processes -- `cmd/api` sets the enqueuer,
   `cmd/worker` supplies the mail client. Only then was the service-side interface
   re-added, so it arrived with a caller.
   A test pins the two ends together: a producer enqueueing under a name no handler
   is registered for succeeds, delivers nothing and logs nothing, which is the
   quietest failure available.
   *Kept deliberately:* `emailFor` still does its DB lookups inline (they are
   cheap) and falls back to `mail.Send` when the enqueue fails, so an unreachable
   queue costs a slow request rather than a missing receipt.
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

**[resolved: unwired, not dead] `MarkShipmentDelivered` has no production caller.**
`ShipmentRepository.MarkShipmentDelivered` is reachable only from its own test. It is
a primitive for an OBSERVED carrier arrival, and nothing here receives one — there are
payment webhooks but no shipment webhooks and no tracking poll — so no caller can
exist. Parcel status stops at `dispatched` and `delivered_at` stays NULL.

It is NOT a money bug: order delivery runs on its own path (`ConfirmDelivery`,
`AdvanceShippedOrders`) and `CompleteDelivered` is what calls `ReleaseEscrow`.
`DeriveShippingStatus` only ever derives `shipped`/`partially_shipped`, so it cannot
reach `delivered` either. Escrow releases regardless; the gap is the parcel-level
audit trail.

It was tempting to wire it into the order's delivered transition. That would be wrong
twice over — it records an arrival nobody observed as fact, and it sets `delivered_at`,
which `BuyLabel` reads to refuse a label — and unnecessary. The reasoning now lives on
the function itself, since a reader finding an uncalled method has two wrong moves
available and neither is obvious from the signature (`ecb8ea5`).

*The genuine fix is a feature* — a carrier tracking integration — and is deliberately
left as a feature rather than a call site.

**Test coverage is very uneven, and the headline number is misleading.** Measured with
`go test ./internal/... -cover`, per package:

| Package | Coverage | Note |
| --- | --- | --- |
| `carrier` | 87.2% | |
| `payments` | 78.2% | gateway adapters, signature verification |
| `metrics` | 74.5% | |
| `config` | 71.6% | |
| `domain` | 64.9% | state machine + validation |
| `middleware` | 40.1% | |
| `service` | **10.4%** | the money package |
| `worker` | 8.9% | |
| `app` | 5.8% | |
| `repository` | **1.1%** | every SQL statement |
| `httpapi/handler` | **0.7%** | |
| `ai`, `cache`, `httpapi`, `mail`, `stream` | 0.0% | no test files |

Two things worth being precise about, because a bare percentage misleads in both
directions:

1. **Much of the `service` suite is structural**, asserting the SHAPE of source rather
   than executing it — which is why a package with dozens of tests reports 10.4%. Those
   tests are real and they have caught real defects, but they contribute zero statement
   coverage, so the number understates how much of this code has ever been reasoned
   about.

2. **The same fact means most of `service` has never been EXECUTED.** Structural tests
   constrain what the code says, not what it does. `repository` at 1.1% is the honest
   version of the same problem: essentially no SQL in this repository has ever run
   against a database in a test. The concurrency fixes in this series were verified by
   asserting operation ORDER in source, not by running two transactions — which is
   recorded, and is the weakest claim in the whole project.

Closing this needs an integration harness (testcontainers or a `docker compose` test
profile), not more unit tests: the untested surface is overwhelmingly I/O and SQL.
Until then, treat per-package `-cover` as the useful number and the repo-wide total as
noise.

14. **[partly closed] External payment is self-asserted.** A buyer can mark any
    pending order paid with a free-text reference and an amount checked against the
    total the same API just returned. It is one-shot (the order leaves `pending`, so
    a replay fails on status) and the amount is bounds-checked.
    **[closed] It wrote no `payment_intents` row, so the order could never settle.**
    `ReleaseEscrow` resolves the intent through `IntentByOrder`, so a confirmed
    external payment was `paid` with no money path behind it and completion failed
    `NO_INTENT` — silently unpaid on the buyer's route (the order had already moved
    to `completed`, so nothing retried it) and a permanent retry loop on the
    sweep's. `RecordExternalCapture` now books the payment inside the same
    transaction as the status flip, through the same primitives as every other
    capture (snapshot, guarded transition to `captured`, strict journal). It commits
    a CLAIM, not a verified receipt, and says so.
    *Still open: there is no admin verification gate.* The real answer to the trust
    question is an operator confirming the transfer before the order leaves
    `pending`; this entry is not closed until that exists.
15. **Several endpoints are backend-only with no UI.** Re-audited after the parcel
    and payout work, because this list had drifted in BOTH directions — it named
    things that had UI and missed things that did not.
    Now WITH UI: session management, multi-address checkout (checkout sends
    `address_id` from the address book), the admin refund button, payout batches,
    and the parcel / return-label / arrival flows.
    **[closed, and the entry itself was wrong] Return evidence.** This entry said
    there was no endpoint at all. That was not true: `POST /returns` accepted
    `evidence_urls`, `RequestReturn` passed it through and `CreateReturn` wrote the
    JSONB column. What was missing was everything around it -- no UI sent the field,
    no screen read it, and nothing validated it, so any string in the body reached
    the database and would have been rendered on staff pages once a screen did read
    it. `9e250e6` adds uploads (max 3) to the return modal, thumbnails on the buyer,
    seller and admin cards, and a validator that accepts only
    `/uploads/<32 hex>.(jpg|png|webp|gif)` on this platform. The first cut of that
    validator allowed any host with that path; a unit test rejected
    `https://evil.example/uploads/<hex>.jpg` and the host check was added.
    **[closed] Admin user-detail stats.** `GET /admin/users/{id}` returned order
    count, total spent, tickets, returns and wallet balance, and nothing fetched it
    — the list showed the account and never what it had done, which is what an
    operator needs before granting or revoking a role. Now a per-row Detail action
    opening a modal, fetched only for the row opened (a query without an `enabled`
    guard would fire once per row for data nobody asked for).
    **[closed] The CSV import template.** `GET /seller/products/import-template`
    existed and nothing could reach it, so the import UI asked sellers to produce a
    CSV whose columns were documented only in the handler. Now a download beside
    the import control, through `downloadFile` (the route is behind the seller auth
    group, so a bare link would 401). A test pins the two ends together: the
    importer reads columns positionally and skips row 0 on
    `HasPrefix(records[0][0], "name")`, so a served header that stopped starting
    with "name" would be imported as a product instead of skipped.
    **[closed] The admin order-release action.** `POST /payments/orders/{id}/release`
    existed, was admin/support-gated and audit-logged, and NOTHING called it — so an
    order whose automatic escrow release failed sat at `delivered` for ever with no
    way for an operator to clear it. It is now a confirmed button on the admin orders
    table, offered only on `delivered` orders (that is the state whose release is
    supposed to have happened, so this is a retry of a failure rather than a way to
    pay a seller early) and labelled as irreversible.
16. **Bundle pricing is display-only.** "Ambil Paket" adds items at full price.
17. **No PWA offline queue.** The cart is server-side, so add-to-cart fails
    offline even though the shell is cached.
18. **[partly closed] Accessibility is partial.** Modals, forms and tables were
    brought up to keyboard/screen-reader basics.
    **[closed] There is now an automated a11y gate.** `axe-core` runs over
    rendered components in `src/__tests__/a11y.test.tsx`, on the existing vitest
    run, so it is enforced by CI without a new job. Rules that need real layout
    (`color-contrast`, `target-size`, `scrollable-region-focusable`) are disabled
    because jsdom has no layout engine and would report every element as failing
    contrast for reasons that are not real. The suite includes a control test that
    asserts axe FINDS a known violation, so the three "no violations" assertions
    cannot pass vacuously.
    *Still open:* axe catches the mechanical half -- accessible names, roles, aria
    validity, alt text. It cannot see focus order, reading order, whether an
    aria-label is USEFUL, or anything requiring assistive technology. Those remain
    manual, and no automated gate will close them.
19. **[closed] No rate limit fell back to a local limiter** when Redis was down —
    limits failed *open*, so a Redis blip returned `/auth/login` to unlimited
    attempts. Failing closed was rejected as converting a degradation into a total
    lockout, so the limiter now falls back to an in-process fixed-window limiter and
    LOGS the fallback (once a minute, not once a request). It is weaker than the
    shared limiter — N replicas admit N × max — which is stated in the code rather
    than papered over. The fallback's map is bounded, and refuses rather than
    growing past `maxLocalWindows`, because the key is attacker-influenced and a
    Redis outage is when an attacker would choose to exploit that.
    While fixing it: `X-RateLimit-Remaining` was `max - count`, which goes
    **negative** once a window is exhausted, and was emitted on every over-limit
    response. Clamped.
20. **No database backup or PITR ships in-repo.** `SECURITY.md` says so. For a
    platform holding escrow and a wallet ledger this is the single largest
    operational risk.
21. **No TLS terminator ships in-repo.** Compose publishes HTTP on loopback and
    relies on the operator placing a proxy in front. The edge rate limiter has
    been fixed for that case (`real_ip` now resolves the real client before
    bucketing; without it, every visitor behind a terminator would have shared
    one bucket).

## Closed in the current series

These were found by a later audit pass than the list above and were fixed before it was
updated. Each has a commit whose message records the failure mode, the reasoning, and
what was and was not verified -- that is the authoritative record, not this list.

| Defect | Why it mattered |
| --- | --- |
| Migrations `0032`-`00051` never executed | They could not have been: goose splits on semicolons and has no dollar-quote awareness |
| `Migrate()` replayed `00001` | The application could not boot at all |
| Loyalty debited after order commit | 15 concurrent checkouts each redeemed one balance |
| Four parcel/return IDORs | Any seller could read another store's returns, buy a label at their expense, or unblock their refund |
| Midtrans dropped `refund_id` and `refund_amount` | No provider refund was ever applied automatically; the wrong amount reached reconciliation |
| `Commission` returned fractional rupiah | ~2.6% of orders could not be settled, so the seller was not paid |
| Remittance CSV had no formula escaping | Seller-supplied formulas executed on a finance operator's or bank's machine |
| Checkout idempotency was GET-then-SET | N retries produced N orders, N reservations, N charges |
| Two parcel read-then-insert races | Two concurrent requests orphaned a return parcel |
| Four mutation witnesses were false | The tool that certifies the tests was itself not testing |
| 150 hardcoded npm mirror URLs | Supply-chain smell in the lockfile |
| Mojibake in buyer-facing strings | Buyers were emailed subjects containing replacement characters |
| `web/README.md` was the Vite template | Described a starting point, not the application |
| Bundle picker shared a query key | `['seller-products', 1]` served the product LIST into the variant picker; the second `queryFn` never ran because a cache hit is a success |
| Inline shipping address was unvalidated | `shipping_address` is JSONB, so no column CHECK caught a blank recipient/street/phone; it surfaced days later as an undeliverable parcel |
| Releasing a reservation re-logged earlier releases | The ledger insert selected `status = 'released'`, i.e. EVERY release for the order, not the ones the call transitioned |
| Checkout locked variants in cart order | Two concurrent checkouts over the same two variants deadlocked; each transaction was individually correct |
| A wallet payment could mark a CANCELLED order paid | The status write was unguarded and `status <> $2` only refuses already-paid; the pending check runs BEFORE the transaction, so a cancel landing in between is overwritten after the wallet is debited |
| `ReleaseEscrow` used `fee_amount` as a "has a split" flag | Latent, and verified unreachable today -- but a 0% rate is a legitimate zero fee, so the predicate would credit the seller nothing and book the whole sale as commission |
| Three high advisories in `web` (axios direct) | axios carries every authenticated request; `npm audit` now reports zero |

**Still unverified, and it is worth being blunt about it:** the three concurrency fixes
were checked by asserting the ORDER of operations in source, not by running two
transactions at once. There is no integration suite and, in the environment this work
was done in, no working PostgreSQL and no Redis. The reasoning about READ COMMITTED is
from PostgreSQL semantics; it is not an observation of this code misbehaving.

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

## Cart-stepper e2e failure: cause NOT yet identified, and a correction

Status as of the `9f49c29` run: still failing. `cart-stepper.spec.ts:42` and `:126`
cannot see the row's increment button, and the page renders "Keranjangmu masih
kosong." for a buyer who has just written a line to their own cart via the API.

CORRECTION. Commit `1b03e4e` claims a product bug -- "every logged-in customer who
reloads the cart page sees an empty cart" -- caused by `CartPage` querying before
`session.restore()` finished. That claim is WRONG, or at least unproven. `App.tsx:31`
renders a full-screen "Memuat sesi..." placeholder while `loading` is true and does
not mount the routes until it clears, so the pre-restore query cannot happen. The
gate added in that commit is harmless defence in depth, but it is not the cause and
did not fix the failure. The mutation-checked test added in `9f49c29` pins the gate,
which is fine, but it pins a race that the app's own loading screen already
prevents.

RULED OUT, with evidence:
  - query fires before restore   -> App gates rendering on `loading` (App.tsx:31)
  - restore() fails after reload -> the failure snapshot shows `button "Keluar"`,
    i.e. the browser IS authenticated as the buyer
  - cart merge wipes the cart    -> CartRepository.Merge unions
    (INSERT ... ON CONFLICT DO UPDATE, LEAST(quantity), capped at 99) and only
    flips the guest cart to 'merged'; it deletes nothing
  - a selector/name mismatch     -> firstVariant returns product_name = product.name,
    which is what CartPage renders into the aria-label

NEXT STEP, cheapest first: have the failing test GET /api/v1/cart with the API token
immediately after the PUT and assert the line is there. That splits "the write did
not persist" from "the browser session resolves to a different cart", which is the
only remaining fork. The run's network trace answers the same question directly.

## Round 2: the cart is no longer empty, and the suite is bigger than I reported

Run `39262401019` (`3c738cb`): backend GREEN, Playwright 28 passed / 15 failed.

PROGRESS, measured: "Keranjangmu masih kosong." no longer appears in the failure log
(0 occurrences, previously 2 per attempt). The upsert in `235f33d` did what it said:
`PUT /cart/items` now creates the line, so the buyer's cart is no longer empty when
the page loads. That is the first real change in this symptom.

BUT THE TEST STILL FAILS, at the same assertion: the button
`Tambah jumlah Smartphone Aurora X5 Pro` is not visible. So an empty cart was part of
the story and not all of it. The cart now holds a line and the row still does not
render (or renders with a different accessible name). Next diagnostic is the run's
`error-context.md` again: it will show whether the page has a cart row at all, and
what the row is called.

CORRECTION TO MY OWN REPORTING. I previously described this suite as "4 failures"
because I grepped four matching lines out of the failure summary. It has 15, and
likely has had all along:

  - sse (2): "Failed to parse URL from /api/v1/stream/orders" -- the stream client is
    given a RELATIVE url, which works same-origin but not against the e2e dev server
  - checkout (2): COUPON_MIN_NOT_MET, plus a test-side assertion that the chosen
    fixture is above the Rp50.000 floor -- so `firstVariant` is picking a cheap line
  - regression-checkout-pdp (3): order tracking, PDP add-to-cart, checkout summary
  - smoke (4): swagger UI, coupons page, follow-store, external payment
  - error-states (2): retry + 500 handling
  - cart-stepper (2): the above

Reading four lines and calling it the failure count was the same mistake as grepping
ANSI output earlier: the check was wrong, not the system. The lesson is to take the
whole summary, not the lines that match the hypothesis I already had.

## Round 3: one fix worked, two did not

Run `39274959381` (`8517a4e`): Playwright 14 failing, was 15.

WORKED: seeding `shipping_methods` (c9e4581). `regression-checkout-pdp` "buyer A
cannot read buyer B's order" is gone -- order B could finally be placed. The
`SHIPPING_METHOD` / "could not place B's order: 400" symptoms are gone with it.

DID NOT WORK 1: the stream base URL fix (006102d). SSE still fails with "the stream
request never returned headers" and "no stream data arrived". The relative-URL smell
was real and the change is correct in isolation, but it was not the cause of these two
failures. Next: check whether the e2e web server actually defines VITE_API_URL (if it
does not, `${''}` is still a relative path and nothing changed), and look at what the
API's stream endpoint requires/returns in the job.

DID NOT WORK 2: the coupon fixture minPrice (8517a4e). The assertion is unchanged --
"the fixture must be a single unit above it" -- which means NO variant of
smartphone-aurora-x5-pro satisfies price >= 50_000, so the filter falls back to the
old selection. Before changing anything again, dump the variant price shape from
GET /products/smartphone-aurora-x5-pro in the e2e environment. Either the prices are
different from what the seed suggests, or the field is not what `firstVariant` reads
(`Number(v.price)` can be NaN, and NaN >= 0 is false, so a wrong field name would
silently produce exactly this).

STILL FAILING (14): cart-stepper (2), checkout coupons (2), error-states (2),
regression-checkout-pdp (2), smoke (4), sse (2).

The transferable lesson from this round is the round-2 one repeating: I fixed what
looked wrong (a relative URL, a fixture that picks by stock) and measured; one fix
was real, two were not. Measuring after each batch is the only reason that is known
instead of assumed.

## Round 4: 11 failing, down from 14

Run `39288585917` (`494da9c`).

FIXED, measured:
  - sse (2 of 2): the specs were calling page.evaluate on about:blank. Navigating to
    '/' first fixed both. The relative-URL change in NotificationBell was NOT the
    cause (Vite proxies /api), and the commit says so.
  - checkout coupon per_user_limit (1): gone after the catalogue was priced in rupiah.

STILL FAILING (11): cart-stepper 2, coupon "quote and the placed order" 1,
error-states 2, regression-checkout-pdp 2, smoke 4.

The pattern that is now established: the e2e suite keeps being right about symptoms
and wrong about nothing -- but the fixes have to be located by evidence. Four of the
six fixes in rounds 3-4 were found by disproving a hypothesis with a measurement
(minPrice changed nothing -> the data was wrong; SSE still failed -> the URL was not
the cause).

NEXT: the remaining coupon failure is one spec, so its error message is the whole
question. Then error-states (2) and regression (2) share the "page body not rendered"
shape and should be grouped; smoke (4) needs its own evidence pass.

## Round 5: 8 failing, down from 11

Run `39418696062` (`1b1873a`).

FIXED, measured: coupon quote assertion (test), both error-state mocks (test).

STILL FAILING (8): cart-stepper 2, regression-checkout-pdp 2 (single-variant add,
checkout summary), smoke 4 (swagger, follow-store, external payment, vouchers).

The single-variant spec STILL fails after its locator was corrected to "+ Keranjang".
Its new error-context contains no Locator/Error line at all -- so the next question is
not the selector but what actually failed (timeout? navigation? crash?). Re-read that
file in full before changing anything; the previous four guesses all cost a run.

## Round 6: 6 failed, 2 flaky, 35 passed

Run `39450114319` (`6f67089`).

The browser sign-in fixed the checkout-render spec's session, and follow-store +
external-payment are now FLAKY rather than failing (they pass on retry). The
single-variant PDP spec still fails, so the signed-out browser was not its whole
cause -- the HTTP-FAIL listener is still in that spec and the next run's log carries
the request that breaks it.

REMAINING (6): cart-stepper 2, regression-checkout-pdp 2 (single-variant at :30,
checkout render at :121), smoke swagger, smoke vouchers.

## Round 7: 37 passed, 6 failed -- the bare timeouts are gone

Run `39775054942` (`63a1743`).

The two specs that failed with "Test timeout of 60000ms exceeded" and no error at all
(the single-variant PDP and external payment) now PASS. The cause was proven from the
network trace: it held exactly one request -- POST /api/v1/auth/register -- with no
response, and the browser made none. The spec was killed inside the fixture's own
rate-limit wait: /auth/login and /auth/register are both 10/minute per IP, the suite
shares one IP, Retry-After is 60s, the fixture waited 62s, and the test timeout was
60s. The limiter is untouched; the suite is no longer louder than a person.

REMAINING (6), all with a real assertion error now:
  - cart-stepper 2: the row's "+ Tambah jumlah ..." button is not found, though the
    PUT succeeded. Next: capture the GET /cart response the browser received and
    compare its product_name with what the row renders.
  - regression "checkout page renders" 1: the order summary is not visible.
  - smoke 3: swagger (expect false), follow-store (expect false), vouchers (heading
    not visible).

Also: the Go toolchain pin moved to 1.27 for GO-2026-6809 (crypto/x509, stdlib) in
`21878ed`, which unblocked the Backend job -- and with it the Playwright job, which
had been SKIPPED whenever Backend failed.

## GREEN: CI 6/6, Playwright 43/43

Run `39975890009` (`cd99207`). Every job passes and the whole e2e suite passes with no
flaky retries. For context, the workflow had never completed a run: 71 consecutive
failures since 2026-08-25, all of them dying in seconds.

The last defect was the one the API log named immediately once its output was
captured: `ListActiveBySeller` selected 17 columns and scanned 15, so every PUBLIC
STORE PAGE returned 500. It had been broken for as long as the query existed and was
invisible only because no store row existed until the seed started creating them.

What the suite found that reading the code had not, across the whole run:
  - `env` in services.*.image        -> the entire workflow never started
  - four migrations missing +goose   -> 00041-00044
  - a real CVE (golang.org/x/text)   -> GO-2026-6629, then the stdlib GO-2026-6809
  - seed created no stores           -> the whole catalogue was 404
  - seed created no shipping methods -> every checkout was 400
  - seed priced the catalogue /1000  -> coupon floor unreachable
  - store slugs were synthetic       -> /stores/elektrostore 404
  - PUT /cart/items matched 0 rows   -> silent no-op, 200 with an unchanged cart
  - header and cart page shared a
    query key with different shapes  -> cart read an array, rendered empty
  - ListActiveBySeller 17 vs 15      -> every store page 500
  - networkidle vs the SSE stream    -> test could only time out
  - the fixture's own 62s rate-limit
    wait inside a 60s test timeout   -> two unexplainable timeouts

Now: product backlog only. Bundle pricing (needs a rule), admin verification for
off-platform payments (needs a decision), coverage harness (needs a machine where
PostgreSQL starts).

## Item 16 — bundle pricing: what implementing it actually requires

`bundles`/`bundle_items` have existed since 00014 and nothing in the checkout path
reads them. `bundles.price` is therefore decorative: the storefront advertises a
bundled price that the quote ignores.

A bundle is SELLER-SCOPED (`bundles.seller_id`), and its items are exact
`(variant_id, quantity)` rows with a primary key on both, so "strict match" is the
only rule the schema can express unambiguously: a bundle applies when a cart's lines
for that seller contain exactly those variants in exactly those quantities.

It is not a one-line price adjustment, because a discount must survive everything
that happens after it:

  1. Quote and Place must agree. They are separate code paths today; the discount has
     to be computed by one function both call, or the buyer is quoted one number and
     charged another.
  2. Per-LINE allocation. Refunds are per line and are capped by what was paid for
     that line, so a bundle discount has to be spread across its lines (by subtotal
     proportion, with the remainder assigned deterministically) and stored on the
     order lines, not just on the order.
  3. Commission. The commission snapshot is per line; a discount that is not
     allocated before commission silently overpays the seller.
  4. Coupons and points. Order of application is a business decision: stack, or
     best-of? Stacking a 10% coupon on a bundle price is a different discount depth
     than applying it to the sum of the parts.
  5. Partial carts. If the buyer adds a fourth item, does the bundle stop applying to
     the other three (strict) or still apply to the matching subset (subset)? Strict
     is what the schema implies and what the storefront shows.

DECISION NEEDED before code: (a) strict match, (b) subset match, and coupon order
(stack vs best-of). Recommendation remains (a) strict + best-of, because both are
unambiguous and neither can charge less than quoted.

Not implemented. A seeded bundle whose price is not honoured at checkout would be
worse than none: it would advertise a discount the API then ignores.

## Also fixed after green: the seller product list showed zeroes

`GET /seller/products` returns products without an embedded `variants` array, so the
table rendered "0 varian", Rp 0 and 0 stock for every row -- while `ListBySeller`
computed the stock sum and variant count and discarded them. Same defect class as the
public store page: a query computing data no field could hold.

Now returns `stock`, `variant_count` and `min_price` (MIN over variants), and the
table reads them. Two e2e specs pin it (payload + rendered cells). CI: 6/6 jobs,
**45/45** e2e.

## Item 16 CLOSED — bundle pricing

Run `40072780062` (`c3ba1e0`): CI 6/6, e2e **46/46**.

`bundles.price` was never read. It now is, under the two rules recorded above:

  - STRICT MATCH (schema-imposed): the seller's lines must be exactly the bundle's
    variants in exactly its quantities.
  - BEST-OF (business decision): the deeper of {bundles, coupon} applies; points stay
    additive.

Implemented as one pure matcher (`matchBundles`, six table-driven cases) plus a block
in `QuoteCheckout` placed BEFORE the points cap so a bundle cannot combine with
points to drive the merchandise total negative. `PlaceOrder` already called
`QuoteCheckout`, so quote and charge share one implementation by construction;
refunds are capped at the charged amount, so the discount cannot enable an over-refund.

Seeded bundle: 2 x Aurora Black at 10% off, priced from the variant so it cannot drift
from the catalogue. The e2e spec reads the same quote the storefront does and asserts
the discount applies at 2 units and is GONE at 3 -- the strict rule, end to end.

Two things this caught that reading would not have: the seed's parameter-type conflict
("inconsistent types deduced for parameter $1", only visible against real Postgres) and
the need for the decision to precede the points cap.

## Coverage harness, step 1: measured, and the first DB-backed test

Run `40086799868` (`c0cf829`): CI 6/6, e2e 46/46, integration test PASS.

COVERAGE IS NOW MEASURED ON EVERY RUN, not remembered: the unit-test step writes a
profile, prints the repository total (currently **8.7%**) and uploads it as an
artifact. Deliberately not a gate -- with no database-backed tests a threshold would
be arbitrary, and an arbitrary threshold teaches people to test the metric.

THE NUMBER THAT MATTERS is the per-package one: `repository` 1.1%, `httpapi/handler`
0.7%. Every defect that reached a 500 or a silent no-op in this project lived in
exactly that untested slice (a 17-vs-15 column scan, a PUT matching zero rows, a seed
statement Postgres refused), and no source-inspection test could see any of them.

So the second half: `TestQuoteAppliesSeededBundle`, opt-in via INTEGRATION_DB=1 and run
by CI AFTER the migrate+seed step, exercises the real SQL -- the cart write, the bundle
listing, the strict-match decision, and the quote arithmetic. It asserts three
invariants in one place:
  - the bundle applies at exactly its quantities and is gone one unit later;
  - the displayed discount equals the applied bundle's discount;
  - `DiscountFor(seller)`, which is what PlaceOrder actually subtracts, equals it too.
    That last one is the split that would be worst: quote honest, charge not.

NOT verified by mutation, unlike every other test added here: a mutation needs the
database, and this machine cannot start Postgres. The mutation it would catch is the
bundle feature itself (revert the quote integration and the first assertion fails),
but that is reasoning, not evidence, and it is recorded as such.

Next step if this is worth deepening: more DB-backed cases (coupon vs bundle best-of,
PlaceOrder's total against the quote) using the same harness. That is now cheap where
it used to be impossible.

## Coverage harness, step 2: the pricing rule is now verified where it runs

Run `40090764867` (`0270222`): CI 6/6, e2e 46/46, integration test PASS.

`TestQuoteAppliesSeededBundle` now covers, against a real migrated+seeded database:

  1. STRICT MATCH -- the bundle applies at exactly its quantities and is gone one unit
     later, with both the bundle list and the discount checked.
  2. DISPLAYED == CHARGED -- `DiscountAmount` (what the UI shows) equals both the
     applied bundle's discount and `DiscountFor(seller)` (what PlaceOrder subtracts).
  3. QUOTE == ORDER -- `placed.GrandTotal` equals the quote's `Total`, and the
     persisted order carries the same `discount_amount`. This is the invariant the
     whole checkout exists to keep.
  4. BEST-OF, BOTH DIRECTIONS -- a 1% coupon loses to the 10% bundle (coupon inert,
     discount unchanged) and a 50% coupon beats it (bundle inert, nothing doubled).
     Two throwaway coupons make it deterministic; the seeded WELCOME10's per-user
     limit may already be spent by the seed's demo orders.

Two harness lessons worth keeping:
  - opt-in via INTEGRATION_DB, because the suite must also run where Postgres cannot
    start; CI sets it after the existing migrate+seed step, so nothing was reordered;
  - a test that can only run in CI must say so in its own failure messages, because
    nobody will be able to iterate on it locally. The assertions here print the quote
    and the charge side by side for exactly that reason.

Coverage is 8.7% overall and `repository` is still 1.1% -- unchanged by this test,
because one integration test exercises a path, not a package. Raising that number
means many more DB-backed cases, which is now cheap rather than impossible.

## Item 14 CLOSED -- off-platform payments need admin verification

Run `40545005020` (`3a38f52`): CI 6/6, e2e 46/46.

THE DEFECT WAS WORSE THAN "UNVERIFIED". `POST /orders/{id}/external-payment` marked
the order PAID on the buyer's own say-so -- a reference string and an amount equal to
the total -- so any buyer could pay their own order with a fabricated reference and
the seller would ship against it. The comment beside the intent booking already named
the fix: "records a CLAIM, not a verified receipt".

NOW:
  - the buyer files a CLAIM (202), one open claim per order (partial unique index);
  - the order stays `pending` -- asserted in e2e, because that is the security
    property, not a happy path;
  - an admin sees the queue with the claimed amount beside the order's real total, so
    a mismatch is visible before anyone approves;
  - approval runs the existing paid-transition using the CLAIM'S figures;
  - rejection leaves the order pending: nothing is refunded because nothing was
    charged;
  - a decided claim cannot be decided again (guarded UPDATE, 0 rows = conflict).

`ConfirmExternalPayment` is now reachable ONLY from approval, pinned structurally
because the service methods need a database.

TWO THINGS WORTH KEEPING from how this landed:
  - the buyer UI distinguishes a claim from an instant wallet/Snap capture. Sharing
    the `done` flag would have told buyers a transfer was "received" when it had only
    been reported -- the same class of lie as the original endpoint;
  - the e2e test was inverted rather than extended: it used to assert that a buyer
    COULD pay, and now asserts they cannot. A test that only gains an approval step
    would have kept passing if the buyer endpoint quietly kept confirming.

## Coverage harness, step 3: the claim lifecycle, including the guards

Run `40637271266` (`e875224`): CI 6/6, e2e 46/46, three integration tests PASS.

`TestExternalPaymentClaimLifecycle` covers the flow end to end against Postgres, and
deliberately includes the four steps a happy-path test would skip:

  - a claim leaves the order pending (the security property);
  - a second pending claim is CLAIM_PENDING, enforced by the partial unique index;
  - rejection leaves the order pending and the claim recorded;
  - a rejected claim cannot then be approved;
  - rejection frees the order for corrected evidence, as a NEW claim;
  - approval is what pays it;
  - approval cannot happen twice.

Steps 2, 4 and 7 depend on SQL no source-inspection test can verify: a partial unique
index and an UPDATE guarded by `WHERE status = 'pending'`.

THREE DB-BACKED TESTS NOW, all in one CI step after the migrate+seed step:
money path (bundle, best-of, quote==charge), store queries (the two SQL regressions),
and the claim lifecycle. Coverage overall is still 8.7% -- these exercise paths, not
packages -- but the paths they cover are the ones that have actually broken.

## Dependabot sweep: 18 merged, 0 deferred

Run `41126980543` (`0b8e282`): CI 6/6, e2e 46/46 on postgres 18.

MERGED (each verified by a full green CI run before the next):
actions group, mailpit v1.29->v1.32, redis digest, oxlint 1.76->1.87,
@types/node 24->26, go-x (crypto 0.54->0.58, oauth2, sys), chi 5.3->5.4,
go-redis 9.21->9.23, playwright 1.62->1.64, nginx 1.27->1.29-alpine,
golang 1.26->1.27-alpine (builder, aligns with CI toolchain),
alpine 3.21->3.24 (runtime),
node 24->25-alpine (web builder, verified by web build + probe),
pgx 5.10->5.11, goose 3.27->3.29 (migrate+seed witness),
react 19.2->19.3 group (e2e witness),
build-tooling group (typescript ~6->~7, vite, plugin-react, vitest --
typecheck + build + unit + e2e all green on the major),
postgres 17->18 (compose + CI services together; migrate+seed and all three
INTEGRATION_DB tests PASS on 18, 46/46 e2e).

The playwright bump exposed a real CI defect and paid for itself: the browser
cache's restore-keys fell back to the previous lockfile's build, the install
step was skipped on a non-empty directory, and every spec failed at launch
with "Executable doesn't exist ... chromium_headless_shell-1248". The install
step now always runs `npx playwright install chromium` (downloads only what is
missing) and keeps --with-deps for true cache misses.

DEFERRED: none. The postgres major was the last item; it needed its own
cycle (compose bump in `3e54ac6`, CI services aligned in `0b8e282`) and the
full suite as witness. Merging the compose bump alone would have left CI on
17 -- green but proving nothing about 18.

## Parity audit follow-up: four defects fixed

Audit run against a Shopee/Tokopedia parity checklist (buyer, seller, admin,
cross-cutting, performance, security). The full gap list is the open table at
the top; these four were small, verifiable defects rather than features:

- `71d6d28` fix(i18n): the offline shell was the only screen with an English
  heading in an `lang="id"` app. It shipped with a service-worker version bump
  because a precached file alone never reaches an existing install -- browsers
  only re-install a worker when `sw.js` changes.
- `101f9cb` fix(config): `SplitOrigins` used `strings.Split`, so
  `"https://a, https://b"` silently blocked the second origin (the space is
  part of the value, and `Origin` never has one). Now trims, drops trailing
  slashes, empties and duplicates.
- `74a249f` fix(a11y): the three manual-transfer inputs had no accessible name
  -- placeholders only, and the date field not even that.
- `9e250e6` feat(returns): return photo evidence is captured (max 3), shown to
  buyer/seller/admin, and validated server-side. P2-2 closed.

Run `41177815185` (`9e250e6`): CI 6/6, 46/46 e2e. The a11y commit rode in the
same tree, so it is covered by that run; the offline and CORS commits each had
their own green run before it.

## Parity follow-up: wallet top-up, Indonesian search, carrier tracking

Three slices from the Shopee/Tokopedia checklist, each verified by its own
green CI run.

**Wallet top-up (`833aa5c`, P0-2).** The wallet had no way in: every credit
came from a refund, so "pay with wallet" was unreachable for an ordinary
buyer. `POST /wallet/topups` creates a charge, the provider's signed webhook
credits the balance exactly once (guarded `pending -> paid`), and the credit
posts a journal against a new `user_wallet:<id>` liability. Paying an order
with balance now journals the matching debit, and reconciliation sums
seller_available + user_wallet instead of reporting every top-up as drift.
Run `41188856185`: 6/6, the integration test delivers the paid webhook twice
and proves one credit and one balanced journal.

**Indonesian search (`09a8351`, P1-3).** Search stemmed Indonesian text with
the english Snowball config, so "makanan" and "makan" were unrelated lexemes.
Migration 00054 rebuilds the generated products column and the help-article
index with `indonesian`, and the queries use the same config. Run
`41186693835` PASS: `TestSearchStemsIndonesianText` asserts the stem works,
english does not, and an ordinary "laptop" search still returns rows.

**Carrier tracking (`424963f`, P1-1, partial).** `MarkShipmentDelivered` had
no caller: there was no shipment webhook. `POST
/shipping/tracking/webhook/{carrier}` accepts a normalized event signed with
HMAC-SHA256 over the raw body, stores it with a unique `(shipment_id,
event_key)`, and advances the parcel through the existing guarded transition.
An empty `CARRIER_WEBHOOK_SECRET` disables the route rather than accepting
unsigned events. Still open and deliberately not claimed: label purchase and
rate shopping for JNE/J&T/SiCepat, which need provider credentials.

**PPN breakdown and invoice numbers (`0f88c5e`, P0-3, partial).** The invoice
printed the order number in the fiscal position and showed no tax at all.
`orders.invoice_number` is now issued once by `invoice_number_seq` through a
guarded `WHERE invoice_number IS NULL` update (lazy, so a cancelled order
never burns a number; a second view reads the committed one). `INVOICE_PPN_RATE`
(default 0) prints DPP + PPN as an INCLUDED breakdown: prices are
tax-inclusive, rounding is taken on the PPN leg so the two lines sum to the
charged total exactly. Run `41203267540`: 6/6, `PASS:
TestInvoiceNumberIsAssignedOnce`.

Still open and not claimed: a seller NPWP field and its KYC capture, and a
per-fiscal-year sequence reset. A column nobody fills would be worse than the
gap.
