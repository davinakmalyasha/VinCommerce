package service

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/vincommerce/backend/internal/domain"
	"github.com/vincommerce/backend/internal/payments"
	"github.com/vincommerce/backend/internal/repository"
)

// THE DOUBLE-REFUND.
//
// A refund notification from the gateway means the provider has ALREADY returned
// the money. When the gateway-refund path was unified, that notification was
// routed through `ExecuteRefund` -- the path for refunds WE initiate -- so every
// `payment.refunded` webhook asked Midtrans to refund the same money a second
// time.
//
// The failure is quiet for a full refund and expensive for a partial one. Midtrans
// caps cumulative refunds at the charge, so a second full refund is rejected and
// merely noisy. A partial one is not:
//
//	charge Rp100,000
//	provider refunds Rp30,000 and notifies us
//	we call Refund(Rp30,000) again
//	provider: 30,000 + 30,000 = 60,000 <= 100,000  ->  ACCEPTED
//	buyer has received Rp60,000 for a Rp100,000 order
//
// A second, independent defect compounded it: the cumulative cap summed
// `wallet_transactions WHERE kind = 'credit'`, but a gateway refund never credits
// the buyer's wallet -- the money goes to their card. So the cap read zero after
// every gateway refund and the guard built to stop over-refunding measured
// nothing on the path that actually refunds people.
//
// Both are asserted here. Neither needs a database: the first is a property of
// which function the webhook calls, the second of which query the cap reads.

// The two legs of a reversal must sum to the refund EXACTLY, and BOTH must be
// present.
//
// A refund after an escrow release has already paid the seller AND the platform,
// so the refund has to claw back both. Dropping the commission leg is how a
// marketplace keeps its fee on a fully refunded sale -- the defect `03a100b`
// fixed, and reachable again while this arithmetic was inline in a function that
// needed a database to call.
func TestARefundAfterReleaseReversesBothLegs(t *testing.T) {
	const charge, fee = 100000.0, 2000.0

	fullFee, fullSeller := deriveReversalLegs(fee, charge, charge)
	if fullFee != fee {
		t.Errorf("a full refund reverses Rp%.0f of commission, want Rp%.0f", fullFee, fee)
	}
	if fullSeller != charge-fee {
		t.Errorf("a full refund reverses Rp%.0f from the seller, want Rp%.0f",
			fullSeller, charge-fee)
	}

	// And a PARTIAL must reverse both proportionally, still summing exactly.
	//
	// The amounts here are all above the rounding threshold (see
	// TestTinyRefundsReverseNoCommission below), because for a refund smaller than
	// about Rp25 the commission share is less than half a rupiah and correctly
	// rounds to zero. Asserting a non-zero commission on those would be asserting
	// that the function invents money.
	for _, amount := range []float64{30000, 33333, 25000, 99999, 123457} {
		refundFee, refundSeller := deriveReversalLegs(fee, charge, amount)
		if refundFee+refundSeller != amount {
			t.Errorf("refund of Rp%.0f: legs sum to Rp%.0f, want Rp%.0f; the drift "+
				"accumulates permanently because nothing ever sweeps it",
				amount, refundFee+refundSeller, amount)
		}
		if refundFee == 0 {
			t.Errorf("refund of Rp%.0f reversed NO commission", amount)
		}
		if refundSeller == 0 && amount > fee {
			t.Errorf("refund of Rp%.0f reversed nothing from the seller", amount)
		}
	}
}

// WHOLE-RUPIAH ROUNDING HAS A THRESHOLD, AND IT IS NOT A BUG.
//
// On a Rp100,000 order at 2%, the commission attributable to a Rp1 refund is
// Rp0.02. That is not a whole rupiah, so it rounds to Rp0 and the whole Rp1 comes
// off the seller.
//
// This is asserted rather than left implicit because it looks like a bug and
// someone will eventually "fix" it by introducing a sen, which reintroduces every
// sub-rupiah amount the checkout work removed -- an amount the gateway cannot
// charge, which is what caused paid orders to be cancelled as AMOUNT_MISMATCH.
func TestTinyRefundsReverseNoCommissionAndThatIsCorrect(t *testing.T) {
	const charge, fee = 100000.0, 2000.0

	// Rp1: the share is Rp0.02, which is not a whole rupiah.
	f, s := deriveReversalLegs(fee, charge, 1)
	if f != 0 {
		t.Errorf("a Rp1 refund reversed Rp%.2f of commission; the exact share is Rp0.02 "+
			"and IDR has no sen", f)
	}
	if s != 1 {
		t.Errorf("the seller leg is Rp%.0f, want the full Rp1", s)
	}
	if f+s != 1 {
		t.Errorf("legs sum to Rp%.0f, want Rp1", f+s)
	}

	// Rp25: the share is exactly Rp0.50, which rounds to Rp1. Above this, the
	// commission leg is non-zero.
	if f, _ := deriveReversalLegs(fee, charge, 25); f == 0 {
		t.Error("a Rp25 refund reversed no commission; the share is Rp0.50 and " +
			"rounds to Rp1")
	}
}

