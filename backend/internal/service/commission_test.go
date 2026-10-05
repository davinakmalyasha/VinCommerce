package service

import (
	"testing"

	"github.com/vincommerce/backend/internal/domain"
	"github.com/vincommerce/backend/internal/repository"
)

func TestCommissionSplit(t *testing.T) {
	cases := []struct {
		pct, fixed, amount  float64
		wantFee, wantSeller float64
	}{
		// 2% of 10599 is 211.98. These expectations used to say 211.98, which
		// ENCODED THE BUG: a fractional-rupiah leg cannot be charged to a gateway,
		// and Postgres rounded the two legs independently on the way into NUMERIC(14,2),
		// so they stopped summing to the charge and payment_intents_split_sums refused
		// the write -- leaving the seller unpaid. The fee is now rounded first (212)
		// and the seller leg derived as the residual, so the legs always sum exactly.
		{2, 0, 10599, 212, 10387},
		{5, 1000, 100000, 6000, 94000},
		{2, 0, 50, 1, 49},
		{50, 0, 100, 50, 50}, // never exceed amount
		// Rounds UP, so the platform absorbs at most half a rupiah. Flooring here
		// would cost a rupiah per transaction on every transaction, forever.
		{2, 0, 10598, 212, 10386},
		{2, 0, 10597, 212, 10385},
		{1, 0, 150, 2, 148}, // 1.5 -> 2
		{1, 0, 250, 3, 247}, // 2.5 -> 3, half away from zero
		// Fixed fee above the order must not produce a negative or oversized fee.
		{2, 100000, 50, 50, 0},
		{0, 0, 100000, 0, 100000},
	}
	for _, c := range cases {
		fee, seller := repository.Commission(c.pct, c.fixed, c.amount)
		if fee != c.wantFee || seller != c.wantSeller {
			t.Errorf("Commission(%v,%v,%v) = (%v,%v), want (%v,%v)", c.pct, c.fixed, c.amount, fee, seller, c.wantFee, c.wantSeller)
		}
	}
}

func TestCouponDiscount(t *testing.T) {
	percent := &domain.Coupon{Type: "percent", Value: 10, MinSubtotal: 0}
	if d := couponDiscount(percent, 100000); d != 10000 {
		t.Errorf("percent discount = %v, want 10000", d)
	}
	fixed := &domain.Coupon{Type: "fixed", Value: 50000}
	if d := couponDiscount(fixed, 30000); d != 30000 {
		t.Errorf("fixed capped at subtotal: got %v want 30000", d)
	}
	cap := &domain.Coupon{Type: "percent", Value: 50, MaxDiscount: float64Ptr(10000)}
	if d := couponDiscount(cap, 100000); d != 10000 {
		t.Errorf("max discount cap = %v, want 10000", d)
	}
}

func TestOrderStateMachine(t *testing.T) {
	if !domain.CanTransition(domain.OrderPending, domain.OrderPaid) {
		t.Error("pending->paid should be allowed")
	}
	if !domain.CanTransition(domain.OrderDelivered, domain.OrderCompleted) {
		t.Error("delivered->completed should be allowed")
	}
	if domain.CanTransition(domain.OrderPaid, domain.OrderDelivered) {
		t.Error("paid->delivered should be forbidden")
	}
	if domain.CanTransition(domain.OrderCompleted, domain.OrderShipped) {
		t.Error("completed->shipped should be forbidden")
	}
	if !domain.CanTransition(domain.OrderDelivered, domain.OrderReturnRequested) {
		t.Error("delivered->return_requested should be allowed")
	}
}

func float64Ptr(v float64) *float64 { return &v }
