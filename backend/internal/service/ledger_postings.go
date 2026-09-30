package service

import (
	"context"
	"fmt"
	"log/slog"

	"github.com/vincommerce/backend/internal/domain"
	"github.com/vincommerce/backend/internal/repository"
)

// The ledger postings for the money paths, and the rules they follow.
//
// Why these are helpers rather than inline in the payment service: a journal is a
// statement about where money came from and where it went, and a statement that is
// easy to get subtly wrong in six places is a statement that WILL be subtly wrong
// in one of them. Each helper below is a pure decision about which accounts a
// movement touches, which is testable without a database.
//
// Why a nil ledger is tolerated at all: a payment must not stop working because
// an optional subsystem is absent, or the fallback becomes a way to ship the
// original defect. So nil is allowed AND every skip is logged at Error. A
// reconciliation job then reports the drift. A silent skip would have reintroduced
// exactly the bug this ledger was built to remove, while looking deliberate.

// captureEntries builds the capture journal: credit escrow, debit a clearing
// account for the amount received.
//
// The clearing account depends on who is holding the cash, which is the whole
// reason this is a function rather than an inline literal. COD cash sits with a
// courier, so it belongs in cod_receivable; everything else came through a
// payment gateway, so it belongs in gateway_clearing. Posting COD to
// gateway_clearing makes the money appear to be a Midtrans balance we are owed
// for several days while it is actually in a courier's bag, and the
// reconciliation against a gateway settlement will never tie out.
//
// Extracted so the test asserts the SAME logic the service runs. A test with its
// own copy of the mapping passes while the service is wrong, which is precisely
// how a mutation that reroutes COD survived the first version of this file.
func captureEntries(amount float64, method string) []LedgerEntry {
	clearing := AccGatewayClearing
	if method == domain.MethodCOD {
		clearing = AccCodReceivable
	}
	return []LedgerEntry{
		// Escrow is a liability, so receiving money increases it on the CREDIT
		// side. Debiting it would leave escrow negative from the first payment.
		Credit(AccEscrowHeld, amount),
		Debit(clearing, amount),
	}
}

// postLedger writes a journal, logging and continuing on failure.
//
// The error is deliberately NOT propagated. The caller's transaction has already
// moved the money through the wallet tables, and failing the whole operation here
// would mean a buyer cannot be refunded because the accounting record could not be
// written. Losing an audit row is recoverable; losing a refund is not.
//
// The mitigation is that this is loud and that Reconcile proves the gap. A silent
// skip would leave nothing to notice.
func (s *PaymentService) postLedger(ctx context.Context, q repository.Querier, spec JournalSpec) {
	if s.ledger == nil {
		slog.Error("ledger not wired: a money movement posted no journal",
			"tx_type", spec.TxType, "ref_id", spec.RefID,
			"idempotency_key", spec.IdempotencyKey)
		return
	}
	if _, err := s.ledger.Post(ctx, q, spec); err != nil {
		slog.Error("ledger posting failed; the wallet movement is not accounted for",
			"tx_type", spec.TxType, "ref_id", spec.RefID,
			"idempotency_key", spec.IdempotencyKey,
			"error", err.Error())
	}
}

// postLedgerStrict is postLedger for a path that cannot continue without a journal.
//
// `postLedger` logs and returns, which is right for a movement that has already
// committed: telling the caller "this failed" would invite a retry of a money
// movement that already happened. But it is exactly wrong when the journal, the
// wallet row and the reservation are ONE event -- then a swallowed error leaves
// the balance moved with no accounting entry and no way to notice, which is the
// defect 0cb43d1 was.
//
// So: two functions, and the choice is made by whether the posting is part of the
// same transaction as the thing it describes.
func (s *PaymentService) postLedgerStrict(ctx context.Context, q repository.Querier, spec JournalSpec) error {
	if s.ledger == nil {
		return domain.E(domain.KindConflict, "LEDGER_UNAVAILABLE",
			"the ledger is not wired, so this money movement cannot be accounted for; "+
				"refusing rather than moving money with no journal")
	}
	if _, err := s.ledger.Post(ctx, q, spec); err != nil {
		return err
	}
	return nil
}

// withMeta attaches metadata to every entry in a journal.
//
// Applied to the whole journal rather than per-entry because every line of a
// posting shares the same reason it exists, and repeating the same map literal at
// each site is how one line ends up missing it.
func withMeta(entries []LedgerEntry, meta map[string]any) []LedgerEntry {
	out := make([]LedgerEntry, len(entries))
	for i, e := range entries {
		out[i] = WithMetadata(e, meta)
	}
	return out
}