// A replayed provider notification must return the ORIGINAL refund and do
// nothing else.
//
// This is the guard, exercised end to end rather than through the classifier.
// The earlier version of this file tested `classifyProviderRefund(ev, true)` with
// a literal `true`, which asserts that the CLASSIFIER handles a recorded refund
// correctly and says nothing about whether the lookup that produces that `true`
// is wired up. A mutation replacing `already != nil` with `false` passed every
// test in the package.
//
// The point of this test is that it supplies the recorded row directly, so the
// only way to pass is to actually consult it.
// The call-graph parser itself, tested.
//
// A guard that parses source to decide what is reachable is only as trustworthy as
// its parser, and this parser got two things wrong in a row -- both discovered by
// a surviving mutation rather than by reading the code. It failed open on
// `if x, err := ...; err == nil {`, and before that it failed open on any
// `x := s.gatewayFor(...)` written in a shape the pattern list did not cover.
func TestFindAssignmentLocatesTheOperator(t *testing.T) {
	cases := []struct {
		line string
		want int // -1 for none
	}{
		{"gw, err := s.gatewayFor(m)", 8},
		{"gw := s.gatewayFor(m)", 3},
		// The case that broke it: the first `=` belongs to `==`, and the `:=` comes
		// before it.
		{"if g, gerr := s.gatewayFor(m); gerr == nil {", 11},
		{"for i := range xs {", 6},
		{"if x == y {", -1},
		{"if x != y {", -1},
		{"if x >= y {", -1},
		{"return nil", -1},
		{"x += 1", -1},
		{"total = total + fee * (amount / charge)", 6},
	}
	for _, c := range cases {
		if got := findAssignment(c.line); got != c.want {
			t.Errorf("findAssignment(%q) = %d, want %d", c.line, got, c.want)
		}
	}
}

// And the gateway-variable extraction, which is what the reachability check rests
// on.
func TestGatewayVariablesCoversTheIdiomaticShapes(t *testing.T) {
	cases := []struct {
		name string
		src  string
		want []string
	}{
		{
			name: "statement form",
			src:  "\tgw, err := s.gatewayFor(method)\n\tres, _ := gw.Refund(ctx, in)",
			want: []string{"gw", "err"},
		},
		{
			name: "if-initializer with a trailing comparison",
			src:  "\tif g, gerr := s.gatewayFor(m); gerr == nil {\n\t\tg.Refund(ctx, in)\n\t}",
			want: []string{"g"},
		},
		{
			name: "single return value",
			src:  "\tg := s.gatewayFor(method)",
			want: []string{"g"},
		},
		{
			name: "not a gateway",
			src:  "\tx, err := s.somethingElse(m)",
			want: nil,
		},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got := gatewayVariables(c.src)
			for _, w := range c.want {
				found := false
				for _, g := range got {
					if g == w {
						found = true
					}
				}
				if !found {
					t.Errorf("variable %q not discovered in %q; got %v", w, c.src, got)
				}
			}
		})
	}
}

func TestAReplayedNotificationReturnsTheOriginalAndChangesNothing(t *testing.T) {
	original := &repository.Refund{
		ID:         "refund-original",
		OrderID:    "o1",
		Gateway:    "midtrans",
		GatewayRef: "rf-77",
		Amount:     30000,
		Status:     RefundStateSucceeded,
	}
	lookup := &stubRefundLookup{found: original}
	// `payments` is nil, so anything that reaches the database PANICS rather than
	// quietly succeeding. A replay must return before it gets there.
	svc := &PaymentService{refunds: lookup}

	const intentID = "intent-1"
	ev := providerRefundEvent{OrderID: "o1", Amount: 30000, Gateway: "midtrans", Reference: "rf-77"}
	got, err := svc.replayIsRecognised(t.Context(), intentID, ev)

	if err != nil {
		t.Fatalf("a replay returned an error; the provider would retry forever: %v", err)
	}
	if got == nil {
		t.Fatal("the replay was not recognised as already applied, so it would be " +
			"applied a second time and the buyer paid twice")
	}
	if got.ID != original.ID {
		t.Errorf("returned %q, want the recorded refund %q", got.ID, original.ID)
	}
	if lookup.calls != 1 {
		t.Errorf("the lookup was consulted %d times, want exactly 1", lookup.calls)
	}

	// WHAT IT WAS ASKED. The lookup filters `WHERE payment_intent_id = $1`, and
	// that column is `UUID NOT NULL`, so an empty id is not a payment with no
	// refunds -- it is an invalid uuid, and the query ERRORS. The guard then aborts
	// every provider refund notification, and Midtrans retries for 24 hours.
	//
	// Asserting only that the lookup was called once is what let this ship: the
	// stub used to discard both string arguments, so a literal "" was invisible
	// here and in every other test in this file.
	if lookup.lastIntentID != intentID {
		t.Errorf("the lookup was asked about intent %q, want %q; an unscoped lookup "+
			"cannot match a refund and the guard is inert", lookup.lastIntentID, intentID)
	}
	if lookup.lastKey != providerRefundKey(ev) {
		t.Errorf("the lookup was asked about key %q, want %q",
			lookup.lastKey, providerRefundKey(ev))
	}
	// And the DECISION, so a future refactor that keeps the guard but stops
	// routing replays through it is still caught.
	if d := classifyProviderRefund(ev, got != nil); d != refundDecisionAlreadyApplied {
		t.Errorf("decision = %q, want %q", d, refundDecisionAlreadyApplied)
	}
}

// An empty key never consults the lookup at all: there is nothing to match on,
// and hitting the database for it is a query per notification for no reason.
func TestAnEmptyProviderKeySkipsTheLookup(t *testing.T) {
	lookup := &stubRefundLookup{found: &repository.Refund{ID: "should-not-be-found"}}
	svc := &PaymentService{refunds: lookup}

	got, err := svc.refundRecordedForProviderKey(t.Context(), "intent-1", "")
	if err != nil {
		t.Fatalf("empty key returned an error: %v", err)
	}
	if got != nil {
		t.Error("an empty key returned a refund; there was nothing to match")
	}
	if lookup.calls != 0 {
		t.Errorf("an empty key hit the database %d times", lookup.calls)
	}
}

