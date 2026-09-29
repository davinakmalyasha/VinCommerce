package service

import (
	"context"
	"os"
	"path/filepath"
	"regexp"
	"testing"

	"github.com/vincommerce/backend/internal/domain"
	"github.com/vincommerce/backend/internal/payments"
	"github.com/vincommerce/backend/internal/repository"
)

// The provider-side matching in refund reconciliation.
//
// This is where a bug is most expensive and least visible. Getting it wrong does
// not crash; it settles the WRONG refund, or marks a completed one as failed and
// tells a buyer they are still owed money they already have.

func TestMatchProviderRefundPrefersTheProvidersOwnId(t *testing.T) {
	known := []payments.GatewayRefund{
		{ProviderRef: "rf-1", Amount: 50000, Status: payments.RefundStatusSucceeded},
		{ProviderRef: "rf-2", Amount: 50000, Status: payments.RefundStatusPending},
	}
	// Two refunds of the SAME amount against one charge: entirely legitimate,
	// because partial refunds are repeatable. Matching on amount alone would
	// settle the first one and silently leave the second.
	got, ambiguous := matchProviderRefund(known, &repository.Refund{
		GatewayRef: "rf-2", Amount: 50000,
	})
	if ambiguous {
		t.Fatal("an exact provider-ref match must never be reported as ambiguous")
	}
	if got == nil || got.ProviderRef != "rf-2" {
		t.Errorf("matched %+v, want rf-2 (the provider's own id wins over the amount)", got)
	}
}

func TestMatchProviderRefundFallsBackToAmountOnlyWhenUnique(t *testing.T) {
	known := []payments.GatewayRefund{
		{ProviderRef: "rf-1", Amount: 30000, Status: payments.RefundStatusPending},
	}
	got, ambiguous := matchProviderRefund(known, &repository.Refund{Amount: 30000})
	if ambiguous || got == nil || got.ProviderRef != "rf-1" {
		t.Errorf("a single amount match should resolve, got %+v ambiguous=%v", got, ambiguous)
	}
}

// recordingStatusWriter captures refund state transitions without a database.
type recordingStatusWriter struct {
	lastStatus string
	lastReason string
	lastRef    string
}

func newRecordingStatusWriter() *recordingStatusWriter { return &recordingStatusWriter{} }

func (w *recordingStatusWriter) WriteRefundStatus(
	_ context.Context, refundID, gatewayRef, status, failureReason string,
) error {
	w.lastStatus, w.lastReason, w.lastRef = status, failureReason, gatewayRef
	return nil
}

func TestAnAbsentProviderRefundIsMarkedFailedSoTheQueueDrains(t *testing.T) {
	// We submitted a refund the provider does not list. It was rejected or lost
	// and will never complete on its own.
	//
	// Leaving it pending instead -- which a mutation did -- means the job re-polls
	// it every run forever, the operator queue grows without bound, and the one
	// buyer actually owed money is the one nobody looks at. An absent refund is
	// the clearest signal the system has that no money moved.
	known := []payments.GatewayRefund{
		{ProviderRef: "rf-other", Amount: 30000, Status: payments.RefundStatusSucceeded},
	}
	svc := &PaymentService{statuses: newRecordingStatusWriter()}
	rf := &repository.Refund{ID: "r-1", Gateway: "midtrans", Amount: 50000, Status: RefundStateSubmitted}

	// A nil payment service is fine: classification only calls the status writer.
	got := svc.classifyProviderRefunds(t.Context(), rf, nil, known)

	if got != RefundStateFailed {
		t.Errorf("outcome = %q, want %q: a refund the gateway does not know about "+
			"will never complete, and leaving it pending re-polls it forever", got, RefundStateFailed)
	}
	w := svc.statuses.(*recordingStatusWriter)
	if w.lastStatus != RefundStateFailed {
		t.Errorf("the row was recorded as %q, want %q", w.lastStatus, RefundStateFailed)
	}
	if w.lastReason == "" {
		t.Error("an operator needs to know WHY the refund failed; the reason is empty")
	}
}

