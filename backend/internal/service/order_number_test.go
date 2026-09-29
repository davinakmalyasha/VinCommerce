package service

import (
	"regexp"
	"strings"
	"testing"
	"time"
)

// orderNumberShape is the exact format newOrderNumber must produce.
var orderNumberShape = regexp.MustCompile(`^VC-\d{8}-[0-9ABCDEFGHJKMNPQRSTVWXYZ]{8}$`)

// TestOrderNumberIsNotSequential is the regression test for the tracking
// enumeration.
//
// `order_number` used to be built as `VC-YYYYMMDD-%04d` over
// `nextval('order_number_seq')`, a global monotonic counter declared in
// migration 00004 with `START 1000`. That number is the identifier a buyer types
// into `GET /orders/tracking/{number}`, and the handler on the other end had no
// ownership check -- so the two defects compounded into a loop over four digits
// that walked every order the platform had ever created, returning the buyer's
// UUID, the seller's UUID, the full money breakdown, the payment status, the
// coupon code, the seller's display name, and the complete event timeline
// (which carries the escrow-release and refund notes).
//
// The day prefix is NOT a mitigation and this test is why: it narrows the range
// rather than hiding it. If the suffix were still a counter, the per-day
// sequence would restart in a guessable way.
func TestOrderNumberIsNotSequential(t *testing.T) {
	const n = 5000
	seen := make(map[string]bool, n)
	dayPrefixes := map[string]int{}

	for i := 0; i < n; i++ {
		num, err := newOrderNumber(time.Now())
		if err != nil {
			t.Fatalf("newOrderNumber: %v", err)
		}
		if !orderNumberShape.MatchString(num) {
			t.Fatalf("order number %q does not match VC-YYYYMMDD-XXXXXXXX", num)
		}
		if seen[num] {
			t.Fatalf("order number %q collided after %d draws", num, i)
		}
		seen[num] = true
		dayPrefixes[num[:11]]++
	}

	// The suffix is the security property. 5 random bytes = 40 bits. Over 5000
	// draws the chance of ANY collision is ~1e-8, so a collision here is a real
	// signal that the entropy was removed rather than flake. (A sequential
	// generator never collides, so this assertion does not catch a regression
	// to sequential on its own -- TestOrderNumberSuffixCarriesEntropy does.)
	if len(dayPrefixes) < 1 {
		t.Fatalf("no day prefix was produced")
	}
}

// TestOrderNumberSuffixCarriesEntropy asserts the suffix is not a function of
// the call index, which is the actual property that was violated.
func TestOrderNumberSuffixCarriesEntropy(t *testing.T) {
	// Freeze the clock: with a fixed time, a date+counter generator would
	// produce a perfectly predictable sequence. Any implementation that derives
	// the number from anything but entropy fails this.
	fixed := time.Date(2026, 9, 29, 12, 0, 0, 0, time.UTC)

	suffixes := make(map[string]bool, 2000)
	// Per-position symbol sets. Every position must reach the full 32-symbol
	// alphabet: a position that only ever emits 8 distinct values means the
	// bit-packing is uneven even when the total entropy is correct.
	perPosition := make([]map[byte]bool, 8)
	for i := range perPosition {
		perPosition[i] = map[byte]bool{}
	}
	var prev string
	for i := 0; i < 2000; i++ {
		num, err := newOrderNumber(fixed)
		if err != nil {
			t.Fatalf("newOrderNumber: %v", err)
		}
		if !strings.HasPrefix(num, "VC-20260929-") {
			t.Fatalf("expected the frozen date in the prefix, got %q", num)
		}
		suffix := num[len("VC-20260929-"):]
		if len(suffix) != 8 {
			t.Fatalf("suffix %q is %d characters, want 8 (5 bytes = 40 bits = 8 x base32)", suffix, len(suffix))
		}
		if suffixes[suffix] {
			t.Fatalf("suffix %q repeated with a frozen clock -- the suffix is not random", suffix)
		}
		suffixes[suffix] = true
		for pos := 0; pos < 8; pos++ {
			perPosition[pos][suffix[pos]] = true
		}
		if i > 0 && suffix == prev {
			t.Fatalf("suffix repeated on consecutive draws: %q", suffix)
		}
		prev = suffix
	}

	// 2000 draws over 32 symbols: the expected distinct count per position is
	// 32*(1-(31/32)^2000) ~= 32, so anything under 30 means that position is
	// not drawing uniformly from the full alphabet.
	for pos, set := range perPosition {
		if len(set) < 30 {
			t.Errorf("suffix position %d used only %d of 32 symbols across 2000 draws -- "+
				"the bit-packing is uneven at that position", pos, len(set))
		}
	}
}

// TestOrderNumberExcludesAmbiguousGlyphs: a support agent reading a number
// aloud, or a buyer retyping one from a screenshot, must not be able to
// produce a different valid-looking number. Crockford base32 drops I, L, O, U.
func TestOrderNumberExcludesAmbiguousGlyphs(t *testing.T) {
	// Crockford base32 deliberately omits these; 0 and 1 are retained because
	// unlike I/L/O/U they are unambiguous in a monospace screenshot.
	for _, bad := range []string{"I", "L", "O", "U"} {
		if strings.Contains(orderNumberAlphabet, bad) {
			t.Errorf("orderNumberAlphabet contains the ambiguous glyph %q", bad)
		}
	}
	if len(orderNumberAlphabet) != 32 {
		t.Errorf("orderNumberAlphabet must be exactly 32 symbols, got %d", len(orderNumberAlphabet))
	}
	for i := 0; i < len(orderNumberAlphabet); i++ {
		for j := i + 1; j < len(orderNumberAlphabet); j++ {
			if orderNumberAlphabet[i] == orderNumberAlphabet[j] {
				t.Fatalf("duplicate symbol %q in the alphabet", orderNumberAlphabet[i])
			}
		}
	}
}

// TestOrderNumberIsStableForTheWholeSale is a documentation test for the
// format decision: the date prefix is kept because support needs a date a
// buyer can read, and the entropy is in the suffix. If someone replaces
// newOrderNumber with a date-only number this must fail, not silently reduce
// the entropy to zero.
func TestOrderNumberHasEnoughEntropy(t *testing.T) {
	num, err := newOrderNumber(time.Now())
	if err != nil {
		t.Fatal(err)
	}
	suffix := num[len("VC-"):]
	// strip the 8-digit date
	suffix = suffix[9:]
	if len(suffix) < 8 {
		t.Fatalf("suffix %q is shorter than the 8 characters the format promises", suffix)
	}
	// 8 base32 chars = 40 bits. Assert the constant cannot be quietly reduced.
	if len(orderNumberAlphabet) < 32 {
		t.Fatalf("reducing the alphabet reduces the entropy of every order number")
	}
}
