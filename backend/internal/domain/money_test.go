package domain

import (
	"encoding/json"
	"math"
	"testing"
)

func TestIDRFromFloatRoundsHalfAwayFromZero(t *testing.T) {
	cases := map[float64]IDR{
		100.4:  100,
		100.5:  101,
		100.6:  101,
		-100.5: -101,
		0:      0,
	}
	for in, want := range cases {
		if got := IDRFromFloat(in); got != want {
			t.Errorf("IDRFromFloat(%v) = %v, want %v", in, got, want)
		}
	}
}

func TestIDRPctMatchesTheOldMoneyRoundBehavior(t *testing.T) {
	// The commission/insurance paths computed moneyRound(amount*pct/100).
	// Pct must agree with that expression for every rate the system uses,
	// or migrating a call site changes what buyers are charged.
	for _, tc := range []struct {
		amount float64
		pct    float64
	}{
		{100000, 2}, {100000, 0.3}, {250000, 10}, {99999, 3},
		{1500, 0.3}, {1, 11}, {123457, 7.5},
	} {
		want := IDR(math.Round(tc.amount * tc.pct / 100))
		if got := IDR(tc.amount).Pct(tc.pct); got != want {
			t.Errorf("IDR(%v).Pct(%v) = %v, want %v", tc.amount, tc.pct, got, want)
		}
	}
}

func TestIDRPctNeverChargesNegative(t *testing.T) {
	if got := IDR(100000).Pct(-5); got != 0 {
		t.Errorf("negative rate = %v, want 0", got)
	}
	if got := IDR(-100).Pct(10); got != 0 {
		t.Errorf("negative amount = %v, want 0", got)
	}
	if got := IDR(0).Pct(10); got != 0 {
		t.Errorf("zero amount = %v, want 0", got)
	}
}

func TestIDRSubFloorsAtZero(t *testing.T) {
	if got := IDR(100).Sub(30); got != 70 {
		t.Errorf("100-30 = %v", got)
	}
	if got := IDR(100).Sub(100); got != 0 {
		t.Errorf("100-100 = %v, want 0", got)
	}
	if got := IDR(100).Sub(150); got != 0 {
		t.Errorf("100-150 = %v, want floored 0", got)
	}
}

func TestIDRJSONRoundTripsAsANumber(t *testing.T) {
	// Existing clients parse amounts as JSON numbers; the type change must be
	// wire-compatible.
	data, err := json.Marshal(IDR(150000))
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	if string(data) != "150000" {
		t.Fatalf("marshalled as %s, want a bare number", data)
	}
	var back IDR
	if err := json.Unmarshal(data, &back); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if back != 150000 {
		t.Fatalf("round trip = %v", back)
	}
}

func TestIDRUnmarshalRoundsFractionalInputOnce(t *testing.T) {
	// A client that still sends floats must not smuggle sen into the type.
	var v IDR
	if err := json.Unmarshal([]byte("999.6"), &v); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if v != 1000 {
		t.Fatalf("fractional input became %v, want rounded 1000", v)
	}
}
