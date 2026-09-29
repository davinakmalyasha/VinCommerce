package payments

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// Refund tests.
//
// The failure modes these cover are the ones that cost real money in a
// Midtrans integration, and none of them are visible from the happy path:
//
//  1. Refunding by order_id instead of transaction_id. Every order looks paid and
//     every refund 404s.
//  2. Reporting Succeeded when the provider has only accepted the request. The
//     buyer is told they have been refunded days before the money moves.
//  3. Treating a 5xx as a permanent failure, or a conflict as retryable, which
//     between them either lose a refund or double one.
//  4. Accepting a success with no refund_id, which makes the refund impossible to
//     reconcile or to retry safely.

func newRefundGateway(t *testing.T, h http.HandlerFunc) (*MidtransGateway, *httptest.Server) {
	t.Helper()
	srv := httptest.NewServer(h)
	t.Cleanup(srv.Close)
	return &MidtransGateway{
		serverKey: "SB-Mid-server-test",
		apiBase:   srv.URL,
		snapBase:  srv.URL,
		http:      srv.Client(),
	}, srv
}

func TestRefundTargetsTheTransactionNotTheOrder(t *testing.T) {
	var gotPath, gotMethod, gotAuth, gotBody string
	g, _ := newRefundGateway(t, func(w http.ResponseWriter, r *http.Request) {
		gotPath, gotMethod = r.URL.Path, r.Method
		user, pass, _ := r.BasicAuth()
		gotAuth = user + ":" + pass
		body, _ := io.ReadAll(r.Body)
		gotBody = string(body)
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"status_code":"200","status_message":"Refund requested","refund_id":"rf-99","refund_time":"2026-01-02 10:00:00"}`))
	})

	res, err := g.Refund(context.Background(), RefundInput{
		Reference:      "tx-abc123",
		Amount:         50000,
		Reason:         "return accepted",
		IdempotencyKey: "refund:order-1:50000",
	})
	if err != nil {
		t.Fatalf("refund: %v", err)
	}

	// The charge id must be in the path and the order number must not be
	// anywhere: Midtrans resolves /v2/{type}/{id}/refund against transaction_id.
	if !strings.Contains(gotPath, "tx-abc123") {
		t.Errorf("refund path %q does not carry the charge reference", gotPath)
	}
	if gotMethod != http.MethodPost {
		t.Errorf("method = %s, want POST", gotMethod)
	}
	if gotAuth != "SB-Mid-server-test:" {
		t.Errorf("basic auth = %q, want the server key as username", gotAuth)
	}
	// Midtrans takes amount as a STRING. Sending a JSON number is a 400, and the
	// integration docs show it quoted, so this is the shape most likely to be
	// written wrongly.
	if !strings.Contains(gotBody, `"amount":"50000"`) {
		t.Errorf("body %q must send amount as a quoted integer-rupiah string", gotBody)
	}
	if !strings.Contains(gotBody, "return accepted") {
		t.Errorf("body %q must carry the reason", gotBody)
	}
	if res.ProviderRef != "rf-99" {
		t.Errorf("ProviderRef = %q, want the provider's refund id", res.ProviderRef)
	}
}

func TestRefundSuccessIsPendingNotSucceeded(t *testing.T) {
	// A 200 from Midtrans means the refund was ACCEPTED. It does not mean the
	// money has moved, which takes the settlement window. Reporting Succeeded
	// here is how a platform tells a buyer they have been refunded while the
	// money is still theirs.
	g, _ := newRefundGateway(t, func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"status_code":"200","status_message":"ok","refund_id":"rf-1"}`))
	})
	res, err := g.Refund(context.Background(), RefundInput{Reference: "tx-1", Amount: 1000})
	if err != nil {
		t.Fatalf("refund: %v", err)
	}
	if res.Status != RefundStatusPending {
		t.Errorf("status = %q, want %q: a 200 is acceptance, not settlement", res.Status, RefundStatusPending)
	}
}

