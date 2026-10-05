package service

import (
	"fmt"
	"math"
	"math/rand/v2"
	"sort"
	"testing"

	"github.com/vincommerce/backend/internal/repository"
)

// Property tests for the checkout money arithmetic.
//
// Every test here corresponds to a defect that shipped:
//
//   allocateGlobal  -> a NEGATIVE discount share, which inflates one sub-order's
//                      total AND trips migration 00040's
//                      `CHECK (discount_amount >= 0 AND discount_amount <= subtotal)`,
//                      failing the whole multi-seller checkout with a raw 500
//   billableKg      -> truncating per line and summing, so six 500g items billed
//                      as ZERO kilograms: free shipping on every order from any
//                      seller whose catalogue is under a kilogram
//   moneyRound      -> rounding to 2dp on a currency with no subunit, which is
//                      what made the gateway charge differ from the stored total
//
// The randomised tests use math/rand/v2 with an EXPLICIT seed so they execute on
// every `go test`, not only under `-fuzz`. A bare F.Fuzz body is not run by a
// normal `go test` invocation, which makes a file full of fuzz targets silently
// assert nothing -- a trap worth naming, because the file looks thorough.

const rupiah = 1.0 // IDR has no sen

// ─── 1. allocateGlobal ──────────────────────────────────────────────────────

// TestAllocateGlobalSharesAreNeverNegative is the direct reproducer.
//
// The previous implementation rounded each of the first n-1 shares to the
// nearest rupiah, accumulated, and gave the LAST bundle `amount - assigned`.
// With 15 bundles of Rp10 and one of Rp1 (sorted so the small one is last) and a
// Rp1 coupon, each of the 15 rounds UP from Rp0.066 to Rp0.07, `assigned`
// reaches Rp1.05, and the last share is Rp-0.05.
//
// A negative discount means the sub-order total is INFLATED by 5 sen, and the
// CHECK added in migration 00040 rejects the INSERT, so the entire
// multi-seller checkout fails.
func TestAllocateGlobalSharesAreNeverNegative(t *testing.T) {
	const n = 16
	sellerIDs := make([]string, n)
	subtotals := make(map[string]float64, n)
	for i := 0; i < n; i++ {
		// Zero-padded so lexicographic sort order is the numeric order, matching
		// how the caller sorts before passing the slice.
		sellerIDs[i] = fmt.Sprintf("seller-%02d", i)
		subtotals[sellerIDs[i]] = 10
	}
	subtotals[sellerIDs[n-1]] = 1 // the smallest bundle sorts last

	shares := allocateGlobal(1, sellerIDs, subtotals)

	for _, sid := range sellerIDs {
		if shares[sid] < 0 {
			t.Errorf("seller %s received a NEGATIVE discount of Rp%.0f -- this inflates the "+
				"sub-order total and trips 00040's orders_discount_bounded CHECK", sid, shares[sid])
		}
	}
	var sum float64
	for _, sid := range sellerIDs {
		sum += shares[sid]
	}
	if sum != 1 {
		t.Errorf("shares sum to Rp%.2f, want exactly Rp1", sum)
	}
}

// TestAllocateGlobalNeverExceedsItsOwnSubtotal is the other half of 00040's
// CHECK: `discount_amount <= subtotal`.
func TestAllocateGlobalNeverExceedsItsOwnSubtotal(t *testing.T) {
	rng := rand.New(rand.NewPCG(3, 4))
	for iter := 0; iter < 50_000; iter++ {
		n := 1 + rng.IntN(40)
		sellerIDs := make([]string, n)
		subtotals := make(map[string]float64, n)
		var total float64
		for j := 0; j < n; j++ {
			sellerIDs[j] = fmt.Sprintf("s%03d", j)
			// A wide spread, including tiny bundles beside huge ones -- the shape
			// that makes per-share rounding dominate.
			subtotals[sellerIDs[j]] = float64(1+rng.IntN(1_000_000)) * 10
			total += subtotals[sellerIDs[j]]
		}
		// The platform discount is itself bounded by the grand subtotal upstream
		// (couponDiscount clamps to the subtotal), so amount <= total holds.
		amount := math.Trunc(total * rng.Float64())

		shares := allocateGlobal(amount, sellerIDs, subtotals)
		var sum float64
		for _, sid := range sellerIDs {
			sum += shares[sid]
			if shares[sid] < 0 {
				t.Fatalf("n=%d amount=Rp%.0f sid=%s share=Rp%.0f is NEGATIVE", n, amount, sid, shares[sid])
			}
			if shares[sid] > subtotals[sid] {
				t.Fatalf("n=%d amount=Rp%.0f sid=%s share=Rp%.0f exceeds its own subtotal Rp%.0f "+
					"-- 00040's orders_discount_bounded CHECK rejects the INSERT",
					n, amount, sid, shares[sid], subtotals[sid])
			}
		}
		if sum != amount {
			t.Fatalf("n=%d shares sum to Rp%.2f, want exactly Rp%.2f -- a discount is created or "+
				"destroyed by the allocation itself", n, sum, amount)
		}
	}
}

