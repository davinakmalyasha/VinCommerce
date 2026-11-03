package service

import (
	"strings"
	"testing"
)

// Withdrawing a return unwinds money (the seller hold) before closing the
// claim. These assert the ordering and the gates, which need a live database
// plus a hold to observe behaviourally.
func TestWithdrawReturnGatesOnUndecidedStates(t *testing.T) {
	src := readSource(t, "seller_service.go")
	i := strings.Index(src, "func (s *SellerService) WithdrawReturn(")
	if i < 0 {
		t.Fatal("no WithdrawReturn on SellerService")
	}
	fn := src[i:]
	if end := strings.Index(fn, "\n}\n"); end > 0 {
		fn = fn[:end]
	}
	if !strings.Contains(fn, "domain.ReturnRequested") || !strings.Contains(fn, "domain.ReturnApproved") {
		t.Errorf("withdraw is not gated on requested/approved:\n%s\n"+
			"a decided or refunded claim takes a different path", fn)
	}
	if !strings.Contains(fn, "NOT_OWNED") {
		t.Errorf("withdraw does not check ownership:\n%s", fn)
	}
}

func TestWithdrawReturnReleasesTheHoldBeforeClosing(t *testing.T) {
	src := readSource(t, "seller_service.go")
	i := strings.Index(src, "func (s *SellerService) WithdrawReturn(")
	if i < 0 {
		t.Fatal("no WithdrawReturn on SellerService")
	}
	fn := src[i:]
	if end := strings.Index(fn, "\n}\n"); end > 0 {
		fn = fn[:end]
	}
	hold := strings.Index(fn, "ReleaseLiveHold")
	close := strings.Index(fn, "WithdrawReturn(ctx, returnID)")
	if hold < 0 {
		t.Errorf("withdraw does not release the seller hold:\n%s\n"+
			"the money would stay held until the lag job", fn)
	}
	if close < 0 {
		t.Errorf("withdraw does not close the claim:\n%s", fn)
	}
	if hold > close {
		t.Errorf("withdraw closes the claim before releasing the hold:\n%s\n"+
			"a failed release after the close strands the money held", fn)
	}
}

func TestWithdrawReturnIsPredicatedInTheRepository(t *testing.T) {
	src := readRepositorySource(t, "store_repo.go")
	i := strings.Index(src, "func (r *StoreRepository) WithdrawReturn(")
	if i < 0 {
		t.Fatal("no WithdrawReturn in store_repo.go")
	}
	fn := src[i:]
	if end := strings.Index(fn, "\n}\n"); end > 0 {
		fn = fn[:end]
	}
	// The service check-then-act races a seller decision; the UPDATE predicate
	// is what makes the loser fail instead of resurrecting a decided claim.
	if !strings.Contains(fn, "status IN ('requested', 'approved')") {
		t.Errorf("withdraw UPDATE is not predicated on undecided states:\n%s", fn)
	}
}