func TestRefundRejectsSuccessWithoutARefundID(t *testing.T) {
	// Without a refund id there is no way to detect a duplicate on retry, and the
	// uniqueness guard in the refunds table keys on it. Failing here is far
	// better than recording a refund nobody can reconcile.
	g, _ := newRefundGateway(t, func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"status_code":"200","status_message":"ok"}`))
	})
	_, err := g.Refund(context.Background(), RefundInput{Reference: "tx-1", Amount: 1000})
	if err == nil {
		t.Fatal("a success with no refund_id was accepted; a retry would double-refund")
	}
	if !strings.Contains(err.Error(), "refund_id") {
		t.Errorf("the error should name the missing field: %v", err)
	}
}

func TestRefundStatusMatrix(t *testing.T) {
	cases := []struct {
		name       string
		httpStatus int
		body       string
		want       string
		wantNote   string
	}{
		{
			name: "not found names the order-id mistake",
			// This is what a refund by order_id looks like, and the raw 404 tells
			// an operator nothing about why.
			httpStatus: http.StatusNotFound,
			body:       `{"status_code":"404","status_message":"not found"}`,
			want:       RefundStatusFailed,
			wantNote:   "transaction_id",
		},
		{
			name:       "unauthorized is not retryable",
			httpStatus: http.StatusUnauthorized,
			body:       `{"status_code":"401","status_message":"invalid key"}`,
			want:       RefundStatusFailed,
		},
		{
			name: "conflict needs a human",
			// Usually means a refund already went through under another retry.
			// Retrying blindly is how a buyer gets refunded twice.
			httpStatus: http.StatusConflict,
			body:       `{"status_code":"409","status_message":"already refunded","error_messages":["duplicate refund"]}`,
			want:       RefundStatusManual,
			wantNote:   "duplicate refund",
		},
		{
			name:       "too large needs a human",
			httpStatus: http.StatusRequestEntityTooLarge,
			body:       `{"status_code":"413","status_message":"exceeds remaining"}`,
			want:       RefundStatusManual,
		},
		{
			name:       "server error is retryable",
			httpStatus: http.StatusBadGateway,
			body:       `{"status_code":"502","status_message":"bad gateway"}`,
			want:       RefundStatusFailed,
			wantNote:   "retry",
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			g, _ := newRefundGateway(t, func(w http.ResponseWriter, r *http.Request) {
				w.Header().Set("Content-Type", "application/json")
				w.WriteHeader(tc.httpStatus)
				_, _ = w.Write([]byte(tc.body))
			})
			res, err := g.Refund(context.Background(), RefundInput{Reference: "tx-1", Amount: 1000})
			if err != nil {
				t.Fatalf("a rejected refund must return a result, not an error: %v", err)
			}
			if res.Status != tc.want {
				t.Errorf("status = %q, want %q (HTTP %d)", res.Status, tc.want, tc.httpStatus)
			}
			if tc.wantNote != "" && !strings.Contains(res.Message, tc.wantNote) {
				t.Errorf("message %q should mention %q so an operator knows what to do",
					res.Message, tc.wantNote)
			}
		})
	}
}

func TestRefundSurfacesAnUndecodableBodyWithItsHTTPStatus(t *testing.T) {
	// A proxy returning an HTML 502 must not be reported as a JSON decode error:
	// the HTTP status is the part that diagnoses it, and losing it is how a
	// provider outage looks like a code bug.
	g, _ := newRefundGateway(t, func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusBadGateway)
		_, _ = w.Write([]byte("<html>502 Bad Gateway</html>"))
	})
	_, err := g.Refund(context.Background(), RefundInput{Reference: "tx-1", Amount: 1000})
	if err == nil {
		t.Fatal("expected an error")
	}
	if !strings.Contains(err.Error(), "502") {
		t.Errorf("error %q must include the HTTP status", err)
	}
}

func TestRefundValidatesItsInput(t *testing.T) {
	g, _ := newRefundGateway(t, func(w http.ResponseWriter, r *http.Request) {
		t.Error("an invalid refund must not reach the provider")
	})

	for _, tc := range []struct {
		name string
		in   RefundInput
	}{
		{"no reference", RefundInput{Amount: 1000}},
		{"blank reference", RefundInput{Reference: "   ", Amount: 1000}},
		{"zero amount", RefundInput{Reference: "tx-1", Amount: 0}},
		{"negative amount", RefundInput{Reference: "tx-1", Amount: -100}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if _, err := g.Refund(context.Background(), tc.in); err == nil {
				t.Errorf("%s was accepted", tc.name)
			}
		})
	}
}

func TestRefundRejectsAnAmountThatRoundsToZero(t *testing.T) {
	// Rp0.4 rounds to Rp0. Posting it is a 400, and dropping it silently would be
	// a refund that vanishes.
	g, _ := newRefundGateway(t, func(w http.ResponseWriter, r *http.Request) {
		t.Error("a zero-rupiah refund must not reach the provider")
	})
	_, err := g.Refund(context.Background(), RefundInput{Reference: "tx-1", Amount: 0.4})
	if err == nil {
		t.Fatal("an amount that rounds to zero was accepted")
	}
	if !strings.Contains(err.Error(), "zero") {
		t.Errorf("error %q should say the amount rounds to zero", err)
	}
}

func TestRefundResourceTypeIsDerivedFromTheChannel(t *testing.T) {
	// /v2/{type}/{id}/refund uses the payment channel as {type}. Sending "snaps"
	// for a bank transfer 404s against a charge that is plainly paid, so the
	// mapping has to follow the channel.
	cases := map[string]string{
		"va-12345":       "bank_transfer",
		"qris-abc":       "qris",
		"gop-1":          "ewallet",
		"dana-1":         "ewallet",
		"ovo-1":          "ewallet",
		"shopeepay-1":    "ewallet",
		"bca_1":          "bank_transfer",
		"bri_1":          "bank_transfer",
		"mandiri-1":      "bank_transfer",
		"cc_1":           "credit_card",
		"indomaret-1":    "retail",
		"unknown-prefix": "snaps",
		// The channel code is case-insensitive; an uppercase prefix is not a
		// reason to fall through to "snaps" and 404.
		"VA-1":   "bank_transfer",
		"Qris-1": "qris",
	}
	for ref, want := range cases {
		if got := refundResourceType(ref); got != want {
			t.Errorf("refundResourceType(%q) = %q, want %q", ref, got, want)
		}
	}
}

func TestRefundStatusMapsProviderStates(t *testing.T) {
	// Only an explicit success may map to Succeeded. Guessing it for an empty or
	// unknown status marks a refund paid that was never processed.
	cases := map[string]string{
		"success":   RefundStatusSucceeded,
		"SUCCESS":   RefundStatusSucceeded,
		"succeeded": RefundStatusSucceeded,
		"pending":   RefundStatusPending,
		"pending ":  RefundStatusPending,
		"":          RefundStatusManual,
		"weird":     RefundStatusManual,
	}
	for in, want := range cases {
		if got := normaliseRefundStatus(in); got != want {
			t.Errorf("normaliseRefundStatus(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestRefundStatusReadsTheProvidersOwnView(t *testing.T) {
	var gotPath string
	g, _ := newRefundGateway(t, func(w http.ResponseWriter, r *http.Request) {
		gotPath = r.URL.Path
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{
			"transaction_status":"refund",
			"gross_amount":"100000.00",
			"refunds":[
				{"refund_id":"rf-1","refund_amount":"30000.00","refund_status":"success","refund_reason":"partial","refund_time":"2026-01-02 10:00:00"},
				{"refund_id":"rf-2","refund_amount":"70000.00","refund_status":"pending","refund_time":"2026-01-03 11:00:00"}
			]
		}`))
	})

	refunds, err := g.RefundStatus(context.Background(), "tx-abc123")
	if err != nil {
		t.Fatalf("refund status: %v", err)
	}
	if !strings.Contains(gotPath, "tx-abc123") || !strings.HasSuffix(gotPath, "/status") {
		t.Errorf("status path %q should address the charge's status", gotPath)
	}
	if len(refunds) != 2 {
		t.Fatalf("got %d refunds, want 2", len(refunds))
	}
	if refunds[0].Status != RefundStatusSucceeded || refunds[0].Amount != 30000 {
		t.Errorf("refund[0] = %+v, want succeeded 30000", refunds[0])
	}
	// The second is still moving. Reporting it as settled is the bug this whole
	// endpoint exists to prevent.
	if refunds[1].Status != RefundStatusPending {
		t.Errorf("refund[1].Status = %q, want %q", refunds[1].Status, RefundStatusPending)
	}
}