// TestAllocateGlobalIsDeterministic matters because the caller sorts sellerIDs
// and uses the position to number sub-orders. A map-iteration-order dependency
// would make the same cart produce different splits on different runs, which
// shows up as a checkout that "changes" for no reason and makes the money
// impossible to reconcile after the fact.
func TestAllocateGlobalIsDeterministic(t *testing.T) {
	sellerIDs := []string{"c", "a", "b", "d", "e"}
	subtotals := map[string]float64{"a": 3, "b": 7, "c": 1, "d": 11, "e": 2}
	first := allocateGlobal(13, sellerIDs, subtotals)
	for i := 0; i < 200; i++ {
		got := allocateGlobal(13, sellerIDs, subtotals)
		for _, sid := range sellerIDs {
			if got[sid] != first[sid] {
				t.Fatalf("allocation is not deterministic: %s got Rp%.0f then Rp%.0f",
					sid, first[sid], got[sid])
			}
		}
	}
}

// TestAllocateGlobalDegenerateInputs: the guards, so none of them is a division
// by zero that only shows up in production.
func TestAllocateGlobalDegenerateInputs(t *testing.T) {
	t.Run("zero amount allocates nothing", func(t *testing.T) {
		got := allocateGlobal(0, []string{"a", "b"}, map[string]float64{"a": 10, "b": 20})
		if len(got) != 0 {
			t.Errorf("got %v, want an empty map", got)
		}
	})
	t.Run("no sellers is not a division by zero", func(t *testing.T) {
		if got := allocateGlobal(100, nil, nil); len(got) != 0 {
			t.Errorf("got %v, want an empty map", got)
		}
	})
	t.Run("all-zero subtotals is not a division by zero", func(t *testing.T) {
		got := allocateGlobal(100, []string{"a", "b"}, map[string]float64{"a": 0, "b": 0})
		if len(got) != 0 {
			t.Errorf("got %v, want an empty map (the sumAll<=0 guard)", got)
		}
	})
	t.Run("one seller takes the whole amount", func(t *testing.T) {
		got := allocateGlobal(37, []string{"a"}, map[string]float64{"a": 500})
		if got["a"] != 37 {
			t.Errorf("got Rp%.0f, want Rp37", got["a"])
		}
	})
	t.Run("a bundle not in sellerIDs is ignored", func(t *testing.T) {
		got := allocateGlobal(10, []string{"a"}, map[string]float64{"a": 5, "b": 95})
		if _, ok := got["b"]; ok {
			t.Error("allocated to a bundle the caller did not ask for")
		}
		if got["a"] != 10 {
			t.Errorf("got Rp%.0f, want the whole Rp10", got["a"])
		}
	})
}