// releaseEntries builds the release journal.
//
// Debits escrow for the GROSS and credits the two things the gross became: the
// seller's personal account for their net, and platform_commission for ours.
//
// The gross on the debit side is the part that is easy to get wrong. Debiting the
// two net figures instead would sum to `sellerAmount + fee`, which is the gross
// only if the split is exhaustive -- and it leaves escrow carrying whatever the
// rounding dropped. A residual in escrow reads as money we still owe that we have
// already disbursed, and it never clears.
//
// Extracted for the same reason as captureEntries: so the test exercises the code
// the service runs.
func releaseEntries(sellerID string, gross, sellerNet, fee float64) []LedgerEntry {
	return []LedgerEntry{
		Debit(AccEscrowHeld, gross),
		Credit(PersonalAccount(sellerID), sellerNet),
		Credit(AccPlatformCommission, fee),
	}
}

// refundEntries builds a refund journal.
//
// Two shapes, and the choice between them is the whole subtlety:
//
//	NOT RELEASED -- still in escrow:
//	    debit escrow_held, credit gateway_clearing
//
//	RELEASED -- already disbursed:
//	    debit seller_available, debit platform_commission, credit gateway_clearing
//
// The released case is where a marketplace loses money quietly. Crediting escrow
// after a release, when escrow is zero, gives a negative escrow balance that reads
// as a receivable from nobody -- and the journal still balances. Reversing only
// the seller's leg leaves the platform its commission on a sale it refunded.
func refundEntries(sellerID string, amount, refundSeller, refundFee float64, wasReleased bool) []LedgerEntry {
	var entries []LedgerEntry
	if wasReleased {
		if refundSeller > 0 {
			entries = append(entries, Debit(PersonalAccount(sellerID), refundSeller))
		}
		if refundFee > 0 {
			entries = append(entries, Debit(AccPlatformCommission, refundFee))
		}
	} else {
		entries = append(entries, Debit(AccEscrowHeld, amount))
	}
	return append(entries, Credit(AccGatewayClearing, amount))
}

// sellerHoldEntries moves money OUT of a seller's spendable balance and into their
// held balance.
//
// This is the posting that makes a hold actually hold. A hold recorded as a
// `seller_reservations` row is a NOTE; the money stays withdrawable because
// `RequestPayout` reads `wallets.balance` and nothing else. Transferring the value
// between the two accounts is what removes it from the balance the payout path
// checks -- which is why there is no "available = balance - held" arithmetic
// anywhere: the transfer already did it.
//
// Two entries, and the reason it can be two is that the account is a LIABILITY on
// both sides. A liability increases on credit, so taking a hold is a debit of what
// the seller can spend and a credit of what they cannot; the release is the exact
// mirror. An asset/liability pair would have needed the signs worked out, and a
// sign error here would post cleanly and mean the opposite of the truth.
func sellerHoldEntries(sellerID string, amount float64) []LedgerEntry {
	return []LedgerEntry{
		Debit(PersonalAccount(sellerID), amount),
		Credit(SellerHeldAccount(sellerID), amount),
	}
}

// sellerHoldReleaseEntries is the EXACT mirror of sellerHoldEntries.
//
// A reversal expressed as a reversal rather than as an independent second posting,
// so the two journals read as the same event in opposite directions and a reader
// can check them against each other. `Post` is idempotent on its key, so a
// repeated release returns the original rather than moving the money twice.
func sellerHoldReleaseEntries(sellerID string, amount float64) []LedgerEntry {
	return []LedgerEntry{
		Debit(SellerHeldAccount(sellerID), amount),
		Credit(PersonalAccount(sellerID), amount),
	}
}

// payoutRequestedEntries builds the journal for a withdrawal request.
// Debit the seller's personal account, credit seller_pending. NOT bank_clearing:
// the money has left what the seller can spend, but no transfer has been
// attempted, so booking it as cash already in our bank would report a payment
// that has not happened.
//
// seller_pending rather than escrow_held because the money was released to this
// seller and is now owed to them specifically. Putting it back in escrow would
// make "money we are holding for the marketplace" include a withdrawal already
// queued, which is the number an operator reads when asking what is at risk.
func payoutRequestedEntries(sellerID string, amount float64) []LedgerEntry {
	return []LedgerEntry{
		Debit(PersonalAccount(sellerID), amount),
		Credit(AccSellerPending, amount),
	}
}