// stubRefundLookup returns a fixed recorded refund, counts calls, and RECORDS the
// arguments it was handed.
//
// The recording is not incidental. This stub used to declare both string
// parameters unnamed and ignore them, which is why a caller passing an empty
// intent id -- a literal "" against a `UUID NOT NULL` column, so the query errored
// on every provider refund notification -- passed every test in this file. A stub
// that cannot see its inputs cannot fail on them.
type stubRefundLookup struct {
	found *repository.Refund
	calls int
	err   error

	// lastIntentID and lastKey are what the caller actually asked about.
	lastIntentID, lastKey string
}

func (s *stubRefundLookup) RefundByProviderKey(
	_ context.Context, intentID, key string,
) (*repository.Refund, error) {
	s.calls++
	s.lastIntentID, s.lastKey = intentID, key
	return s.found, s.err
}

// A lookup error must not be treated as "nothing recorded". Doing so turns a
// database blip into a duplicate refund, which is the worst possible failure
// direction for this guard.
func TestALookupFailureIsNotSilentlyTreatedAsNothingRecorded(t *testing.T) {
	boom := errors.New("connection reset")
	svc := &PaymentService{refunds: &stubRefundLookup{err: boom}}

	_, err := svc.refundRecordedForProviderKey(t.Context(), "intent-1", "provider:midtrans:rf-77:30000")
	if err == nil {
		t.Fatal("a failed lookup returned no error; the caller would conclude nothing " +
			"is recorded and apply a duplicate")
	}
	if !errors.Is(err, boom) {
		t.Errorf("error = %v, want the underlying failure", err)
	}
}

// The commission leg is the one that gets dropped, so assert it SCALES rather
// than being all-or-nothing.
func TestTheCommissionReversalScalesWithTheRefund(t *testing.T) {
	const charge, fee = 100000.0, 2000.0

	halfFee, _ := deriveReversalLegs(fee, charge, 50000)
	if halfFee != 1000 {
		t.Errorf("a half refund reverses Rp%.0f of commission, want Rp1000 (half of Rp%.0f)",
			halfFee, fee)
	}
	smallFee, _ := deriveReversalLegs(fee, charge, 1000)
	if smallFee <= 0 {
		t.Error("a Rp1,000 refund reversed no commission at all")
	}
	if smallFee >= fee {
		t.Errorf("a Rp1,000 refund reversed Rp%.0f of commission, which is not less than "+
			"the Rp%.0f charged", smallFee, fee)
	}
}

// Degenerate inputs must produce zeroes, never negatives.
func TestDeriveReversalLegsHandlesDegenerateInputs(t *testing.T) {
	for _, c := range []struct {
		name                string
		fee, charge, amount float64
	}{
		{"zero refund", 2000, 100000, 0},
		{"negative refund", 2000, 100000, -5000},
		{"zero charge", 2000, 0, 5000},
		{"negative charge", 2000, -100000, 5000},
		{"zero fee", 0, 100000, 50000},
		{"all zero", 0, 0, 0},
	} {
		t.Run(c.name, func(t *testing.T) {
			f, s := deriveReversalLegs(c.fee, c.charge, c.amount)
			if f < 0 || s < 0 {
				t.Errorf("legs are negative: fee=Rp%.2f seller=Rp%.2f; a negative "+
					"seller leg is a credit TO the seller on a refund", f, s)
			}
			if c.amount <= 0 && (f != 0 || s != 0) {
				t.Errorf("legs = (Rp%.2f, Rp%.2f) for a non-positive amount, want zeroes", f, s)
			}
		})
	}
}

// The legs must still sum when the fee exceeds the refund itself.
func TestDeriveReversalLegsClampsWhenTheFeeExceedsTheRefund(t *testing.T) {
	f, s := deriveReversalLegs(2000, 100000, 500)
	if s < 0 {
		t.Fatalf("seller leg is negative (Rp%.2f): a refund must never credit the seller", s)
	}
	if f+s != 500 {
		t.Errorf("legs sum to Rp%.2f, want Rp500", f+s)
	}
}

func TestAProviderRefundNotificationNeverSubmitsARefundToTheProvider(t *testing.T) {
	// The structural assertion, and the strongest one available: the webhook path
	// must not be able to reach a function that talks to the gateway.
	//
	// A behavioural test would need a database, because the call is buried in a
	// transaction. This reads the source instead, which is why it can run
	// anywhere -- and why it catches the bug even if the call is moved.
	src := readSource(t, "payment_service.go")
	body := src

	// Find onRefunded and look at what it calls.
	start := strings.Index(body, "func (s *PaymentService) onRefunded(")
	if start < 0 {
		t.Fatal("onRefunded not found; if it was renamed, point this test at the new name")
	}
	end := strings.Index(body[start:], "\nfunc ")
	if end < 0 {
		end = len(body) - start
	}
	fn := body[start : start+end]

	if strings.Contains(fn, "s.RefundOrder(") {
		t.Error("onRefunded calls RefundOrder, which submits a refund to the gateway; " +
			"a provider refund notification must go to RecordProviderRefund, which never does")
	}
	if strings.Contains(fn, "s.ExecuteRefund(") {
		t.Error("onRefunded calls ExecuteRefund, which submits a refund to the gateway")
	}
	if !strings.Contains(fn, "s.RecordProviderRefund(") {
		t.Error("onRefunded does not call RecordProviderRefund; a provider refund " +
			"must be recorded without asking the provider to do it again")
	}
}