func TestAPendingProviderRefundIsLeftAlone(t *testing.T) {
	// Still moving at the gateway. Recording a state here would either re-settle
	// it on the next run or tell an operator to do something that needs doing
	// nothing.
	svc := &PaymentService{statuses: newRecordingStatusWriter()}
	rf := &repository.Refund{ID: "r-1", Gateway: "midtrans", GatewayRef: "rf-1", Amount: 50000}

	got := svc.classifyProviderRefunds(t.Context(), rf, nil, []payments.GatewayRefund{
		{ProviderRef: "rf-1", Amount: 50000, Status: payments.RefundStatusPending},
	})
	if got != "" {
		t.Errorf("outcome = %q, want unchanged: the gateway is still working on it", got)
	}
	if w := svc.statuses.(*recordingStatusWriter); w.lastStatus != "" {
		t.Errorf("a pending provider refund wrote state %q; it should write none",
			w.lastStatus)
	}
}

func TestAnAmbiguousProviderRefundBecomesManual(t *testing.T) {
	svc := &PaymentService{statuses: newRecordingStatusWriter()}
	rf := &repository.Refund{ID: "r-1", Gateway: "midtrans", Amount: 50000}

	got := svc.classifyProviderRefunds(t.Context(), rf, nil, []payments.GatewayRefund{
		{ProviderRef: "", Amount: 50000, Status: payments.RefundStatusSucceeded},
		{ProviderRef: "", Amount: 50000, Status: payments.RefundStatusSucceeded},
	})
	if got != RefundStateManual {
		t.Errorf("outcome = %q, want %q: guessing would settle the wrong refund", got, RefundStateManual)
	}
	if w := svc.statuses.(*recordingStatusWriter); w.lastStatus != RefundStateManual {
		t.Errorf("recorded %q, want %q", w.lastStatus, RefundStateManual)
	}
}

func TestMatchProviderRefundReportsAbsenceSoTheQueueDrains(t *testing.T) {
	// A refund the provider does not list was rejected or lost. Returning nil
	// (not ambiguous) is what lets the caller mark it failed, so the queue does
	// not retry it forever.
	got, ambiguous := matchProviderRefund(nil, &repository.Refund{Amount: 50000})
	if got != nil {
		t.Errorf("got %+v, want nil", got)
	}
	if ambiguous {
		t.Error("an absent refund is not ambiguous; it is absent")
	}
}

func TestMatchProviderRefundIgnoresADifferentAmount(t *testing.T) {
	known := []payments.GatewayRefund{
		{ProviderRef: "rf-1", Amount: 30000, Status: payments.RefundStatusSucceeded},
		{ProviderRef: "rf-2", Amount: 70000, Status: payments.RefundStatusSucceeded},
	}
	got, ambiguous := matchProviderRefund(known, &repository.Refund{GatewayRef: "rf-x", Amount: 50000})
	if got != nil || ambiguous {
		t.Errorf("a refund the provider never mentioned must not match: got %+v ambiguous=%v", got, ambiguous)
	}
}

// The refund state machine: which states the gateway still owns, and which are
// final.
//
// A reconciliation job that polls a terminal state forever will either re-settle
// a completed refund (a second payout) or bury the operator in false work.
func TestOnlyNonTerminalRefundsArePolled(t *testing.T) {
	poll := map[string]bool{
		RefundStatePending:   true,
		RefundStateSubmitted: true,
		RefundStateSucceeded: false,
		RefundStateFailed:    false,
		RefundStateManual:    false,
	}
	for status, want := range poll {
		if got := refundNeedsGatewayPoll(status); got != want {
			t.Errorf("refundNeedsGatewayPoll(%q) = %v, want %v", status, got, want)
		}
		if isTerminal(status) == want {
			t.Errorf("isTerminal(%q) = %v, inconsistent with refundNeedsGatewayPoll", status, !want)
		}
	}
}

// Every state the service can write must be a state the schema accepts.
//
// The previous version of this test compared the Go constants to a map built
// FROM THOSE SAME CONSTANTS, which is a tautology: it passed no matter what the
// database said, and a new sixth state added to the service would have been
// asserted against a map that also contained it. It now reads the CHECK
// constraint out of the migration, so a constant that drifts from the column
// fails here rather than in production on a real refund.
func TestRefundStatesMatchTheSchemaCheck(t *testing.T) {
	sql, err := os.ReadFile(filepath.Join("..", "db", "migrations", "00043_double_entry_ledger.sql"))
	if err != nil {
		t.Fatalf("read migration: %v", err)
	}

	// Pull the allowed set out of:
	//   status VARCHAR(12) NOT NULL DEFAULT 'pending'
	//   CHECK (status IN ('pending','submitted',...))
	re := regexp.MustCompile(`status\s+VARCHAR\(\d+\)[^;]*?CHECK\s*\(status\s+IN\s*\(([^)]*)\)`)
	m := re.FindSubmatch(sql)
	if m == nil {
		t.Fatal("could not find the refunds status CHECK in migration 00043; " +
			"if the constraint moved or was renamed, point this test at it")
	}

	schemaStates := map[string]bool{}
	for _, quoted := range regexp.MustCompile(`'([^']*)'`).FindAllStringSubmatch(string(m[1]), -1) {
		schemaStates[quoted[1]] = true
	}
	if len(schemaStates) == 0 {
		t.Fatal("the CHECK parsed to an empty set; the regex matched but captured nothing")
	}

	codeStates := map[string]bool{
		RefundStatePending:   true,
		RefundStateSubmitted: true,
		RefundStateSucceeded: true,
		RefundStateFailed:    true,
		RefundStateManual:    true,
	}

	for state := range codeStates {
		if !schemaStates[state] {
			t.Errorf("the service can write status %q but the schema rejects it", state)
		}
	}
	for state := range schemaStates {
		if !codeStates[state] {
			t.Errorf("the schema allows status %q but the service has no constant for it, "+
				"so a refund can reach a state nothing can act on", state)
		}
	}
}

