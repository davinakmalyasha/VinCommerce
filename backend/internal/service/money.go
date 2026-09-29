package service

import (
	"math"
	"sort"
)

// This file holds the money arithmetic of the checkout engine as pure
// functions with no database, no context, and no service receiver.
//
// Three separate defects motivated the extraction, and all three are the same
// shape: a float64 amount divided, rounded, and reassembled in a way that does
// not survive arithmetic identity.
//
//  1. allocateGlobal gave the remainder to the last seller after rounding every
//     other share INDEPENDENTLY. The accumulated up-rounding can exceed the
//     amount, so the last share goes negative -- which inflates that order's
//     total AND trips migration 00040's `CHECK (discount_amount >= 0 AND
//     discount_amount <= subtotal)`, failing the entire multi-seller checkout
//     with a raw Postgres error surfaced as a 500.
//  2. billableKg truncated per line and then summed. Three 600g items plus one
//     400g item is 2.2kg, but computed 1+0 = 1kg. Six 500g items is 3.0kg,
//     computed as 0kg -- free shipping on any order from a seller whose entire
//     catalogue is under a kilogram.
//  3. The insurance fee rounded the PERCENTAGE and then divided, rather than
//     dividing and then rounding. They agree for the current rates by luck of
//     float representation, and diverge for any rate where the intermediate
//     exceeds 1.
//
// The reason they were hard to see is that they were not functions. Each was a
// closure or an inline loop inside a 1200-line method that also opened
// transactions and sent email, so there was no seam to test, and no amount of
// reading the code would have surfaced the failing input. These are now
// individually testable, and money_property_test.go asserts the invariants that
// each one violates.

// moneyRound rounds to whole rupiah.
//
// IDR has no circulating subunit: the smallest coin is Rp100 and amounts are
// quoted in whole rupiah. Rounding to 2dp leaves a representation the currency
// does not have, and that representation is where the gateway boundary breaks
// (see RoundIDR and the note on gatewayAmount).
//
// This function is the single rounding point for the checkout engine. It is not
// a substitute for decimal arithmetic -- that is tracked separately -- but it
// does mean every share, fee and subtotal is on a representable grid before it
// is summed, which removes the most common source of drift.
func moneyRound(v float64) float64 { return math.Round(v) }

// RoundIDR rounds to the nearest whole rupiah, half away from zero.
//
// IDR has no sen. The order total is stored as NUMERIC(14,2) -- which CAN carry
// sen -- and the payment gateway is charged int64(math.Round(total)). A 3%
// coupon on a Rp12,345.67 cart produced a stored total of Rp11,975.30 and a
// Midtrans charge of Rp11,975. The webhook then compared the two with a
// tolerance of 1 sen, rejected the notification as AMOUNT_MISMATCH, and the
// order was cancelled at T+30min by the expired-order sweeper with the cash
// already debited from the buyer's account and settled to the platform's bank.
//
// So the rounding has to happen at WRITE time, not at the gateway boundary:
// moneyRound is applied to every share, subtotal, discount and fee as it is
// computed, and a total with a sen component is a bug rather than a value.
func RoundIDR(v float64) float64 { return math.Round(v) }

// IsWholeIDR reports whether a value is exactly representable in whole rupiah.
// Used to assert at the boundary that no sen component reaches the gateway.
func IsWholeIDR(v float64) bool { return v == math.Trunc(v) }

// absDiff is the absolute difference between two amounts.
//
// With whole-rupiah money the epsilon comparisons this used to guard --
// `absDiff(x, y) > 0.01`, `absDiff(amount, total) > 1.0` -- are no longer
// load-bearing, and one of them was a vulnerability rather than a rounding
// accommodation: `> 1.0` on the external-payment confirmation let a buyer mark
// a Rp1,000,000 order paid by reporting 999,999.00, and on a 100%-discount
// order the minimum accepted claim was Rp0.01.
//
// It lives here rather than in payment_service.go so that every money helper is
// in one reviewable file, and it stays only for the places that compare a
// RECOMPUTED total against a STORED one, where a tolerance is honest. Everywhere
// else the comparison should be exact equality.
func absDiff(a, b float64) float64 { return math.Abs(a - b) }