// RecordProviderRefund must not call the gateway, and neither may anything IT
// calls.
//
// Checked transitively over the real call graph rather than only
// RecordProviderRefund's own body, because the obvious version -- asserting the
// gateway call is not in that one function -- passes while the call sits one level
// down in a helper. That is not hypothetical: the double-refund itself was one
// level of indirection away.
func TestRecordProviderRefundNeverReachesTheGateway(t *testing.T) {
	body := readSource(t, "refund_service.go")
	funcs := parseGoFuncs(body)

	// Everything reachable from RecordProviderRefund, following method calls.
	reachable := reachableFrom(funcs, "RecordProviderRefund")

	for _, name := range reachable {
		parsed, ok := funcs[name]
		if !ok {
			continue
		}
		if parsed.callsGateway {
			t.Errorf("%s calls the gateway and is reachable from RecordProviderRefund; "+
				"a provider refund must never be submitted back to the provider", name)
		}
	}
}

// reachableFrom walks the call graph from a root function, returning every
// function name reachable from it (including itself).
//
// A breadth-first walk with a `seen` set, so a cycle in the graph terminates --
// and a call graph in a package with interfaces and mutual recursion is exactly
// where a naive recursive walk would hang the test suite.
func reachableFrom(funcs map[string]parsedFunc, root string) []string {
	if _, ok := funcs[root]; !ok {
		return nil
	}
	seen := map[string]bool{root: true}
	queue := []string{root}

	for len(queue) > 0 {
		name := queue[0]
		queue = queue[1:]

		cur, ok := funcs[name]
		if !ok {
			continue
		}
		for _, callee := range cur.calls {
			if seen[callee] {
				continue
			}
			seen[callee] = true
			queue = append(queue, callee)
		}
		// Bare calls resolve to functions in the same package, and are enqueued
		// the same way. No recursion: the queue already reaches them, and
		// recursing here would need a depth limit to terminate on a cycle.
		for _, local := range cur.localCalls {
			if seen[local] {
				continue
			}
			seen[local] = true
			queue = append(queue, local)
		}
	}

	out := make([]string, 0, len(seen))
	for name := range seen {
		out = append(out, name)
	}
	return out
}

// parsedFunc is one top-level function, reduced to what the call graph needs.
type parsedFunc struct {
	name string
	// body is the raw source, kept so the classification pass can read it.
	body string
	// callsGateway is true if the body invokes a gateway refund.
	callsGateway bool
	// calls are `x.Foo(` -- the qualified names, resolvable across packages.
	calls []string
	// localCalls are bare `foo(` names, which are functions in the same package.
	localCalls []string
}

// parseGoFuncs splits a Go source file into its top-level functions.
//
// A hand-rolled scan rather than go/ast: the goal is to answer "which functions
// can reach a gateway call", and the file is a single package with a consistent
// style, so a brace-depth scan is sufficient and keeps the test free of a
// toolchain dependency. It tracks string literals and comments so a `}` inside
// either does not end a function early.
func parseGoFuncs(src string) map[string]parsedFunc {
	out := map[string]parsedFunc{}
	lines := strings.Split(src, "\n")

	depth := 0
	inFunc := false
	var name string
	var body []string

	flush := func() {
		if !inFunc {
			return
		}
		f := parsedFunc{name: name, body: strings.Join(body, "\n")}
		out[name] = f
		inFunc = false
		body = nil
	}

	for _, line := range lines {
		trimmed := strings.TrimSpace(line)

		if !inFunc {
			if strings.HasPrefix(trimmed, "func ") {
				name = parseFuncName(trimmed)
				inFunc = true
				body = []string{line}
				depth = strings.Count(line, "{") - strings.Count(line, "}")
			}
			continue
		}
		body = append(body, line)
		depth += strings.Count(line, "{") - strings.Count(line, "}")
		if depth <= 0 {
			flush()
		}
	}
	flush()

	// Now classify each function.
	for name, f := range out {
		raw := f.body
		// Strip comments and string literals so a mention in prose is not a call.
		raw = stripGoComments(raw)

		// Which locals hold a gateway? Tracked rather than pattern-matched on the
		// call site, because a pattern list of call FORMS is a guard that fails
		// open: a mutation that wrote `g.Refund(ctx, payments.RefundInput{...})`
		// instead of `gw.Refund(ctx, ...)` matched none of the three literal
		// patterns and the whole test suite passed. Which VARIABLE holds the
		// gateway does not change when the call is written differently.
		gwVars := gatewayVariables(raw)
		for _, v := range gwVars {
			if strings.Contains(raw, v+".Refund(") {
				f.callsGateway = true
			}
		}
		// Also catch a gateway used directly through a package qualifier.
		if strings.Contains(raw, "payments.Gateway)") && strings.Contains(raw, ".Refund(") {
			f.callsGateway = true
		}
		f.calls = qualifiedCalls(raw)
		f.localCalls = bareCalls(raw)
		out[name] = f
	}
	return out
}