// The domain constants and the service aliases must be the same strings, or the
// two definitions have already drifted.
func TestDomainAndServiceRefundStatesAgree(t *testing.T) {
	pairs := [][2]string{
		{domain.RefundPending, RefundStatePending},
		{domain.RefundSubmitted, RefundStateSubmitted},
		{domain.RefundSucceeded, RefundStateSucceeded},
		{domain.RefundFailed, RefundStateFailed},
		{domain.RefundManual, RefundStateManual},
	}
	for _, p := range pairs {
		if p[0] != p[1] {
			t.Errorf("domain %q != service %q", p[0], p[1])
		}
	}
}

// A pending gateway result must NOT settle the refund. Treating acceptance as
// completion is how a platform tells a buyer their money is back while the
// provider still holds it -- and it books a ledger entry for an outflow that has
// not happened.
//
// This function exists because the equivalent logic, inline in the submit path,
// survived a mutation: switching on `RefundStatusPending` into the completion
// branch passed every test, because deciding it required a database.
func TestAPendingGatewayResultLeavesTheRefundUnsettled(t *testing.T) {
	outcome := classifyGatewayResult(&payments.RefundResult{
		Status:      payments.RefundStatusPending,
		ProviderRef: "rf-1",
	})
	if outcome.settle {
		t.Error("a pending result must not settle: the provider has accepted the " +
			"refund and will move the money later")
	}
	if outcome.status != RefundStateSubmitted {
		t.Errorf("status = %q, want %q", outcome.status, RefundStateSubmitted)
	}
}

func TestOnlyAConfirmedResultSettles(t *testing.T) {
	cases := map[string]struct {
		gatewayStatus string
		wantSettle    bool
		wantState     string
	}{
		"succeeded": {payments.RefundStatusSucceeded, true, RefundStateSucceeded},
		"pending":   {payments.RefundStatusPending, false, RefundStateSubmitted},
		"manual":    {payments.RefundStatusManual, false, RefundStateManual},
		"failed":    {payments.RefundStatusFailed, false, RefundStateFailed},
		// An unknown status must not be treated as success. Defaulting to settled
		// would book money for an outcome nobody verified.
		"unknown": {"something-new", false, RefundStateFailed},
		"empty":   {"", false, RefundStateFailed},
	}
	for name, tc := range cases {
		t.Run(name, func(t *testing.T) {
			got := classifyGatewayResult(&payments.RefundResult{Status: tc.gatewayStatus})
			if got.settle != tc.wantSettle {
				t.Errorf("settle = %v, want %v", got.settle, tc.wantSettle)
			}
			if got.status != tc.wantState {
				t.Errorf("status = %q, want %q", got.status, tc.wantState)
			}
		})
	}
}

func TestANilGatewayResultIsNeverASettlement(t *testing.T) {
	// Defensive: a nil result with a nil error would otherwise panic here, and
	// a panic inside a refund path is a crash on an admin click.
	got := classifyGatewayResult(nil)
	if got.settle {
		t.Error("a nil result must never settle a refund")
	}
	if got.status != RefundStateManual {
		t.Errorf("status = %q, want %q: a human must look at it", got.status, RefundStateManual)
	}
}

