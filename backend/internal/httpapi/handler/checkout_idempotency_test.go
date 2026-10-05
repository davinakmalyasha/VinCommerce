package handler

import (
	"os"
	"strings"
	"testing"
)

// Checkout idempotency was GET-then-SET:
//
//	GET  idem:<user>:<key>   -> miss
//	<place the order>
//	SET  idem:<user>:<key>
//
// which is not a lock. Every concurrent retry arriving before the SET saw the same miss
// and placed its own order: N retries, N orders, N stock reservations, N charges. A
// checkout is precisely where a buyer double-taps and mobile clients retry on a slow
// response, so the guard invited the failure it claimed to prevent.
//
// There is no Redis here, so this asserts the ORDERING that makes it a lock rather than
// a race, which is the property that actually broke.
func TestIdempotencyKeyIsClaimedBeforeTheOrderIsPlaced(t *testing.T) {
	src, err := os.ReadFile("cart.go")
	if err != nil {
		t.Fatalf("read cart.go: %v", err)
	}
	body := string(src)
	i := strings.Index(body, "func (h *Checkout) Place(")
	if i < 0 {
		t.Fatal("no Checkout.Place handler")
	}
	rest := body[i:]
	if end := strings.Index(rest[1:], "\nfunc "); end > 0 {
		body = rest[:end]
	}

	claimAt := strings.Index(body, "SetNX(")
	placeAt := strings.Index(body, "svc.PlaceOrder(")
	if claimAt < 0 {
		t.Fatal("the key is never CLAIMED with SetNX.\n" +
			"Read-then-write is not a lock: two retries both read a miss and both place an " +
			"order. SetNX is atomic, so exactly one request can win")
	}
	if placeAt < 0 {
		t.Fatal("cannot find the PlaceOrder call; update this test")
	}
	if claimAt > placeAt {
		t.Errorf("SetNX is at offset %d but PlaceOrder is called at %d: the claim happens "+
			"AFTER the order is placed, which is the original race.\n"+
			"The claim has to exist before the order does, or the window between them is "+
			"exactly where concurrent retries slip in", claimAt, placeAt)
	}
}

// The claim must be honoured: a request that LOSES the race must not place an order.
func TestALostRaceDoesNotPlaceAnOrder(t *testing.T) {
	body := checkoutPlaceBody(t)
	claimAt := strings.Index(body, "SetNX(")
	if claimAt < 0 {
		t.Fatal("no SetNX claim")
	}
	region := body[claimAt:]
	if end := strings.Index(region, "svc.PlaceOrder("); end > 0 {
		region = region[:end]
	}
	if !strings.Contains(region, "!claimed") {
		t.Errorf("a lost SetNX race is not handled:\n%s\n"+
			"Without checking the boolean, two callers both believe they hold the lock and "+
			"both place an order -- the same defect with an extra Redis round trip", region)
	}
	if !strings.Contains(region, "StatusTooEarly") {
		t.Error("a lost race does not return an honest status.\n" +
			"Placing the order anyway, or returning 200, both hide the collision")
	}
}

// THE SHAPE BUG. The replay response used `orders` as a comma-joined STRING while the
// original response returned an array of objects, so a client reading `orders[0].id`
// got the character "a" -- and only on the retry, which is the path nobody tests by hand.
func TestTheReplayResponseHasTheSameShapeAsTheFirstResponse(t *testing.T) {
	body := checkoutPlaceBody(t)

	if strings.Contains(body, `orderIDs += ","`) || strings.Contains(body, `orderIDs += o.ID`) {
		t.Error("the replay payload is built by concatenating order ids into a string.\n" +
			"That makes `orders` a string on the replay path and an array on the original " +
			"path, so a client that parses `orders[0].id` breaks only when retrying")
	}
	if !strings.Contains(body, "json.Marshal(ids)") {
		t.Error("the order ids are not marshalled as JSON.\n" +
			"Store them as a JSON array so both responses agree")
	}
	if !strings.Contains(body, "json.Unmarshal(") {
		t.Error("the stored value is never decoded.\n" +
			"Reading it back as a string and echoing it is what produced the shape mismatch")
	}
	// Assert the SLICE, not the idiomatic way of building it.
	//
	// This test previously only rejected `orderIDs += ","`, and a mutation that rebuilt
	// the value with `ids := ""; ids += o.ID` passed it -- a dishonest witness for the
	// general case, which is the exact failure mode this file exists to prevent. Tying
	// the assertion to `make([]string, ...)` follows the value through any construction.
	if !strings.Contains(body, "make([]string") {
		t.Error("the order ids are not accumulated as a []string.\n" +
			"Joining them into one string and marshalling that is what made the replay " +
			"response disagree with the original")
	}
}

// A key claimed but never filled must not block the buyer for the whole TTL.
func TestAFailedOrderReleasesTheClaim(t *testing.T) {
	body := checkoutPlaceBody(t)

	errAt := strings.Index(body, "svc.PlaceOrder(")
	if errAt < 0 {
		t.Fatal("cannot find PlaceOrder")
	}
	tail := body[errAt:]
	if end := strings.Index(tail, "http.StatusCreated"); end > 0 {
		tail = tail[:end]
	}
	if !strings.Contains(tail, "rdb.Del(") {
		t.Errorf("a failed PlaceOrder does not release the claim:\n%s\n"+
			"Otherwise every transient failure becomes a permanent 425 for that key, for "+
			"the full hour, and the buyer cannot place the order at all", tail)
	}
}

// An in-flight claim holds no order ids, so it must be distinguished from a finished one
// rather than replayed as an empty success.
func TestAnInFlightClaimIsNotReplayedAsSuccess(t *testing.T) {
	body := checkoutPlaceBody(t)
	if !strings.Contains(body, "REQUEST_IN_PROGRESS") {
		t.Error("an unfinished claim is not reported as in-progress.\n" +
			"Without a distinct answer, a retry either replays an empty order list -- " +
			"telling the buyer their order succeeded when no order exists -- or places a " +
			"second one")
	}
	if !strings.Contains(body, "in-flight") {
		t.Error("no placeholder value distinguishes an unfinished claim from a finished one")
	}
}

// The guard is per-user, so one buyer cannot collide with, or replay, another's.
func TestTheIdempotencyKeyIsScopedToTheAuthenticatedUser(t *testing.T) {
	body := checkoutPlaceBody(t)
	if !strings.Contains(body, `"idem:" + user.ID + ":"`) {
		t.Errorf("the redis key is not scoped to the authenticated user:\n%s\n"+
			"A global key would let any buyer replay another buyer's checkout response, "+
			"which is a cross-account data leak rather than merely a cache collision", body)
	}
}

func checkoutPlaceBody(t *testing.T) string {
	t.Helper()
	raw, err := os.ReadFile("cart.go")
	if err != nil {
		t.Fatalf("read cart.go: %v", err)
	}
	body := string(raw)
	i := strings.Index(body, "func (h *Checkout) Place(")
	if i < 0 {
		t.Fatal("no Checkout.Place handler")
	}
	rest := body[i:]
	if end := strings.Index(rest[1:], "\nfunc "); end > 0 {
		return rest[:end]
	}
	return rest
}