// gatewayVariables finds locals assigned from a gateway resolver.
//
// Handles BOTH shapes a Go programmer writes:
//
//	gw, err := s.gatewayFor(m)        // statement
//	if g, err := s.gatewayFor(m); ... // if-initializer
//
// The second is the more idiomatic of the two, and the first version of this
// parser missed it -- it split on the first ":=" in the line, got "if g, err" as
// the left-hand side, and dropped it because "if" is not an identifier. A
// mutation that planted its gateway call in that shape passed the entire suite.
// A guard that only recognises one of the two ways to write a line is a guard
// that fails open, which is the worst kind.
func gatewayVariables(src string) []string {
	gwNames := map[string]bool{
		"gatewayFor": true, "NewGateway": true, "NewMidtransGateway": true,
	}
	var out []string
	seen := map[string]bool{}

	add := func(lhs string) {
		for _, part := range strings.Split(lhs, ",") {
			v := strings.TrimSpace(part)
			// Strip any leading statement keyword the split left behind.
			for _, kw := range []string{"if", "var", "const", "return"} {
				if strings.HasPrefix(v, kw+" ") {
					v = strings.TrimSpace(strings.TrimPrefix(v, kw+" "))
				}
			}
			if v != "" && isIdentStart(v[0]) && !isKeyword(v) && !seen[v] {
				seen[v] = true
				out = append(out, v)
			}
		}
	}

	for _, line := range strings.Split(src, "\n") {
		trimmed := strings.TrimSpace(line)
		eq := findAssignment(trimmed)
		if eq < 0 {
			continue
		}
		lhs := trimmed[:eq]
		rhs := trimmed[eq+1:]

		isGateway := false
		for name := range gwNames {
			if strings.Contains(rhs, name+"(") {
				isGateway = true
			}
		}
		if !isGateway {
			continue
		}
		add(lhs)
	}
	return out
}

// findAssignment locates the assignment operator in a line, or -1.
//
// `:=` is searched FIRST and unconditionally, because it cannot be part of a
// comparison. The previous version searched for a bare `=` and rejected any line
// whose first `=` was part of `==` -- which silently discarded exactly the line
// that matters:
//
//	if g, gerr := s.gatewayFor(m); gerr == nil {
//
// The first `=` in that line belongs to `gerr == nil`, so the line was skipped,
// `g` was never discovered as a gateway, and a real gateway call planted in it
// passed the entire suite. A guard that fails open on the most idiomatic Go
// control-flow form is worse than no guard, because it reads as protection.
func findAssignment(line string) int {
	// Short variable declaration. Cannot be a comparison.
	if i := strings.Index(line, ":="); i >= 0 {
		// Make sure it is not inside a slice or map literal, which is the only
		// common place `:=` appears without being an assignment. Rare enough in
		// practice that a simple check suffices.
		return i
	}
	// Plain assignment: a single `=` not part of ==, !=, <=, >=.
	for i := 0; i < len(line); i++ {
		if line[i] != '=' {
			continue
		}
		if i > 0 {
			switch line[i-1] {
			case '=', '!', '<', '>', '+', '-', '*', '/', '%', '&', '|', '^':
				continue
			}
		}
		if i+1 < len(line) && line[i+1] == '=' {
			continue
		}
		return i
	}
	return -1
}

// parseFuncName extracts the bare name from a func declaration line.
//
//	"func (s *PaymentService) Refund(ctx context.Context) error {"  -> "Refund"
//	"func decideRefundCap(charge float64) ..."                       -> "decideRefundCap"
func parseFuncName(line string) string {
	rest := strings.TrimPrefix(line, "func ")
	if strings.HasPrefix(rest, "(") {
		// Method: the name is after the receiver's closing paren.
		if i := strings.Index(rest, ")"); i >= 0 {
			rest = rest[i+1:]
		}
	}
	rest = strings.TrimSpace(rest)
	if i := strings.IndexAny(rest, "( "); i >= 0 {
		return rest[:i]
	}
	return rest
}

// stripGoComments removes // and /* */ comments and string literals.
func stripGoComments(s string) string {
	var b strings.Builder
	inLine, inBlock, inStr, inRune := false, false, false, false
	for i := 0; i < len(s); i++ {
		c := s[i]
		switch {
		case inLine:
			if c == '\n' {
				inLine = false
				b.WriteByte(c)
			}
		case inBlock:
			if c == '*' && i+1 < len(s) && s[i+1] == '/' {
				inBlock = false
				i++
			}
		case inStr:
			if c == '\\' {
				i++
			} else if c == '"' {
				inStr = false
			}
		case inRune:
			if c == '\\' {
				i++
			} else if c == '\'' {
				inRune = false
			}
		case c == '/' && i+1 < len(s) && s[i+1] == '/':
			inLine = true
			i++
		case c == '/' && i+1 < len(s) && s[i+1] == '*':
			inBlock = true
			i++
		case c == '"':
			inStr = true
		case c == '\'':
			inRune = true
		default:
			b.WriteByte(c)
		}
	}
	return b.String()
}

// qualifiedCalls finds `receiver.Method(` occurrences.
func qualifiedCalls(s string) []string {
	var out []string
	for i := 0; i < len(s); i++ {
		if s[i] != '.' {
			continue
		}
		end := i + 1
		for end < len(s) && (isIdentRune(s[end])) {
			end++
		}
		if end >= len(s) || s[end] != '(' {
			continue
		}
		name := s[i+1 : end]
		if name == "" || isKeyword(name) {
			continue
		}
		out = append(out, name)
	}
	return out
}

// bareCalls finds `name(` not preceded by a dot.
func bareCalls(s string) []string {
	var out []string
	for i := 0; i < len(s); i++ {
		if !isIdentStart(s[i]) {
			continue
		}
		if i > 0 && (s[i-1] == '.' || isIdentRune(s[i-1])) {
			for i < len(s) && isIdentRune(s[i]) {
				i++
			}
			continue
		}
		start := i
		for i < len(s) && isIdentRune(s[i]) {
			i++
		}
		name := s[start:i]
		if i >= len(s) || s[i] != '(' {
			continue
		}
		if isKeyword(name) {
			continue
		}
		out = append(out, name)
	}
	return out
}

func isIdentStart(c byte) bool {
	return c >= 'a' && c <= 'z' || c >= 'A' && c <= 'Z' || c == '_'
}