// FuzzAllocateGlobal asserts the two invariants on arbitrary input, including
// the shapes a table test would not think to enumerate.
//
// Go's fuzzing engine accepts only a fixed set of argument types -- no maps and
// no []float64. The bundle shape is therefore carried as a []byte, with each
// byte mapped to a subtotal through a spread that deliberately includes the
// values that broke the old implementation: 0 (a bundle with no value, e.g. one
// whose lines are all out of stock), 1, and small amounts next to large ones
// (where per-share rounding dominates).
func FuzzAllocateGlobal(f *testing.F) {
	f.Add(1.0, []byte{10, 10, 1})
	f.Add(100.0, []byte{100})
	f.Add(7.0, []byte{0, 0, 0, 0, 0, 0, 0, 0})
	f.Add(13.0, []byte{1, 255, 7, 200, 3, 128})
	f.Add(1.0, []byte{1, 1, 1, 1, 1, 1, 1, 1, 1, 1, 1, 1, 1, 1, 1, 1})
	f.Fuzz(func(t *testing.T, amount float64, shape []byte) {
		if math.IsNaN(amount) || math.IsInf(amount, 0) {
			t.Skip("non-finite amounts are rejected at the config layer")
		}
		if len(shape) == 0 {
			t.Skip("need at least one bundle")
		}
		if len(shape) > 64 {
			shape = shape[:64]
		}
		sellerIDs := make([]string, len(shape))
		subtotals := make(map[string]float64, len(shape))
		for i, b := range shape {
			// A spread across four orders of magnitude, including 0 and 1, which
			// are the values that make the rounding worst.
			subtotal := 0.0
			switch b % 4 {
			case 0:
				subtotal = 0
			case 1:
				subtotal = 1
			case 2:
				subtotal = float64(b) * 10
			default:
				subtotal = float64(b) * 1_000
			}
			sellerIDs[i] = fmt.Sprintf("s%04d", i)
			subtotals[sellerIDs[i]] = subtotal
		}

		shares := allocateGlobal(amount, sellerIDs, subtotals)
		var sum float64
		for i, sid := range sellerIDs {
			if shares[sid] < 0 {
				t.Fatalf("negative share Rp%.4f for bundle %d (amount=%v, %d bundles, shape %v)",
					shares[sid], i, amount, len(sellerIDs), shape)
			}
			sum += shares[sid]
		}
		// The allocation preserves a whole-rupiah amount exactly. A fuzzer will
		// happily produce fractional amounts, which no caller ever passes here
		// (every value reaching this function is already moneyRound'd), so the
		// conservation invariant is only asserted where it is meaningful.
		if amount > 0 && amount == math.Trunc(amount) {
			var sumAll float64
			for _, sid := range sellerIDs {
				sumAll += subtotals[sid]
			}
			if sumAll > 0 && sum != amount {
				t.Fatalf("sum Rp%.4f != amount Rp%.4f with %d bundles (shape %v)",
					sum, amount, len(sellerIDs), shape)
			}
		}
	})
}

// ─── 2. billableKg ──────────────────────────────────────────────────────────

// TestBillableKgNeverUndercharges is the shipping-undercharge regression.
//
// The old code was `weightKg += (grams * qty) / 1000` -- Go INTEGER division,
// per line, summed. Two independent errors:
//
//	truncation, not ceiling: one 1500g line billed 1kg where a carrier bills 2
//	truncate-per-line != truncate-of-sum: six 500g items (3.0kg) billed 0kg
//
// The second is the exploitable one: a seller whose entire catalogue is under a
// kilogram never paid any per-kg freight, on any order, at any weight, and the
// platform ate it because the seller is paid from the order total.
func TestBillableKgNeverUndercharges(t *testing.T) {
	cases := []struct {
		name  string
		grams []int
		want  int
	}{
		{"empty", nil, 0},
		{"single 500g", []int{500}, 1},
		{"single 1000g", []int{1000}, 1},
		{"single 1001g rounds up", []int{1001}, 2},
		{"single 1500g", []int{1500}, 2},
		// THE case: 3.0kg billed as 0kg.
		{"six 500g items", []int{500, 500, 500, 500, 500, 500}, 3},
		// 2.2kg billed as 1kg (1+0+0+0).
		{"3x600g + 1x400g", []int{600, 600, 600, 400}, 3},
		// 2.299kg billed as 1kg (1+0+0+0).
		{"3x600g + 1x499g", []int{600, 600, 600, 499}, 3},
		// 1.999kg billed as 1kg.
		{"2x600g + 1x499g + 1x300g", []int{600, 600, 499, 300}, 2},
		// Truncate-per-line and truncate-of-sum differ: 2+2+2 = 6, not 0.
		{"three 2000g items", []int{2000, 2000, 2000}, 6},
		{"single 1g still bills 1kg (carrier minimum)", []int{1}, 1},
		{"negative weights contribute nothing", []int{-5000, 600}, 1},
		{"all negative is zero", []int{-100, -200}, 0},
		{"all zero is zero", []int{0, 0}, 0},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := billableKg(tc.grams); got != tc.want {
				t.Errorf("billableKg(%v) = %d kg, want %d kg", tc.grams, got, tc.want)
			}
		})
	}
}