func TestRefundStatusSkipsAnUnparseableAmountWithoutLosingTheRest(t *testing.T) {
	// One bad row must not fail the entire lookup: the caller needs to see the
	// other refunds to reconcile them, and a missing refund is itself a signal.
	g, _ := newRefundGateway(t, func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"refunds":[
			{"refund_id":"rf-bad","refund_amount":"not-a-number","refund_status":"success"},
			{"refund_id":"rf-ok","refund_amount":"1000.00","refund_status":"success"}
		]}`))
	})
	refunds, err := g.RefundStatus(context.Background(), "tx-1")
	if err != nil {
		t.Fatalf("one bad row must not fail the lookup: %v", err)
	}
	if len(refunds) != 1 || refunds[0].ProviderRef != "rf-ok" {
		t.Errorf("got %+v, want just the parseable refund", refunds)
	}
}

func TestGatewayInterfaceIsSatisfiedByEveryAdapter(t *testing.T) {
	// A new adapter that forgets Refund must not compile into the graph and
	// discover it at the first refund.
	var _ Gateway = (*SandboxGateway)(nil)
	var _ Gateway = (*MidtransGateway)(nil)
	// And the status provider, which reconciliation depends on.
	var _ RefundStatusProvider = (*SandboxGateway)(nil)
	var _ RefundStatusProvider = (*MidtransGateway)(nil)
}

func TestSandboxRefundIsNeverSettled(t *testing.T) {
	// The sandbox has no settlement engine. A double that reported Succeeded
	// would let a suite pass against a caller that mishandles the real
	// asynchronous shape of a refund.
	g := &SandboxGateway{}
	res, err := g.Refund(context.Background(), RefundInput{Reference: "sbx_1", Amount: 5000})
	if err != nil {
		t.Fatalf("sandbox refund: %v", err)
	}
	if res.Status != RefundStatusPending {
		t.Errorf("sandbox refund status = %q, want %q", res.Status, RefundStatusPending)
	}
	statuses, err := g.RefundStatus(context.Background(), "sbx_1")
	if err != nil {
		t.Fatalf("sandbox refund status: %v", err)
	}
	for _, s := range statuses {
		if s.Status == RefundStatusSucceeded {
			t.Error("the sandbox reported a settled refund; it has no settlement engine")
		}
	}
}

func TestSandboxRefundCarriesTheIdempotencyKeyThrough(t *testing.T) {
	// The key is how a retry is recognised as a retry. The sandbox echoing it
	// lets a test assert the caller actually supplies a stable one.
	g := &SandboxGateway{}
	res, err := g.Refund(context.Background(), RefundInput{
		Reference: "sbx_1", Amount: 5000, IdempotencyKey: "refund:o1:5000",
	})
	if err != nil {
		t.Fatalf("sandbox refund: %v", err)
	}
	if got, _ := res.Raw["idempotency_key"].(string); got != "refund:o1:5000" {
		t.Errorf("idempotency key = %q, want it recorded so a retry is recognisable", got)
	}
}

func TestRefundResultRawIsAlwaysUsableForReconciliation(t *testing.T) {
	// A refund an operator must reconstruct by hand is a refund that will be
	// disputed. The provider's identifiers have to survive into Raw.
	g, _ := newRefundGateway(t, func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"status_code":"200","status_message":"ok","refund_id":"rf-77","refund_time":"2026-01-02 10:00:00"}`))
	})
	res, err := g.Refund(context.Background(), RefundInput{Reference: "tx-9", Amount: 1234})
	if err != nil {
		t.Fatalf("refund: %v", err)
	}
	for _, key := range []string{"transaction_id", "refund_id", "refund_time", "amount", "status_code"} {
		if _, ok := res.Raw[key]; !ok {
			t.Errorf("Raw is missing %q, which reconciliation needs", key)
		}
	}
	// And the amount recorded must be the integer rupiah actually sent, not the
	// float that was requested, or the settlement line will not foot.
	if got := res.Raw["amount"]; got != int64(1234) {
		t.Errorf("Raw[amount] = %v (%T), want int64(1234)", got, got)
	}
	// Sanity: the raw map is real JSON-decodable data, not a Go-only structure.
	if _, err := json.Marshal(res.Raw); err != nil {
		t.Errorf("Raw is not serialisable: %v", err)
	}
}
