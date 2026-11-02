package domain

import (
	"encoding/json"
	"fmt"
	"math"
)

// IDR is an amount of Indonesian rupiah in whole rupiah (no sen).
//
// Money in this codebase is float64 end to end while the database is
// NUMERIC(14,2) with whole-rupiah CHECKs (migration 00060). Every float
// computation must therefore be rounded at its boundary today, and a missed
// boundary is a constraint failure at best and drift at worst. IDR makes the
// invariant structural: an amount of this type cannot carry a fraction, so
// arithmetic on it cannot introduce one.
//
// This type is introduced WITHOUT changing any field yet. Each money area
// (orders, intents, wallets, ledger, catalogue) migrates to it in its own
// commit, with the database CHECKs as the backstop during the transition.
// A premature flag-day flip of ~50 fields would be unreviewable; the CHECKs
// mean a half-migrated tree fails loudly rather than drifting.
type IDR int64

// IDRFromFloat converts a float amount to whole rupiah, half away from zero.
//
// This is the ONLY sanctioned float64 -> IDR path, and it exists for the
// transition: call sites that still compute in float (percentages, gateway
// payloads) round once, here, instead of each inventing its own conversion.
// New code should stay in IDR and never produce a float to convert.
func IDRFromFloat(v float64) IDR { return IDR(math.Round(v)) }

// Float returns the amount as float64 for the boundaries that still need one
// (gateway payloads, legacy scans). Each call site is a remaining migration.
func (m IDR) Float() float64 { return float64(m) }

// Pct takes pct percent of amount, rounded to whole rupiah.
//
// Percentages (commission rates, insurance, coupon percents) are inherently
// fractional, so this is where rounding lives: one place, half away from
// zero, matching what moneyRound did for the same inputs. A negative rate or
// amount yields zero rather than a negative charge.
func (m IDR) Pct(pct float64) IDR {
	if m <= 0 || pct <= 0 {
		return 0
	}
	return IDR(math.Round(float64(m) * pct / 100))
}

// Add returns m + other. Saturating is deliberately NOT implemented: an
// overflowed rupiah total must fail loudly, and int64 holds ~9.2e18 -- nine
// quintillion rupiah -- so saturation would only ever mask a bug.
func (m IDR) Add(other IDR) IDR { return m + other }

// Sub returns m - other, floored at zero. Money owed is never negative; a
// subtraction that would go below zero is a caller error (usually a cap that
// was not checked), and flooring keeps it from becoming a negative charge.
func (m IDR) Sub(other IDR) IDR {
	if other >= m {
		return 0
	}
	return m - other
}

// Abs returns the magnitude.
func (m IDR) Abs() IDR {
	if m < 0 {
		return -m
	}
	return m
}

// String renders whole rupiah without separators, matching how f2 prints
// handler output today. Separators belong to the presentation layer.
func (m IDR) String() string { return fmt.Sprintf("%d", int64(m)) }

// MarshalJSON emits the amount as a JSON number. The storefront's formatIDR
// takes a number, so existing clients parse this unchanged.
func (m IDR) MarshalJSON() ([]byte, error) {
	return []byte(fmt.Sprintf("%d", int64(m))), nil
}

// UnmarshalJSON accepts a JSON number (possibly fractional, from a client
// that still sends floats) and rounds it once, on the way in.
func (m *IDR) UnmarshalJSON(data []byte) error {
	var v float64
	if err := json.Unmarshal(data, &v); err != nil {
		return err
	}
	*m = IDRFromFloat(v)
	return nil
}