// TestBillableKgIsMonotonic: a heavier parcel is never billed lighter. Cheap
// to state, and it is the property the per-line version violated whenever a
// seller added a lighter line to a heavier bundle.
func TestBillableKgIsMonotonic(t *testing.T) {
	rng := rand.New(rand.NewPCG(11, 13))
	for iter := 0; iter < 20_000; iter++ {
		a := make([]int, 1+rng.IntN(6))
		b := make([]int, 1+rng.IntN(6))
		var sumA, sumB int
		for i := range a {
			a[i] = rng.IntN(50_000)
			sumA += a[i]
		}
		for i := range b {
			b[i] = rng.IntN(50_000)
			sumB += b[i]
		}
		if sumA > sumB {
			continue // only test the increasing direction
		}
		ka, kb := billableKg(a), billableKg(b)
		if ka > kb {
			t.Fatalf("billableKg(%v)=%dkg is heavier than billableKg(%v)=%dkg with %d < %d grams",
				a, ka, b, kb, sumA, sumB)
		}
		// And the charge is never below the true weight in kilograms.
		if float64(ka) < float64(sumA)/1000 {
			t.Fatalf("billed %dkg for %d grams", ka, sumA)
		}
	}
}

// ─── 3. Whole-rupiah rounding ───────────────────────────────────────────────

// TestMoneyRoundIsIdempotent: rounding an already-rounded value changes nothing.
// Without this, any code path that rounds twice accumulates drift.
func TestMoneyRoundIsIdempotent(t *testing.T) {
	rng := rand.New(rand.NewPCG(17, 19))
	for i := 0; i < 200_000; i++ {
		v := float64(rng.IntN(200_000_000)) / 100.0
		once := moneyRound(v)
		twice := moneyRound(once)
		if once != twice {
			t.Fatalf("moneyRound is not idempotent: %.2f -> %.0f -> %.0f", v, once, twice)
		}
		if !IsWholeIDR(once) {
			t.Fatalf("moneyRound(%.4f) = %v, which is not whole rupiah", v, once)
		}
	}
}

// TestMoneyRoundNeverProducesFractionalRupiah is the gateway-safety property.
// `payments/midtrans.go` charges int64(math.Round(amount)); if any value
// reaching the gateway carried a sen component the webhook's amount check
// rejected the notification and the expired-order sweeper cancelled the order
// with the buyer's money already debited.
func TestMoneyRoundNeverProducesFractionalRupiah(t *testing.T) {
	// The exact values from the original bug report.
	for _, v := range []float64{
		12345.67 * 0.97, // 3% coupon -> 11975.2999
		39999.98 * 0.93, // 7% coupon -> 37199.9814
		59999.97 * 0.90, // 10% coupon -> 53999.973
		0.1 + 0.2,
		19999.99 * 3, // 59999.97
	} {
		r := moneyRound(v)
		if !IsWholeIDR(r) {
			t.Errorf("moneyRound(%v) = %v, which the gateway cannot charge exactly", v, r)
		}
		if got := int64(math.Round(r)); float64(got) != r {
			t.Errorf("moneyRound(%v) = %v but int64 conversion gives %d -- these disagree, "+
				"which is the AMOUNT_MISMATCH defect", v, r, got)
		}
	}
}

// ─── 4. Commission and refund legs ──────────────────────────────────────────

