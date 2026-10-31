package service

import "testing"

func TestSplitInclusiveTax(t *testing.T) {
	// DPP + tax must ALWAYS equal the charged total, for every rate and amount.
	cases := []struct {
		total float64
		rate  float64
	}{
		{111000, 0.11}, {100000, 0.11}, {250750, 0.11}, {999999, 0.11},
		{1, 0.11}, {100000, 0.1}, {33333, 0.12}, {100000, 0}, {0, 0.11},
	}
	for _, tc := range cases {
		dpp, tax := SplitInclusiveTax(tc.total, tc.rate)
		if dpp < 0 || tax < 0 {
			t.Errorf("total %.0f rate %v: negative split dpp=%.0f tax=%.0f", tc.total, tc.rate, dpp, tax)
		}
		if sum := dpp + tax; sum != moneyRound(tc.total) {
			t.Errorf("total %.0f rate %v: dpp %.0f + tax %.0f = %.0f",
				tc.total, tc.rate, dpp, tax, sum)
		}
		if tc.rate == 0 && (dpp != moneyRound(tc.total) || tax != 0) {
			t.Errorf("rate 0: dpp=%.0f tax=%.0f, want the full amount and no tax", dpp, tax)
		}
	}
}
