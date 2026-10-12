package service

import (
	"strings"
	"testing"

	"github.com/vincommerce/backend/internal/repository"
)

func releaseEscrowBody(t *testing.T) string {
	t.Helper()
	src := readSource(t, "payment_service.go")
	at := strings.Index(src, "func (s *PaymentService) ReleaseEscrow(")
	if at < 0 {
		t.Fatal("ReleaseEscrow is gone; update this test")
	}
	rest := src[at+1:]
	if end := strings.Index(rest, "\nfunc "); end > 0 {
		return src[at : at+1+end]
	}
	return src[at:]
}

// THE DEFECT.
//
// ReleaseEscrow read the stored fee split with `if intent.FeeAmount > 0`, using the
// amount as a stand-in for "has this been derived". A 0% rate produces a legitimate
// zero fee, so for a 0%-commission order that test reads as "no split stored" and
// falls through to fee = the whole amount, seller = 0 -- the seller is credited
// nothing and the platform books the entire sale as commission.
//
// The refund path in the same file already keys off `commission_computed` for this
// exact reason, and says so in a comment. This is the sibling that was missed.
func TestReleaseEscrowGatesTheStoredSplitOnCommissionComputed(t *testing.T) {
	body := releaseEscrowBody(t)

	if strings.Contains(body, "intent.FeeAmount > 0") {
		t.Errorf("ReleaseEscrow still uses fee_amount as the 'has a split' flag.\n"+
			"A 0%% rate is a legitimate zero fee, so that test reads a 0%% order as one "+
			"with no stored split and pays the seller nothing.\n%s", body)
	}
	if !strings.Contains(body, "if intent.CommissionComputed {") {
		t.Errorf("ReleaseEscrow does not gate the stored split on CommissionComputed.\n"+
			"That flag exists to tell 'not derived yet' apart from 'genuinely zero'.\n%s", body)
	}
}

// The default must fail toward the seller. If the recompute branch is ever removed
// or skipped, an unset split must not silently become "platform keeps everything".
func TestReleaseEscrowDefaultsToPayingTheSellerInFull(t *testing.T) {
	body := releaseEscrowBody(t)

	if !strings.Contains(body, "fee, sellerAmount := 0.0, intent.Amount") {
		t.Errorf("ReleaseEscrow no longer defaults to (fee 0, seller full amount).\n"+
			"A default of (full, 0) means any path that skips the recompute pays the "+
			"platform the whole sale.\n%s", body)
	}
}

// The behaviour the comments assume, pinned against the real function rather than
// against prose: at 0% the platform takes nothing and the seller is credited the
// whole charge.
//
// This is the case the whole defect is about, and it is reachable through the
// recompute branch -- Commission(0, 0, amount) -- which is what a 0%-rate order
// gets. If that ever stops holding, the release credits the wrong party.
func TestAZeroPercentRateCreditsTheSellerTheWholeAmount(t *testing.T) {
	const gross = 250_000.0

	fee, seller := repository.Commission(0, 0, gross)
	if fee != 0 {
		t.Errorf("a 0%% rate produced a fee of %.2f, want 0", fee)
	}
	if seller != gross {
		t.Errorf("a 0%% rate credited the seller %.2f of %.2f; the platform kept the "+
			"difference on a sale it was entitled to none of", seller, gross)
	}
	if fee+seller != gross {
		t.Errorf("the legs sum to %.2f, want %.2f -- money created or destroyed", fee+seller, gross)
	}
}

// And the non-zero case still uses the stored split verbatim, so the fix does not
// quietly start recomputing orders whose split was already agreed at charge time.
func TestReleaseEscrowUsesTheStoredSplitWhenItExists(t *testing.T) {
	body := releaseEscrowBody(t)

	at := strings.Index(body, "if intent.CommissionComputed {")
	if at < 0 {
		t.Fatal("no CommissionComputed gate; see the test above")
	}
	end := strings.Index(body[at:], "}")
	if end < 0 {
		t.Fatal("could not bound the gate")
	}
	gate := body[at : at+end]
	if !strings.Contains(gate, "intent.FeeAmount, intent.SellerAmount") {
		t.Errorf("the gate does not read the stored split.\n%s\n"+
			"Recomputing a split that was already derived is how an admin rate change "+
			"reaches a sale that was priced before it", gate)
	}
}