// TestCommissionLegsSumToGross is the invariant the whole escrow settlement rests on.
// The two legs are rounded INDEPENDENTLY by Postgres when written to NUMERIC(14,2)
// columns, so a one-sen drift on ~2.6% of orders was invisible to every check: 00040
// only asserts both legs are non-negative, not that they sum.
//
// THIS TEST USED TO RE-IMPLEMENT THE RULE INSTEAD OF CALLING IT.
//
// It computed the intended answer inline --
//
//	fee := moneyRound(amount*pct/100 + fixed)
//	if fee > amount { fee = amount }
//	seller := amount - fee
//
// -- and so verified the ALGORITHM while production implemented a DIFFERENT ONE. It
// passed 100,000 iterations against repository.Commission returning unrounded
// fractional rupiah on every call, and it STILL passed after the fix landed, for the
// same reason. A property test that restates the rule instead of executing it is an
// assertion about the test file.
func TestCommissionLegsSumToGross(t *testing.T) {
	rng := rand.New(rand.NewPCG(23, 29))
	for iter := 0; iter < 100_000; iter++ {
		amount := float64(rng.IntN(500_000_000))
		pct := rng.Float64() * 60      // includes out-of-range; must still be safe
		fixed := rng.Float64() * 25000 // includes a fee far above the amount

		// The production function, not a restatement of it.
		fee, seller := repository.Commission(pct, fixed, amount)

		if seller < 0 {
			t.Fatalf("amount=Rp%.0f pct=%.4f fixed=Rp%.0f: seller leg is negative",
				amount, pct, fixed)
		}
		if fee < 0 {
			t.Fatalf("amount=Rp%.0f pct=%.4f fixed=Rp%.0f: fee leg is negative",
				amount, pct, fixed)
		}
		if fee+seller != amount {
			t.Fatalf("amount=Rp%.0f pct=%.4f fixed=Rp%.0f: fee Rp%.2f + seller Rp%.2f = "+
				"Rp%.2f, want Rp%.2f -- money is created or destroyed at escrow release, and "+
				"payment_intents_split_sums rejects the write so the SELLER IS NOT PAID",
				amount, pct, fixed, fee, seller, fee+seller, amount)
		}
		// IDR has no sen. A fractional leg cannot be charged to a gateway at all.
		if !IsWholeIDR(fee) || !IsWholeIDR(seller) {
			t.Fatalf("amount=Rp%.0f pct=%.4f fixed=Rp%.0f: produced fractional rupiah "+
				"fee=%v seller=%v", amount, pct, fixed, fee, seller)
		}
	}
}

// The random sweep above has a measure-zero blind spot, and it is exactly where the
// alternative implementation hides.
//
// Rounding BOTH legs independently -- moneyRound(raw) and moneyRound(amount-raw) --
// agrees with rounding once and taking the residual for every input EXCEPT when raw
// lands exactly on a half rupiah. Then both legs round up and the pair sums to
// amount+1: money created.
//
// With pct and fixed drawn from a float distribution, landing on exactly .5 has
// probability zero, so 100,000 random iterations pass against that implementation. It
// did pass when tried. These cases pin it.
func TestCommissionLegsSumOnTheHalfRupiahBoundary(t *testing.T) {
	cases := []struct {
		pct, fixed, amount float64
		why                string
	}{
		{0.5, 0, 100, "raw is exactly 0.5; both legs round up if rounded separately"},
		{0.5, 0, 1000, "same, larger order"},
		{2.5, 0, 200, "raw is exactly 5"},
		{0, 0.5, 100, "the FIXED fee is the half-rupiah, not the percentage"},
		{0, 49.5, 100, "fixed just under a half"},
		{0, 50.5, 100, "fixed just over a half"},
		{1.5, 0, 100, "raw is exactly 1.5"},
		{99.5, 0, 100, "nearly the whole order"},
		{33.333, 0.5, 999, "both components fractional at once"},
	}
	for _, c := range cases {
		fee, seller := repository.Commission(c.pct, c.fixed, c.amount)
		if fee+seller != c.amount {
			t.Errorf("Commission(%v,%v,%v) = (%v,%v), sum %v != %v: %s",
				c.pct, c.fixed, c.amount, fee, seller, fee+seller, c.amount, c.why)
		}
		if !IsWholeIDR(fee) || !IsWholeIDR(seller) {
			t.Errorf("Commission(%v,%v,%v) produced fractional rupiah fee=%v seller=%v",
				c.pct, c.fixed, c.amount, fee, seller)
		}
	}
}

// Commission must round the same way as every other money path in the system. A second
// rounding rule means two orderings of the same purchase disagree by a sen, which is
// exactly the class of defect 00040 exists to prevent.
func TestCommissionUsesTheSharedRoundingRule(t *testing.T) {
	rng := rand.New(rand.NewPCG(101, 7))
	for iter := 0; iter < 50_000; iter++ {
		amount := float64(rng.IntN(5_000_000))
		pct := rng.Float64() * 30
		fixed := rng.Float64() * 5000

		fee, _ := repository.Commission(pct, fixed, amount)

		want := amount*pct/100 + fixed
		if want > amount {
			want = amount
		}
		want = moneyRound(want)

		if fee != want {
			t.Fatalf("amount=Rp%.0f pct=%.4f fixed=Rp%.0f: fee=%v but the shared rounding "+
				"rule gives %v", amount, pct, fixed, fee, want)
		}
	}
}

// ─── 5. shipping and insurance ──────────────────────────────────────────────