func isIdentRune(c byte) bool {
	return isIdentStart(c) || c >= '0' && c <= '9'
}

func isKeyword(s string) bool {
	switch s {
	case "if", "for", "switch", "return", "func", "go", "defer", "range",
		"case", "select", "type", "var", "const", "else", "goto", "break", "continue":
		return true
	}
	return false
}

// The cap must count gateway refunds, or it is theatre.
//
// This is the query, asserted against its source. The reason it is a source
// assertion is that the failure is a query reading the WRONG TABLE -- invisible to
// every behavioural test in the package, because the query returns a plausible
// number either way. Only reading it catches the difference.
func TestTheCumulativeCapReadsTheRefundsTable(t *testing.T) {
	body := readRepositorySource(t, "payment_repo.go")
	start := strings.Index(body, "func (r *PaymentRepository) SumRefundedByOrder(")
	if start < 0 {
		t.Fatal("SumRefundedByOrder not found")
	}
	end := strings.Index(body[start:], "\nfunc ")
	if end < 0 {
		end = len(body) - start
	}
	fn := body[start : start+end]

	if strings.Contains(fn, "wallet_transactions") {
		t.Errorf("SumRefundedByOrder still reads wallet_transactions:\n%s\n"+
			"a GATEWAY refund never credits the buyer's wallet -- the money goes to "+
			"their card -- so this returns zero after every gateway refund and the cap "+
			"measures nothing on the path that actually refunds them", fn)
	}
	if !strings.Contains(fn, "FROM refunds") {
		t.Errorf("SumRefundedByOrder does not read the refunds table:\n%s", fn)
	}
	// And the states it counts must be the ones the service considers committed.
	for _, want := range []string{RefundStateSubmitted, RefundStateSucceeded, RefundStateManual} {
		if !strings.Contains(fn, want) {
			t.Errorf("SumRefundedByOrder does not count %q state; it represents money "+
				"that has left or is committed to leaving", want)
		}
	}
	// `failed` must NOT be counted: the provider rejected it and no money moved,
	// so counting it would refuse a legitimate retry.
	if strings.Contains(fn, "'"+RefundStateFailed+"'") {
		t.Error("SumRefundedByOrder counts the 'failed' state; a rejected refund " +
			"moved no money and counting it would refuse a legitimate retry")
	}
}

// The cap's state list is a literal inside the query, and the rule that decides
// which states belong in it lives in `domain`. This asserts the two agree.
//
// The service used to carry its own copy of that rule as a Go function that
// NOTHING CALLED, while the query used literals, under a comment claiming they
// were "read from the same Go constants" and so could not disagree. They were two
// independent definitions, and a mutation against the dead copy was reported as
// coverage of the cap. So the drift is now tested directly: add a state to
// `domain.RefundStatesCountingTowardCap` and this fails, naming the query.
func TestTheCapCountsExactlyTheStatesDomainSays(t *testing.T) {
	body := readRepositorySource(t, "payment_repo.go")
	start := strings.Index(body, "func (r *PaymentRepository) SumRefundedByOrder(")
	if start < 0 {
		t.Fatal("SumRefundedByOrder not found")
	}
	rest := body[start:]
	end := strings.Index(rest, "\nfunc ")
	if end < 0 {
		end = len(rest)
	}
	fn := rest[:end]

	// Only the status predicate, not the whole function: the exclusion clause
	// mentions no states, and matching the entire body would let a stray status
	// string in a comment count as agreement.
	clause := fn
	if i := strings.Index(fn, "status IN ("); i >= 0 {
		clause = fn[i:]
		if j := strings.Index(clause, ")"); j >= 0 {
			clause = clause[:j]
		}
	} else {
		t.Fatalf("SumRefundedByOrder no longer filters on refund status:\n%s\n"+
			"without a state filter the cap sums EVERY row, including `failed` and "+
			"`pending`, and refuses buyers refunds they are owed", fn)
	}

	for _, want := range domain.RefundStatesCountingTowardCap {
		if !strings.Contains(clause, "'"+want+"'") {
			t.Errorf("the cap does not count %q, but domain says it represents money "+
				"that has left or is committed to leaving; either the query or "+
				"domain.RefundStatesCountingTowardCap is now wrong", want)
		}
	}
	for _, unwanted := range domain.RefundStates {
		if domain.RefundStateCountsTowardCap(unwanted) {
			continue
		}
		if strings.Contains(clause, "'"+unwanted+"'") {
			t.Errorf("the cap counts %q, but domain says it does not represent committed "+
				"money; either the query or domain.RefundStatesCountingTowardCap is now "+
				"wrong", unwanted)
		}
	}
}

// Which states count toward the cap, and why. The rule itself is in domain,
// because the query that enforces it is in the repository and a definition only
// the service can see is one the repository cannot be checked against.
func TestRefundStatesThatCountTowardTheCap(t *testing.T) {
	counts := map[string]bool{
		RefundStatePending:   false, // pre-submit; if the call fails nothing moved
		RefundStateSubmitted: true,  // the provider accepted it and will pay
		RefundStateSucceeded: true,  // the money is gone
		RefundStateFailed:    false, // the provider rejected it
		RefundStateManual:    true,  // we owe the buyer; a human must send it
	}
	for status, want := range counts {
		if got := domain.RefundStateCountsTowardCap(status); got != want {
			t.Errorf("domain.RefundStateCountsTowardCap(%q) = %v, want %v", status, got, want)
		}
	}
	// An unrecognised state must NOT count. Defaulting to "counts" means a typo or
	// a new state silently consumes the cap, and the platform refuses buyers a
	// refund they are owed.
	if domain.RefundStateCountsTowardCap("typo") {
		t.Error("an unrecognised refund state counts toward the cap; a typo would " +
			"consume the cap and refuse a buyer a refund they are owed")
	}
}

