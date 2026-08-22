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
		{2, 0, 10599, 211.98, 10387.02},
		{5, 1000, 100000, 6000, 94000},
		{2, 0, 50, 1, 49},
		{50, 0, 100, 50, 50}, // never exceed amount
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