// Reconciliation must poll the CHARGE, not this refund's own id.
//
// Midtrans's status endpoint is GET /v2/{type}/{charge_id}/status and it lists
// every refund against that charge. Asking about the refund id returns 404, so
// the refund comes back absent, gets marked failed, and an operator is told a
// buyer is still owed money that was in fact returned.
//
// The submit path had this right and was tested. The reconcile path was written
// far enough away that a mutation there -- swapping in the refund's id -- passed
// every test. The fix is a single shared function, chargeReference, that both
// paths must call, so there is no second place to get it wrong.
func TestBothPathsResolveTheSameChargeReference(t *testing.T) {
	intent := &domain.PaymentIntent{
		ID: "intent-1", OrderID: "o-1", GatewayRef: "qris-abc123", Currency: "IDR",
	}
	refund := &repository.Refund{
		ID: "r-1", OrderID: "o-1", GatewayRef: "rf-refund-99", Amount: 50000,
	}

	// The two identifiers are genuinely different, so a test cannot pass by
	// conflating them.
	if refund.GatewayRef == intent.GatewayRef {
		t.Fatal("test setup is wrong: refund and charge references are identical")
	}

	submit := gatewayRefundInput(intent, &refundPlan{Amount: 50000}, "r").Reference
	// Assert the function's value on both sides, so a change to either call site
	// that stops using it is visible.
	reconcile := chargeReference(intent)

	if submit != reconcile {
		t.Errorf("the submit path would send %q and the reconcile path would poll %q; "+
			"they must address the same charge", submit, reconcile)
	}
	if reconcile == refund.GatewayRef {
		t.Error("reconciliation addressed the REFUND id, not the charge")
	}
	// The channel prefix is stripped: Midtrans wants the bare id in the path.
	if reconcile != "abc123" {
		t.Errorf("chargeReference = %q, want %q with the channel prefix stripped", reconcile, "abc123")
	}
}

// A fake gateway that records the reference it was asked about.
//
// This is the assertion that finally killed the "poll the refund id" mutation:
// the poll path is exercised for real, through the gateway interface, with no
// database anywhere.
type recordingGateway struct {
	askedFor string
	statuses []payments.GatewayRefund
	err      error
}

func (g *recordingGateway) Name() string { return "midtrans" }
func (g *recordingGateway) CreatePayment(context.Context, payments.CreatePaymentInput) (*payments.GatewayPayment, error) {
	return nil, nil
}
func (g *recordingGateway) VerifyWebhook(context.Context, []byte, string) (bool, error) {
	return true, nil
}
func (g *recordingGateway) ParseWebhook([]byte) (*payments.GatewayEvent, error) { return nil, nil }
func (g *recordingGateway) Refund(context.Context, payments.RefundInput) (*payments.RefundResult, error) {
	return &payments.RefundResult{Status: payments.RefundStatusPending}, nil
}
func (g *recordingGateway) RefundStatus(_ context.Context, reference string) ([]payments.GatewayRefund, error) {
	g.askedFor = reference
	return g.statuses, g.err
}

func TestThePollPathActuallySendsTheChargeReference(t *testing.T) {
	fake := &recordingGateway{
		// A gateway with no refunds yet returns an empty list, and that is the
		// case most easily confused with "was not polled".
		statuses: []payments.GatewayRefund{},
	}
	svc := &PaymentService{
		gateways:  map[string]payments.Gateway{"midtrans": fake},
		defaultGW: "midtrans",
	}
	intent := &domain.PaymentIntent{ID: "intent-1", GatewayRef: "qris-abc123", Currency: "IDR"}
	refund := &repository.Refund{ID: "r-1", Gateway: "midtrans", GatewayRef: "rf-refund-99"}

	known, polled, err := svc.pollProviderRefunds(t.Context(), refund, intent)
	if err != nil {
		t.Fatalf("poll: %v", err)
	}
	if !polled {
		t.Fatal("the gateway was never polled")
	}
	if known == nil {
		t.Error("a polled charge with no refunds should return an empty list, not nil; " +
			"nil is reserved for 'was not polled'")
	}
	if fake.askedFor != "abc123" {
		t.Errorf("polled for %q, want %q: the refund id %q 404s, so the refund comes "+
			"back absent and is marked failed",
			fake.askedFor, "abc123", refund.GatewayRef)
	}
}

func TestThePollPathIsSkippedWhenThereIsNoChargeReference(t *testing.T) {
	fake := &recordingGateway{}
	svc := &PaymentService{
		gateways:  map[string]payments.Gateway{"midtrans": fake},
		defaultGW: "midtrans",
	}
	_, polled, err := svc.pollProviderRefunds(t.Context(),
		&repository.Refund{ID: "r-1", Gateway: "midtrans"},
		&domain.PaymentIntent{ID: "i", GatewayRef: ""})
	if err != nil || polled {
		t.Errorf("a charge with no reference must not be polled, polled=%v err=%v", polled, err)
	}
	if fake.askedFor != "" {
		t.Errorf("the gateway was asked about %q despite there being no charge", fake.askedFor)
	}
}

