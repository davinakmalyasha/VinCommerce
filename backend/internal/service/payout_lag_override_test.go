package service

import (
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
)

// A per-seller payout lag, and why the release query has to look it up.
//
// `ReleasableReservations` used to bind ONE scalar: `make_interval(days => $1)`
// from the platform default. So `PAYOUT_LAG_DAYS` applied to every seller and a
// per-seller override was impossible without rewriting the query. It is now
// `COALESCE(st.payout_lag_days, $1)` over a join on `stores.owner_id`.
//
// The join direction is the part that fails silently, so it is asserted directly.

// The release query must resolve the lag per seller, falling back to the platform
// default only when the seller has not chosen one.
func TestTheReleaseLagIsPerSellerNotOneScalar(t *testing.T) {
	body := repositoryFunctionSource(t, "ReleasableReservations")

	if !strings.Contains(body, "COALESCE(st.payout_lag_days, $1)") {
		t.Errorf("the release query does not resolve the lag per seller:\n%s\n"+
			"one scalar applies the platform default to everyone, which is what made "+
			"an override impossible", body)
	}
	if strings.Contains(body, "make_interval(days => $1)") {
		t.Errorf("the release query still binds a single lag scalar:\n%s", body)
	}

	// LEFT JOIN, and on owner_id. An INNER JOIN would exclude every seller with no
	// store row -- and that fails SAFE but SILENTLY: nothing is released, ever, and
	// the hold queue just grows. Joining on anything but owner_id would match zero
	// rows for the same reason.
	if !strings.Contains(body, "LEFT JOIN stores st ON st.owner_id = sr.seller_id") {
		t.Errorf("the store join is missing or wrong:\n%s\n"+
			"it must be a LEFT JOIN on owner_id: `seller_reservations.seller_id` "+
			"references users(id) and a store is keyed on its owner, so any other "+
			"join key silently matches nothing and no hold is ever released", body)
	}
}

// The admin surface must exist and must be inside the admin guard. A payout term
// that only an operator may set is the entire security property; exposed anywhere
// else, a seller could set their own lag to 1 day and withdraw money a return is
// about to reverse.
func TestThePayoutLagOverrideIsAdminOnlyAndRegistered(t *testing.T) {
	src, err := os.ReadFile(filepath.Join("..", "httpapi", "router.go"))
	if err != nil {
		t.Fatalf("read router.go: %v", err)
	}
	body := string(src)

	if !strings.Contains(body, `r.Put("/stores/{id}/payout-lag", admin.SetStorePayoutLag)`) {
		t.Errorf("router.go does not register the payout-lag route:\n" +
			"a repository column and a service method with no route is the same " +
			"failure as a refund state with no endpoint -- the capability exists and " +
			"no one can reach it")
	}
	// PUT, not POST: it sets a field on an existing store rather than performing an
	// action, and PUT is what makes the idempotent "set this value" semantics true.
	if strings.Contains(body, `r.Post("/stores/{id}/payout-lag"`) {
		t.Error("the payout-lag route is a POST; it sets a value, so PUT is the " +
			"correct verb and POST invites a caller to treat it as an action")
	}

	// And it must sit inside the /admin group. The check reads the region between
	// the group declaration and the route, which is how the existing money-route
	// test establishes the same thing.
	if !insideAdminGroup(body, "/stores/{id}/payout-lag") {
		t.Error("the payout-lag route is not inside the /admin group")
	}
	// And there must be no seller-facing equivalent.
	if strings.Contains(body, "/seller/") && strings.Contains(body, "payout-lag") {
		at := strings.Index(body, "payout-lag")
		if st := strings.Index(body, `r.Route("/seller"`); st >= 0 && st < at &&
			strings.Contains(body[st:at], "payout-lag") {
			t.Error("a payout-lag route appears inside the /seller group; a seller " +
				"must not be able to set their own payout term")
		}
	}
}

// insideAdminGroup reports whether `needle` appears between the admin group's
// declaration and the next top-level `r.Route(`.
func insideAdminGroup(body, needle string) bool {
	start := strings.Index(body, `r.Route("/admin"`)
	if start < 0 {
		return false
	}
	rest := body[start:]
	// The admin group is the last r.Route in the file, so the needle must be inside
	// it. Bounding by the group rather than searching the whole file is the point:
	// a route string appearing anywhere is not evidence it is behind the guard.
	return strings.Contains(rest, needle)
}