// payoutSettledEntries builds the journal for a completed transfer.
//
// Debit seller_pending, credit bank_clearing. This is the moment the money
// genuinely leaves: a liability we owed becomes cash in the platform's own
// account, and the pipeline empties.
func payoutSettledEntries(sellerID string, amount float64) []LedgerEntry {
	return []LedgerEntry{
		Debit(AccSellerPending, amount),
		Credit(AccBankClearing, amount),
	}
}

// payoutFailedEntries is the exact mirror of payoutRequestedEntries, because a
// failed payout is the un-doing of the request: the seller gets their balance
// back and the pipeline empties without ever reaching the bank.
//
// A reversal rather than a new unrelated posting, so the two journals read as
// halves of one story when an operator reads the trial balance for a seller.
func payoutFailedEntries(sellerID string, amount float64) []LedgerEntry {
	return []LedgerEntry{
		Debit(AccSellerPending, amount),
		Credit(PersonalAccount(sellerID), amount),
	}
}

// refundKey builds the idempotency key for a refund journal.
//
// It keys on the CUMULATIVE refunded total, not the refund amount. Two refunds of
// the same amount on one order are two different events, and keying on the amount
// alone would silently swallow the second -- the original uncapped-refund bug
// wearing a different hat. A genuine replay computes the same cumulative total and
// therefore the same key, which is what makes it a no-op rather than a second
// refund.
//
// Extracted as a function so the property is testable and so all three call sites
// (refund, write-off) cannot drift into keying differently.
func refundKey(intentID string, remaining, amount float64) string {
	return fmt.Sprintf("refund:%s:%.0f", intentID, moneyRound(remaining+amount))
}

// postRefundJournal writes the journal for a refund.
//
// This is the most subtle of the three postings, and the shape of the accounts
// depends on whether escrow had already been released:
//
//	NOT YET RELEASED -- the money is still in escrow:
//	    debit  escrow_held          (the money leaves the holding account)
//	    credit gateway_clearing     (and goes back out to the buyer's bank)
//
//	ALREADY RELEASED -- the money has been disbursed to seller and platform:
//	    debit  seller_available     (claw back what the seller was credited)
//	    debit  platform_commission  (reverse the commission we earned)
//	    credit gateway_clearing     (and out to the buyer)
//
// The released case is the one that is easy to get wrong. Crediting escrow after
// a release, when escrow is already zero, produces a negative escrow balance that
// reads as a receivable from nobody. And only reversing the SELLER's leg without
// the commission's is how a marketplace keeps its fee on a fully refunded order.
//
// The idempotency key includes the cumulative refunded total, not the refund
// amount. A second refund of the SAME amount on the same order is a different
// event and must not collide with the first -- keying on the amount alone would
// silently swallow it, which is the original uncapped-refund bug wearing a
// different hat.
func (s *PaymentService) postRefundJournal(
	ctx context.Context, q repository.Querier, order *domain.Order, intent *domain.PaymentIntent,
	plan *refundPlan, refundToBuyer bool, reason string,
) {
	if !refundToBuyer {
		// A write-off: the money stays with the platform because it cannot be
		// returned to the buyer. Debiting the buyer payable and crediting the
		// writeoff expense is what keeps the balance sheet honest about having
		// absorbed a loss rather than quietly keeping revenue.
		s.postLedger(ctx, q, JournalSpec{
			IdempotencyKey: "writeoff:" + refundKey(intent.ID, plan.Remaining, plan.Amount),
			TxType:         TxTypeRefund,
			RefType:        "order",
			RefID:          order.ID,
			Note:           "refund written off: " + reason,
			Entries: withMeta([]LedgerEntry{
				Debit(AccRefundWriteoff, plan.Amount),
				Credit(AccGatewayClearing, plan.Amount),
			}, map[string]any{
				"order_id": order.ID, "reason": reason, "written_off": true,
			}),
		})
		return
	}

	// A refund of the full remaining balance drains escrow entirely, and
	// refundEntries debits it by the refund amount, which is that remaining
	// balance. So the not-released branch leaves escrow back at zero rather than
	// carrying a residual.
	entries := refundEntries(order.SellerID, plan.Amount, plan.RefundSeller, plan.RefundFee, plan.WasReleased)

	s.postLedger(ctx, q, JournalSpec{
		IdempotencyKey: refundKey(intent.ID, plan.Remaining, plan.Amount),
		TxType:         TxTypeRefund,
		RefType:        "order",
		RefID:          order.ID,
		Note:           "refund: " + reason,
		Entries: withMeta(entries, map[string]any{
			"order_id":        order.ID,
			"reason":          reason,
			"escrow_released": plan.WasReleased,
			"refund_seller":   plan.RefundSeller,
			"refund_fee":      plan.RefundFee,
			"partial":         plan.Partial,
		}),
	})
}
