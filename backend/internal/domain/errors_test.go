package domain

import (
	"errors"
	"fmt"
	"testing"
)

// TestErrorsIsMatchesSentinelNotPointer is the regression test for a guard that
// compiled, type-checked, and was permanently false.
//
// *Error had an Unwrap (returning Cause) but no Is. That made
// `errors.Is(err, domain.ErrNotFound)` degrade to comparing two *pointers*.
// Every sentinel in this package is a package-level value, and no code path can
// return the same pointer -- repositories build a fresh
// `E(KindNotFound, "NO_INTENT", ...)` -- so the expression was false for
// EVERY input, not just some.
//
// The site that mattered was CancelOrder's escrow guard:
//
//	case ierr != nil && !errors.Is(ierr, domain.ErrNotFound): return ierr
//	default: // "genuinely no intent, safe to cancel"
//
// With a permanently-false match, every error took the first arm and the
// `default` arm was unreachable. An order with payment_status='paid' and no
// payment_intents row could therefore never be cancelled -- and an
// externally-paid order creates no intent, so the buyer got a permanent error
// on cancel and the seller's stock stayed reserved forever.
func TestErrorsIsMatchesSentinelNotPointer(t *testing.T) {
	// A freshly-constructed error with the same kind and code as the sentinel.
	// Pointer comparison would say false; the Is method must say true.
	fresh := E(KindNotFound, "NOT_FOUND", "resource not found")
	if !errors.Is(fresh, ErrNotFound) {
		t.Error("errors.Is must match a *Error sentinel by kind and code, not by pointer identity")
	}

	// The sentinel itself, and a value that is not a *Error at all.
	if !errors.Is(ErrNotFound, ErrNotFound) {
		t.Error("a sentinel must match itself")
	}
	if errors.Is(errors.New("some other error"), ErrNotFound) {
		t.Error("a non-domain error must not match a domain sentinel")
	}
}

// TestErrorsIsUnwrapsThroughCause: a wrapped domain error must still be
// matchable, which is what the errors.As-based helpers have always relied on.
func TestErrorsIsUnwrapsThroughCause(t *testing.T) {
	inner := E(KindNotFound, "NOT_FOUND", "resource not found")
	wrapped := Wrap(KindInternal, "QUERY_FAILED", "could not read the order", inner)
	if !errors.Is(wrapped, ErrNotFound) {
		t.Error("a wrapped sentinel must still be matchable through Unwrap")
	}
	// The wrapper's own kind must not be confused with the inner one's.
	if errors.Is(wrapped, ErrEmailTaken) {
		t.Error("a wrapped not-found must not match a conflict sentinel")
	}
}

// TestDomainIsMatchesKindAlone covers the helper CancelOrder actually uses.
//
// `IntentByOrder` reports absence as `E(KindNotFound, "NO_INTENT", ...)`. The
// sentinel's code is "NOT_FOUND". So matching the sentinel can never work for
// that call site no matter how the Is method is written -- the codes genuinely
// differ, and they are describing different things: one is "some resource does
// not exist", the other is "this order has no payment intent".
func TestDomainIsMatchesKindAlone(t *testing.T) {
	noIntent := E(KindNotFound, "NO_INTENT", "no payment intent for order")

	if !Is(noIntent, KindNotFound, "") {
		t.Error("domain.Is(err, KindNotFound, \"\") must match any not-found regardless of code")
	}
	if !Is(noIntent, KindNotFound, "NO_INTENT") {
		t.Error("domain.Is with an explicit code must match exactly")
	}
	if Is(noIntent, KindNotFound, "NOT_FOUND") {
		t.Error("domain.Is with a mismatched code must not match")
	}
	if Is(noIntent, KindConflict, "") {
		t.Error("domain.Is with a mismatched kind must not match")
	}
}

// TestDomainIsFailsClosedOnNonDomainErrors is the property that makes the guard
// worth having: a raw database error must NOT be mistaken for "not found", or
// the escrow guard fails open and a paid order gets cancelled with its money
// already gone.
func TestDomainIsFailsClosedOnNonDomainErrors(t *testing.T) {
	dbErr := errors.New("conn closed")
	if Is(dbErr, KindNotFound, "") {
		t.Error("a non-domain error must never be classified as not-found")
	}
	// And ErrorKindOf must default to the safe kind, not KindNotFound.
	if got := ErrorKindOf(dbErr); got != KindInternal {
		t.Errorf("ErrorKindOf(non-domain) = %q, want %q -- a misclassification here "+
			"would let the escrow guard fail open", got, KindInternal)
	}
	if got := ErrorKindOf(nil); got != KindInternal {
		t.Errorf("ErrorKindOf(nil) = %q, want %q", got, KindInternal)
	}
}

// TestDomainIsUnwraps also documents that a %w-wrapped *Error is classified by
// its own kind, not the wrapper's.
func TestDomainIsUnwraps(t *testing.T) {
	err := fmt.Errorf("loading order: %w", E(KindNotFound, "NOT_FOUND", "nope"))
	if !Is(err, KindNotFound, "") {
		t.Error("domain.Is must see through fmt.Errorf %w wrapping")
	}
	if ErrorKindOf(err) != KindNotFound {
		t.Error("ErrorKindOf must see through wrapping")
	}
}