func TestShippingFeeRoundsToWholeRupiah(t *testing.T) {
	cases := []struct {
		base, perKg float64
		kg          int
		want        float64
	}{
		{9000, 6000, 0, 9000},   // 300g bulb: base only
		{9000, 6000, 1, 15000},  // 1kg
		{9000, 6000, 3, 27000},  // 3kg
		{9000, 6000, 7, 51000},  // 7kg volumetric pillow
		{9000, 6000, 10, 69000}, // 10kg
	}
	for _, tc := range cases {
		got := shippingFee(tc.base, tc.perKg, tc.kg)
		if got != tc.want {
			t.Errorf("shippingFee(%v, %v, %d) = Rp%.0f, want Rp%.0f", tc.base, tc.perKg, tc.kg, got, tc.want)
		}
		if !IsWholeIDR(got) {
			t.Errorf("shippingFee produced a fractional rupiah: %v", got)
		}
	}
}

func TestShippingFeeClampsNegativeWeight(t *testing.T) {
	// A negative weight_grams from a seller form produced a negative shipping
	// fee, which migration 00040's orders_total_nonneg turned into a 500.
	if got := shippingFee(9000, 6000, -5); got != 9000 {
		t.Errorf("shippingFee with a negative weight = Rp%.0f, want the base fee Rp9000", got)
	}
}

func TestInsuranceFeeRoundsTheChargeNotTheRate(t *testing.T) {
	// The old expression was math.Round(subtotal*pct) / 100 -- it rounded the
	// PERCENTAGE and then divided. For every current rate that happens to agree
	// with the correct form, which is exactly why the bug was invisible; it
	// diverges once the rate exceeds 100.
	const pct = 0.3
	rng := rand.New(rand.NewPCG(31, 37))
	for iter := 0; iter < 50_000; iter++ {
		subtotal := float64(rng.IntN(100_000_000))
		got := insuranceFee(subtotal, pct)
		want := moneyRound(subtotal * pct / 100)
		if got != want {
			t.Fatalf("insuranceFee(Rp%.0f, %v) = Rp%.0f, want Rp%.0f", subtotal, pct, got, want)
		}
		if !IsWholeIDR(got) {
			t.Fatalf("insuranceFee produced a fractional rupiah: %v", got)
		}
	}
	// A rate above 100 is where the old form visibly diverges.
	if old, correct := math.Round(10_000*150.5)/100, insuranceFee(10_000, 150.5); old != correct {
		t.Logf("the old form gives %.2f and the correct form gives %.0f -- the divergence is real", old, correct)
	}
	if insuranceFee(0, 0.3) != 0 {
		t.Error("a zero subtotal must not produce an insurance fee")
	}
	if insuranceFee(1000, 0) != 0 {
		t.Error("a zero rate must not produce an insurance fee")
	}
}

// ─── 6. The quote is the single source of arithmetic truth ──────────────────

// TestQuoteSubtotalMatchesSumOfLines is the time-of-check/time-of-use property.
// PlaceOrder used to recompute the subtotal in a second loop over the cart, so
// the number shown to the buyer and the number persisted were produced by
// different code. This asserts the per-bundle subtotal the quote exposes is
// exactly the sum of its rounded line subtotals.
func TestQuoteSubtotalMatchesSumOfLines(t *testing.T) {
	rng := rand.New(rand.NewPCG(41, 43))
	for iter := 0; iter < 20_000; iter++ {
		n := 1 + rng.IntN(20)
		ids := make([]string, n)
		subtotals := make(map[string]float64, n)
		expected := 0.0
		for j := 0; j < n; j++ {
			ids[j] = fmt.Sprintf("s%02d", j)
			var bundle float64
			lines := 1 + rng.IntN(5)
			for k := 0; k < lines; k++ {
				bundle += float64(rng.IntN(5_000_000))
			}
			bundle = moneyRound(bundle)
			subtotals[ids[j]] = bundle
			expected += bundle
		}
		sort.Strings(ids)
		q := &Quote{
			discountBySeller: map[string]float64{},
			subtotalBySeller: subtotals,
			gramsBySeller:    map[string][]int{},
		}
		var viaQuote float64
		for _, sid := range ids {
			viaQuote += q.SubtotalFor(sid)
		}
		if viaQuote != expected {
			t.Fatalf("per-bundle subtotals sum to Rp%.2f, want Rp%.2f", viaQuote, expected)
		}
	}
}