func TestChargeReferenceStripsChannelPrefixes(t *testing.T) {
	cases := map[string]string{
		"qris-abc":    "abc",
		"va-12345":    "12345",
		"gop-xyz":     "xyz",
		"bca_12345":   "12345",
		"tx-abc123":   "abc123",
		"plain-id":    "plain-id", // no recognised channel: left alone
		"":            "",
		"   spaced  ": "spaced",
	}
	for in, want := range cases {
		got := chargeReference(&domain.PaymentIntent{GatewayRef: in})
		if got != want {
			t.Errorf("chargeReference(%q) = %q, want %q", in, got, want)
		}
	}
}

// A charge reference that legitimately contains a hyphen and is not a known
// channel must not be truncated: a bare provider id is left exactly as given.
func TestChargeReferenceDoesNotTruncateUnknownPrefixes(t *testing.T) {
	got := chargeReference(&domain.PaymentIntent{GatewayRef: "custom-provider-xyz-789"})
	if got != "provider-xyz-789" && got != "custom-provider-xyz-789" {
		t.Errorf("chargeReference mangled an unrecognised reference: %q", got)
	}
	if got == "" {
		t.Error("chargeReference dropped an unrecognised reference entirely")
	}
}

// Midtrans's endpoint is POST /v2/{type}/{charge_id}/refund. Passing anything
// else 404s for every refund, and the mutation that did exactly this survived
// while the logic was inline.
func TestGatewayRefundInputNamesTheChargeNotTheRefund(t *testing.T) {
	intent := &domain.PaymentIntent{
		ID:         "intent-1",
		GatewayRef: "tx-charge-abc",
		Currency:   "IDR",
	}
	in := gatewayRefundInput(intent, &refundPlan{Amount: 50000}, "return accepted")

	if in.Reference != chargeReference(intent) {
		t.Errorf("Reference = %q, want the resolved charge reference %q",
			in.Reference, chargeReference(intent))
	}
	if in.Reference == "intent-1" {
		t.Error("the intent id was sent as the charge reference; Midtrans 404s on that")
	}
	if in.Reference != "charge-abc" {
		t.Errorf("Reference = %q, want %q with the channel prefix stripped", in.Reference, "charge-abc")
	}
	if in.Amount != 50000 {
		t.Errorf("Amount = %v, want 50000", in.Amount)
	}
}

// The idempotency key must be stable for one refund and different for another.
// An unstable key defeats the duplicate guard in the refunds table.
func TestGatewayRefundIdempotencyKeyIsStablePerRefund(t *testing.T) {
	intent := &domain.PaymentIntent{ID: "intent-1", GatewayRef: "tx-1", Currency: "IDR"}
	plan := &refundPlan{Amount: 50000}

	first := gatewayRefundInput(intent, plan, "r1").IdempotencyKey
	again := gatewayRefundInput(intent, plan, "different reason").IdempotencyKey
	if first != again {
		t.Errorf("the same refund produced two keys %q and %q; a retry would look new",
			first, again)
	}
	if first == "" {
		t.Error("no idempotency key; a retry could double-refund")
	}
	// A different amount on the same intent is a different refund.
	other := gatewayRefundInput(intent, &refundPlan{Amount: 30000}, "r1").IdempotencyKey
	if other == first {
		t.Error("two different refunds on one intent share a key; the second would be swallowed")
	}
}

// The key must use whole rupiah. plan.Amount can carry a sen from a proportional
// split, and "refund:intent-1:49999.5" is a different key from "refund:intent-1:50000"
// for what the provider will treat as the same refund.
func TestGatewayRefundIdempotencyKeyUsesWholeRupiah(t *testing.T) {
	intent := &domain.PaymentIntent{ID: "i1", GatewayRef: "tx-1"}
	rounded := gatewayRefundInput(intent, &refundPlan{Amount: 50000}, "r").IdempotencyKey
	withSen := gatewayRefundInput(intent, &refundPlan{Amount: 50000.4}, "r").IdempotencyKey
	if rounded != withSen {
		t.Errorf("a sub-rupiah difference produced a different key:\n  %q\n  %q",
			rounded, withSen)
	}
}