// The bounds are duplicated in three places -- the service constants, the
// migration's CHECK, and config.Validate -- because none of them can import the
// others. Duplication with nothing comparing it is how a limit stops being enforced
// in one place while the other two look fine.
func TestStorePayoutLagBoundsMatchTheService(t *testing.T) {
	mig, err := osReadFile(filepathJoin("..", "db", "migrations", "00048_seller_payout_lag.sql"))
	if err != nil {
		t.Fatalf("read migration 00048: %v", err)
	}
	text := squashWhitespace(string(mig))
	for _, want := range []string{
		itoa(MinPayoutLagDays), itoa(MaxPayoutLagDays),
	} {
		if !strings.Contains(text, want) {
			t.Errorf("migration 00048 does not carry the service's bound %s:\n%s\n"+
				"a column CHECK and a Go range check that disagree means one of them "+
				"is not the rule", want, text)
		}
	}
	// NULL must remain storable. A NOT NULL column would make "use the platform
	// default" unrepresentable, and the default is meant to change.
	if !strings.Contains(text, "payout_lag_days IS NULL OR") {
		t.Error("00048 does not permit a NULL payout_lag_days; NULL is how a seller " +
			"declares they have not chosen one, and the platform default is meant to change")
	}
	if strings.Contains(text, "payout_lag_days INT NOT NULL") {
		t.Error("00048 makes payout_lag_days NOT NULL, which removes the " +
			"platform-default fallback entirely")
	}
	// And 0 must not be storable: a lag of zero releases a hold the moment the
	// order completes, before any return could be filed.
	if strings.Contains(text, "BETWEEN 0 AND") {
		t.Error("00048 admits a lag of 0, which would make the hold decorative")
	}
}

// squashWhitespace collapses runs of whitespace to single spaces.
//
// A migration wraps its CHECK across lines, and a test that matches the wrapped
// source is a test of the line breaks. Reading SQL as one logical line is what lets
// the assertion be about the CONSTRAINT rather than about how it was typed.
func squashWhitespace(s string) string {
	return strings.Join(strings.Fields(s), " ")
}

// repositoryFunctionSource reads one repository method body from disk. The service
// package cannot import the repository's unexported helpers, and reading the source
// is the established idiom here for asserting on a SQL statement.
func repositoryFunctionSource(t *testing.T, fn string) string {
	t.Helper()
	raw, err := osReadFile(filepathJoin("..", "repository", "payment_repo.go"))
	if err != nil {
		t.Fatalf("read payment_repo.go: %v", err)
	}
	body := string(raw)
	start := strings.Index(body, "func (r *PaymentRepository) "+fn+"(")
	if start < 0 {
		t.Fatalf("%s not found in payment_repo.go", fn)
	}
	rest := body[start:]
	if end := strings.Index(rest, "\nfunc "); end > 0 {
		rest = rest[:end]
	}
	return rest
}

func osReadFile(p string) ([]byte, error) { return os.ReadFile(p) }

func filepathJoin(elems ...string) string { return filepath.Join(elems...) }

func itoa(n int) string { return strconv.Itoa(n) }

// The range check is pure arithmetic and is tested as such, not through the
// service -- which would need a store row and a database.
//
// The edges matter in both directions. 0 must be refused rather than stored: a lag
// of zero releases a hold the moment the order completes, which is before any return
// could be filed, so the hold would be decorative and the fraud control would be
// theatre. 1 and the maximum must be accepted, so the check is a range and not a
// rejection of anything unusual.
func TestThePayoutLagRangeRefusesTheValuesThatWouldDefeatTheHold(t *testing.T) {
	body := functionSource(t, "seller_service.go", "AdminSetPayoutLag")

	if !strings.Contains(body, "*days < MinPayoutLagDays || *days > MaxPayoutLagDays") {
		t.Errorf("the range check is gone or inverted:\n%s\n"+
			"without it the column CHECK is the only guard, and it surfaces as a "+
			"raw constraint violation naming a column", body)
	}
	// A NIL clears the override, and must not be range-checked as zero.
	if !strings.Contains(body, "days != nil &&") {
		t.Errorf("a nil override is range-checked:\n%s\n"+
			"nil means the override is cleared and the platform default applies", body)
	}
	if !strings.Contains(body, "PAYOUT_LAG_OUT_OF_RANGE") {
		t.Errorf("the out-of-range case is not named:\n%s", body)
	}
	// The store is resolved BEFORE the write, because the column is keyed on
	// owner_id and the route carries the store id. Writing without resolving would
	// update zero rows and report success.
	if strings.Index(body, "s.stores.ByID(") > strings.Index(body, "s.stores.SetPayoutLagDays(") {
		t.Errorf("the store is resolved after the write:\n%s\n"+
			"the route carries a store id and the column is keyed on owner_id, so "+
			"the resolution must come first or the UPDATE matches nothing", body)
	}
	// ...and the write must use the RESOLVED owner id, not the route's id. Ordering
	// alone is not the property: passing the store id to an owner-keyed UPDATE
	// matches zero rows and reports success, so an operator would believe they had
	// set a payout term that was never stored. Mutation M34 changed only this
	// argument and left the ordering intact, which is why both are asserted.
	if !strings.Contains(body, "SetPayoutLagDays(ctx, store.OwnerID, days)") {
		t.Errorf("the write does not use the resolved owner id:\n%s\n"+
			"the route's {id} is a STORE id and the column is keyed on owner_id; "+
			"passing the store id updates zero rows and reports success", body)
	}
	if strings.Contains(body, "SetPayoutLagDays(ctx, storeID") {
		t.Errorf("the write is passed the route's store id directly:\n%s\n"+
			"that matches no rows and reports success, so the override silently "+
			"never takes effect", body)
	}
}