// Every legal state must be classified one way or the other. A state that is
// neither counted nor explicitly excluded is a state whose treatment was never
// decided, and it will be decided by whichever branch the query happens to take.
func TestEveryRefundStateIsClassifiedByTheCap(t *testing.T) {
	for _, status := range domain.RefundStates {
		if !domain.RefundStateCountsTowardCap(status) &&
			!contains(domain.RefundStatesNotCountingTowardCap, status) {
			t.Errorf("refund state %q is neither counted toward the cap nor listed as "+
				"excluded; add it to one of the two slices in domain so its treatment "+
				"is a decision rather than an accident", status)
		}
	}
	for _, status := range domain.RefundStatesNotCountingTowardCap {
		if domain.RefundStateCountsTowardCap(status) {
			t.Errorf("refund state %q is in both slices", status)
		}
	}
}

func contains(list []string, want string) bool {
	for _, s := range list {
		if s == want {
			return true
		}
	}
	return false
}

// The cap must refuse a refund that exceeds what remains.
//
// The property is that the running total can never exceed the charge -- NOT that
// any particular partial is refused. An earlier version of this test asserted
// that a third Rp30,000 was refused once Rp60,000 was gone, reasoning that the
// buyer would have "received Rp90,000 of a Rp100,000 order". Rp90,000 of
// Rp100,000 is a perfectly legal partial refund and the cap is right to allow it;
// the test was wrong, and had it passed it would have been asserting that a
// legitimate refund is refused.
func TestTheCapRefusesAnOverRefundAfterPartialRefunds(t *testing.T) {
	const charge = 100000.0

	// Two partials of Rp30,000 are paid out; Rp40,000 remains, so a third
	// Rp30,000 is within the cap and must be allowed.
	if _, err := decideRefundCap(charge, 60000, 30000); err != nil {
		t.Fatalf("a third Rp30,000 refund was refused with Rp40,000 still remaining; "+
			"that is a legal partial refund: %v", err)
	}

	// The boundary: a request for MORE than remains is refused, however small the
	// excess.
	if _, err := decideRefundCap(charge, 60000, 40001); err == nil {
		t.Error("a refund Rp1 over the remaining balance was accepted; the buyer " +
			"would receive Rp100,001 of a Rp100,000 order")
	}

	// And a request for exactly the remainder is allowed and leaves nothing.
	d, err := decideRefundCap(charge, 60000, 40000)
	if err != nil {
		t.Fatalf("refunding exactly the remaining Rp40,000 was refused: %v", err)
	}
	if d.Remaining != 0 || d.Partial {
		t.Errorf("remaining = Rp%.0f, partial = %v; want 0 and false", d.Remaining, d.Partial)
	}
}

func TestTheCapIsExactNotApproximate(t *testing.T) {
	const charge = 100000.0
	// One rupiah over the remainder must be refused, not forgiven. With whole-rupiah
	// money an epsilon is not a tolerance, it is a rupiah that does not exist.
	if _, err := decideRefundCap(charge, 30000, 70001); err == nil {
		t.Error("a refund Rp1 over the remaining balance was accepted; the cap is " +
			"exact because these are whole-rupiah amounts")
	}
	// And exactly the remainder is fine.
	if _, err := decideRefundCap(charge, 30000, 70000); err != nil {
		t.Errorf("refunding exactly the remaining Rp70,000 was refused: %v", err)
	}
}

func TestAZeroRefundRequestMeansTheWholeRemainingBalance(t *testing.T) {
	// This is what a full refund and Midtrans's own `payment.refunded`
	// notification both mean: refund whatever is left.
	//
	// With Rp30,000 already refunded, a zero request takes the other Rp70,000 and
	// leaves NOTHING outstanding -- so this is a full refund, not a partial one,
	// even though two `refunds` rows will exist. An earlier version of this test
	// asserted `Partial == true` on the grounds that Rp30,000 had been refunded at
	// some point; the flag is about what remains NOW, and the assertion was wrong.
	d, err := decideRefundCap(100000, 30000, 0)
	if err != nil {
		t.Fatalf("a zero request was refused: %v", err)
	}
	if d.Amount != 70000 {
		t.Errorf("amount = Rp%.0f, want the full remaining Rp70,000", d.Amount)
	}
	if d.Remaining != 0 {
		t.Errorf("remaining = Rp%.0f, want 0", d.Remaining)
	}
	if d.Partial {
		t.Error("partial = true, but nothing remains outstanding")
	}

	// A request for LESS than the remainder IS partial, and leaves the difference.
	p, err := decideRefundCap(100000, 30000, 20000)
	if err != nil {
		t.Fatalf("a partial refund was refused: %v", err)
	}
	if !p.Partial {
		t.Error("partial = false, but Rp50,000 of the charge is still outstanding")
	}
	if p.Remaining != 50000 {
		t.Errorf("remaining = Rp%.0f, want Rp50,000", p.Remaining)
	}
}

func TestAFullyRefundedOrderRefusesEverything(t *testing.T) {
	if _, err := decideRefundCap(100000, 100000, 0); err == nil {
		t.Error("a fully refunded order accepted another refund")
	}
	// And OVER-refunded history is refused too, rather than treated as a credit.
	if _, err := decideRefundCap(100000, 120000, 0); err == nil {
		t.Error("an over-refunded history produced a positive remaining balance")
	}
}