// allocateGlobal distributes a platform-level discount across seller bundles in
// proportion to their subtotal.
//
// It uses the LARGEST-REMAINDER method: every bundle gets the floor of its exact
// proportional share, and the leftover rupiah are then handed out one at a time
// in a deterministic order (largest fractional part first, ties broken by the
// caller's sort order). That guarantees three properties the previous
// implementation did not have:
//
//	sum(shares) == amount          exactly, by construction
//	every share >= 0              because floors are never negative when the
//	                              amount and the subtotals are non-negative
//	every share <= its subtotal   because the total is allocated proportionally
//
// The previous version rounded each non-final share to the nearest rupiah,
// accumulated, and gave the last bundle `amount - assigned`. With 16 sellers
// (15 bundles of Rp10 and one of Rp1) and a Rp1 coupon, fifteen shares each
// rounded UP to Rp0.07, `assigned` reached Rp1.05, and the last share was
// Rp-0.05. That is a negative discount: the order total for that seller is
// inflated by 5 sen, and the CHECK added in migration 00040 rejects the INSERT,
// so the whole multi-seller checkout fails.
//
// A nil map for sellerIDs is a no-op rather than a division by zero.
func allocateGlobal(amount float64, sellerIDs []string, bundleSubtotals map[string]float64) map[string]float64 {
	shares := map[string]float64{}
	if amount <= 0 || len(sellerIDs) == 0 {
		return shares
	}

	// Exact proportional allocation as a float, then floor.
	exact := make([]float64, len(sellerIDs))
	var sumAll float64
	for _, sid := range sellerIDs {
		sumAll += bundleSubtotals[sid]
	}
	if sumAll <= 0 {
		return shares
	}
	for i, sid := range sellerIDs {
		exact[i] = amount * bundleSubtotals[sid] / sumAll
	}
	// Floor each share, accumulate the shortfall in whole rupiah.
	var assigned float64
	type share struct {
		idx   int
		frac  float64
		total float64
	}
	order := make([]share, 0, len(sellerIDs))
	for i := range exact {
		f := math.Floor(exact[i])
		assigned += f
		order = append(order, share{idx: i, frac: exact[i] - f, total: f})
	}
	// Descending fractional part; ties resolved by index so the result is stable
	// across runs regardless of Go's map iteration order (sellerIDs is sorted by
	// the caller, which is what makes the sub-order numbering deterministic).
	sort.SliceStable(order, func(a, b int) bool {
		if order[a].frac != order[b].frac {
			return order[a].frac > order[b].frac
		}
		return order[a].idx < order[b].idx
	})

	// Hand out the leftover rupiah, one per bundle, largest remainder first.
	// The loop is bounded: there can be at most len(sellerIDs)-1 leftover units,
	// and it stops early if it has distributed them all.
	leftover := int(math.Round(amount - assigned))
	if leftover > 0 {
		for i := 0; i < len(order) && leftover > 0; i++ {
			order[i].total++
			leftover--
		}
	} else if leftover < 0 {
		// Cannot happen when amount >= 0 and subtotals >= 0 (the floors cannot
		// exceed the exact shares, whose sum is `amount`). Clamped rather than
		// asserted because a negative share is a money bug and must not be able
		// to become a negative order total at runtime.
		for i := len(order) - 1; leftover < 0; i-- {
			if i < 0 {
				break
			}
			if order[i].total > 0 {
				order[i].total--
				leftover++
			}
		}
	}

	// Reassemble by the recorded index, NOT by position.
	//
	// `order` was just re-sorted by fractional part, so `order[i]` no longer
	// corresponds to `sellerIDs[i]`. Reading it positionally hands one bundle
	// another's share -- which is how a bundle with a Rp4.6m subtotal ended up
	// carrying a Rp5.0m discount, tripping 00040's `discount_amount <=
	// subtotal` CHECK and failing the checkout. The property test
	// TestAllocateGlobalNeverExceedsItsOwnSubtotal caught this on its first run,
	// which is the argument for writing the property before trusting the
	// implementation.
	byIndex := make([]float64, len(order))
	for _, s := range order {
		byIndex[s.idx] = s.total
	}
	for i, sid := range sellerIDs {
		shares[sid] += byIndex[i]
	}
	return shares
}

// billableKg converts a seller's bundle to billable whole kilograms.
//
// It accumulates GRAMS and rounds up ONCE, at the end. The previous
// implementation divided per line and summed, which is wrong twice:
//
//	truncation, not ceiling -- one 1500g line is billed 1kg where a carrier
//	  bills 2
//	truncate-per-line != truncate-of-sum -- two 600g lines are billed 0kg
//	  where the parcel is 1.2kg
//
// A seller whose entire catalogue is under a kilogram therefore never paid any
// per-kg freight at all, on any order, at any weight. The platform ate it,
// because the seller is paid from the order total.
//
// ceil rather than round matches how every Indonesian carrier bills (JNE, J&T,
// SiCepat all round the chargeable weight up to the next whole kilogram, with a
// 1kg minimum). The volumetric calculation that sits alongside this belongs
// with the carrier integration; what is fixed here is that the actual weight is
// no longer undercounted.
//
// Negative or zero weights contribute nothing. A seller can set
// weight_grams to a negative value today -- there is no CHECK on the column and
// no validation in CreateProduct or UpdateProduct -- and a negative total
// produced a negative shipping fee, which migration 00040's
// `orders_total_nonneg` turned into a checkout 500.
func billableKg(gramsPerLine []int) int {
	total := 0
	for _, g := range gramsPerLine {
		if g > 0 {
			total += g
		}
	}
	if total <= 0 {
		return 0
	}
	return (total + 999) / 1000
}

// shippingFee is the cost of shipping a bundle.
//
// Kept as its own function so the free-shipping threshold, the base fee and the
// per-kilogram rate are applied in one reviewed place, and so the fee is
// rounded to whole rupiah like every other money value.
func shippingFee(baseFee, perKgFee float64, kg int) float64 {
	if kg < 0 {
		kg = 0
	}
	return moneyRound(baseFee + float64(kg)*perKgFee)
}

// insuranceFee is the optional shipping-insurance charge for a subtotal.
//
// The previous expression was `math.Round(subtotal*pct) / 100` -- it rounded the
// PERCENTAGE and then divided. It happens to agree with the correct
// `round(subtotal*pct/100)` for every current rate, because the intermediate is
// small enough that the division is correctly rounded. It diverges the moment
// the rate exceeds 100, and it is confusing enough that the next person to
// touch it would introduce the bug. Written the correct way round.
func insuranceFee(subtotal, pct float64) float64 {
	if pct <= 0 || subtotal <= 0 {
		return 0
	}
	return moneyRound(subtotal * pct / 100)
}