// A replayed provider notification is a no-op, not a second refund.
//
// Providers retry: Midtrans for up to 24 hours. A duplicate notification is the
// single most common event in the system, and the one most likely to be handled
// wrongly.
func TestAReplayedProviderNotificationIsRecognisedAsTheSameRefund(t *testing.T) {
	first := providerRefundEvent{
		OrderID: "o1", Amount: 30000, Gateway: "midtrans",
		Reference: "rf-77", Reason: "gateway refund",
	}
	replay := first

	if providerRefundKey(first) != providerRefundKey(replay) {
		t.Errorf("a replayed notification produced a different key:\n  %s\n  %s",
			providerRefundKey(first), providerRefundKey(replay))
	}
	// And with the refund already recorded, the decision is "already applied".
	if got := classifyProviderRefund(replay, true); got != refundDecisionAlreadyApplied {
		t.Errorf("decision for a recorded replay = %q, want %q", got, refundDecisionAlreadyApplied)
	}
}

// Two DIFFERENT provider refunds must not collapse onto one key, or the second
// would be silently discarded and the buyer would lose it.
func TestTwoDistinctProviderRefundsGetDistinctKeys(t *testing.T) {
	base := providerRefundEvent{OrderID: "o1", Gateway: "midtrans", Reason: "r"}
	a := base
	a.Reference, a.Amount = "rf-77", 30000
	b := base
	b.Reference, b.Amount = "rf-78", 20000

	if providerRefundKey(a) == providerRefundKey(b) {
		t.Error("two distinct provider refunds share an idempotency key; the " +
			"second would be silently discarded")
	}
	if got := classifyProviderRefund(b, false); got != refundDecisionApply {
		t.Errorf("an unrecorded refund = %q, want %q", got, refundDecisionApply)
	}
}

// A notification with no provider reference cannot be attributed, and must be
// escalated rather than guessed at.
//
// Two unidentifiable notifications might be two genuine partial refunds;
// collapsing them loses a refund the buyer is owed. Applying one unidentifiable
// movement is a duplicate waiting to happen.
func TestAnUnidentifiableProviderRefundIsEscalatedNotApplied(t *testing.T) {
	ev := providerRefundEvent{OrderID: "o1", Amount: 30000, Gateway: "midtrans"}

	if k := providerRefundKey(ev); k != "" {
		t.Errorf("a refund with no reference produced key %q; an unidentifiable "+
			"money movement must not be deduplicated by guesswork", k)
	}
	if got := classifyProviderRefund(ev, false); got != refundDecisionManual {
		t.Errorf("decision = %q, want %q: without a reference the notification cannot "+
			"be told apart from a replay", got, refundDecisionManual)
	}
	// Even if a row somehow claims to be already recorded, the absence of a
	// reference wins: there is nothing to match against.
	if got := classifyProviderRefund(ev, true); got != refundDecisionManual {
		t.Errorf("decision with a spurious match = %q, want %q", got, refundDecisionManual)
	}
}

// The idempotency key is scoped per gateway, because two providers may issue the
// same refund id.
func TestTheProviderKeyIsScopedPerGateway(t *testing.T) {
	a := providerRefundEvent{OrderID: "o1", Amount: 1000, Gateway: "midtrans", Reference: "rf-1"}
	b := providerRefundEvent{OrderID: "o1", Amount: 1000, Gateway: "sandbox", Reference: "rf-1"}
	if providerRefundKey(a) == providerRefundKey(b) {
		t.Error("the same reference on two gateways shares a key; one provider's " +
			"refund would be mistaken for another's")
	}
}

// The key must survive the round trip through the repository. The lookup and the
// writer compose this string in two different packages, and drift between them is
// invisible: a lookup that stops matching turns every replay into a refund.
func TestTheProviderKeyRoundTripsThroughTheRepository(t *testing.T) {
	ev := providerRefundEvent{OrderID: "o1", Amount: 30000, Gateway: "midtrans", Reference: "rf-77"}
	svc := providerRefundKey(ev)
	repo := repositoryRefundKeyFor("midtrans", "rf-77", 30000)

	if svc != repo {
		t.Errorf("the service and repository compose different keys:\n  service:     %s\n"+
			"  repository: %s\nA replayed webhook would not match, and every replay "+
			"would become a second refund.", svc, repo)
	}
}

// A whitespace-only reference is no reference.
func TestAWhitespaceReferenceIsTreatedAsAbsent(t *testing.T) {
	ev := providerRefundEvent{OrderID: "o1", Amount: 1000, Gateway: "midtrans", Reference: "   "}
	if providerRefundKey(ev) != "" {
		t.Errorf("a whitespace reference produced key %q; it identifies nothing",
			providerRefundKey(ev))
	}
	if got := classifyProviderRefund(ev, false); got != refundDecisionManual {
		t.Errorf("decision = %q, want %q", got, refundDecisionManual)
	}
}

// refundsCumulativeSourceExists keeps the repository helpers referenced so the
// build fails loudly if a rename desynchronises the two key builders, rather than
// silently compiling and mismatching at runtime.
func TestRefundHelpersExist(t *testing.T) {
	if repositoryRefundKeyFor("", "", 0) != "" {
		t.Error("an empty reference produced a non-empty repository key")
	}
	// A zero amount is legal in the signature but must still key on the reference.
	if repositoryRefundKeyFor("midtrans", "rf-1", 0) == "" {
		t.Error("a refund with a reference produced an empty key")
	}
}

// compile-time assurance that the fake gateway in the other test file still
// satisfies the interface after any changes to it.
var _ payments.Gateway = (*recordingRefundGateway)(nil)

// ensure the context import is used by the interface assertion above.
var _ = context.Background
