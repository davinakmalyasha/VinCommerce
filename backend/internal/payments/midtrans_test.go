package payments

import (
	"context"
	"crypto/sha512"
	"encoding/hex"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestSignatureKey(t *testing.T) {
	sig := SignatureKey("VC-20260101-0001", "200", "150000.00", "SB-Mid-server-test")
	if len(sig) != sha512.Size*2 {
		t.Fatalf("expected 128 hex chars, got %d", len(sig))
	}
	h := sha512.Sum512([]byte("VC-20260101-0001" + "200" + "150000.00" + "SB-Mid-server-test"))
	if sig != hex.EncodeToString(h[:]) {
		t.Fatal("SignatureKey does not match SHA512(order_id+status_code+gross_amount+serverKey)")
	}
	if sig == SignatureKey("VC-20260101-0001", "200", "150001.00", "SB-Mid-server-test") {
		t.Fatal("signature must change when the gross amount changes")
	}
}

func notificationJSON(t *testing.T, n snapNotification) []byte {
	t.Helper()
	b, err := json.Marshal(n)
	if err != nil {
		t.Fatal(err)
	}
	return b
}

func TestVerifyWebhook(t *testing.T) {
	g := NewMidtransGateway("SB-Mid-server-test", "sandbox", nil)
	valid := snapNotification{
		OrderID: "VC-20260101-0001", StatusCode: "200", GrossAmount: "150000.00",
	}
	valid.SignatureKey = strings.ToUpper(SignatureKey(valid.OrderID, valid.StatusCode, valid.GrossAmount, g.serverKey))

	ctx := context.Background()
	ok, err := g.VerifyWebhook(ctx, notificationJSON(t, valid), "")
	if err != nil || !ok {
		t.Fatalf("valid signature rejected: ok=%v err=%v", ok, err)
	}

	tampered := valid
	tampered.GrossAmount = "999999.00"
	ok, _ = g.VerifyWebhook(ctx, notificationJSON(t, tampered), "")
	if ok {
		t.Fatal("tampered amount accepted")
	}

	noSig := valid
	noSig.SignatureKey = ""
	ok, _ = g.VerifyWebhook(ctx, notificationJSON(t, noSig), "")
	if ok {
		t.Fatal("missing signature accepted")
	}

	if _, err := g.VerifyWebhook(ctx, []byte("{not-json"), ""); err == nil {
		t.Fatal("malformed payload should error")
	}
}

func TestParseWebhookStatusMatrix(t *testing.T) {
	cases := []struct {
		status, fraud, want string
	}{
		{"settlement", "", EventPaid},
		{"capture", "accept", EventPaid},
		{"capture", "deny", EventFailed},
		{"capture", "challenge", EventPending},
		{"pending", "", EventPending},
		{"deny", "", EventFailed},
		{"cancel", "", EventFailed},
		{"expire", "", EventFailed},
		{"refund", "", EventRefunded},
		// A partial refund is a DISTINCT event. This row used to assert
		// `partial_refund -> payment.refunded`, which is precisely the bug:
		// a Rp 10 refund at the gateway triggered a credit of the entire
		// order amount to the buyer's wallet, and flipped the intent to
		// `refunded` so a later legitimate refund could never be applied.
		{"partial_refund", "", EventPartiallyRefunded},
		{"something_new", "", EventPending},
	}
	g := NewMidtransGateway("k", "sandbox", nil)
	for _, tc := range cases {
		n := snapNotification{
			OrderID: "VC-20260101-0002", StatusCode: "200", GrossAmount: "25000.00",
			TransactionStatus: tc.status, FraudStatus: tc.fraud,
			TransactionID: "tx-1", PaymentType: "gopay",
		}
		ev, err := g.ParseWebhook(notificationJSON(t, n))
		if err != nil {
			t.Fatalf("%s/%s: %v", tc.status, tc.fraud, err)
		}
		if ev.Type != tc.want {
			t.Fatalf("%s/%s: got %q want %q", tc.status, tc.fraud, ev.Type, tc.want)
		}
		if ev.Reference != "VC-20260101-0002" || ev.Amount != 25000 || ev.Currency != "IDR" {
			t.Fatalf("unexpected normalized event: %+v", ev)
		}
	}

	ev, err := g.ParseWebhook([]byte(`{"transaction_status":"settlement"}`))
	if err == nil || ev != nil {
		t.Fatal("missing order_id should error")
	}
}

// TestPartialRefundCarriesItsOwnAmount pins the contract that makes the
// Rp-10-refunds-Rp-100.000 bug impossible: a partial_refund notification
// produces a distinct event type, and the amount on the event is the REFUNDED
// amount so the caller can credit exactly that and no more.
func TestPartialRefundCarriesItsOwnAmount(t *testing.T) {
	g := NewMidtransGateway("k", "sandbox", nil)

	full := snapNotification{
		OrderID: "VC-20260101-0002", StatusCode: "200", GrossAmount: "100000.00",
		TransactionStatus: "refund", TransactionID: "tx-1", PaymentType: "qris",
	}
	partial := snapNotification{
		OrderID: "VC-20260101-0002", StatusCode: "200", GrossAmount: "10.00",
		TransactionStatus: "partial_refund", TransactionID: "tx-1", PaymentType: "qris",
	}

	evFull, err := g.ParseWebhook(notificationJSON(t, full))
	if err != nil {
		t.Fatalf("full refund: %v", err)
	}
	if evFull.Type != EventRefunded {
		t.Fatalf("full refund type = %q, want %q", evFull.Type, EventRefunded)
	}
	if evFull.Amount != 100000 {
		t.Fatalf("full refund amount = %v, want 100000", evFull.Amount)
	}

	evPartial, err := g.ParseWebhook(notificationJSON(t, partial))
	if err != nil {
		t.Fatalf("partial refund: %v", err)
	}
	if evPartial.Type != EventPartiallyRefunded {
		t.Fatalf("partial refund type = %q, want %q (a full-refund event here is the bug)",
			evPartial.Type, EventPartiallyRefunded)
	}
	// The whole point: the handler must be able to tell these apart and must
	// see 10, not 100000.
	if evPartial.Amount != 10 {
		t.Fatalf("partial refund amount = %v, want 10", evPartial.Amount)
	}
	if evPartial.Type == evFull.Type {
		t.Fatal("partial and full refunds must not produce the same event type")
	}
}

// TestParseWebhookRejectsMalformedAmount proves a bad gross_amount is a
// hard error rather than a silent 0.0, which the handler then reported as
// AMOUNT_REQUIRED with no hint of the real cause.
func TestParseWebhookRejectsMalformedAmount(t *testing.T) {
	g := NewMidtransGateway("k", "sandbox", nil)
	for _, bad := range []string{"", "abc", "1.2.3", "12,000.00"} {
		n := snapNotification{
			OrderID: "VC-20260101-0002", StatusCode: "200", GrossAmount: bad,
			TransactionStatus: "settlement", TransactionID: "tx-1", PaymentType: "qris",
		}
		if _, err := g.ParseWebhook(notificationJSON(t, n)); err == nil {
			t.Errorf("gross_amount %q was accepted; want error", bad)
		}
	}
}

func TestCreatePayment(t *testing.T) {
	var seen struct {
		auth     string
		orderID  string
		gross    int64
		unit     string
		duration int
		finish   string
		itemSum  int64
	}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/snap/v1/transactions" {
			http.NotFound(w, r)
			return
		}
		user, pass, _ := r.BasicAuth()
		seen.auth = user + ":" + pass
		var req snapCreateRequest
		body, _ := io.ReadAll(r.Body)
		_ = json.Unmarshal(body, &req)
		seen.orderID = req.TransactionDetails.OrderID
		seen.gross = req.TransactionDetails.GrossAmount
		seen.unit = req.Expiry.Unit
		seen.duration = req.Expiry.Duration
		seen.finish = req.Callbacks.Finish
		for _, it := range req.ItemDetails {
			seen.itemSum += it.Price * int64(it.Quantity)
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(snapCreateResponse{StatusCode: "201", Token: "tok-123", RedirectURL: "https://app.sandbox.midtrans.com/snap/v2/vtweb/tok-123"})
	}))
	defer srv.Close()

	g := &MidtransGateway{serverKey: "SB-Mid-server-test", apiBase: srv.URL, snapBase: srv.URL, http: srv.Client()}
	gw, err := g.CreatePayment(context.Background(), CreatePaymentInput{
		OrderID:     "0f0e-uuid",
		OrderNumber: "VC-20260101-0003",
		Amount:      149999.6, // rounds up to integer IDR
		Currency:    "IDR",
		ReturnURL:   "http://localhost:5173/orders/0f0e-uuid",
	})
	if err != nil {
		t.Fatalf("CreatePayment: %v", err)
	}
	if gw.Token != "tok-123" || gw.Reference != "VC-20260101-0003" || gw.Status != "pending" {
		t.Fatalf("unexpected GatewayPayment: %+v", gw)
	}
	if seen.auth != "SB-Mid-server-test:" {
		t.Fatalf("basic auth must be serverKey only, got %q", seen.auth)
	}
	if seen.gross != 150000 || seen.itemSum != seen.gross {
		t.Fatalf("gross/item sum mismatch: gross=%d itemSum=%d", seen.gross, seen.itemSum)
	}
	if seen.unit != "minutes" || seen.duration >= 30 || seen.duration <= 0 {
		t.Fatalf("expiry must be <30 minutes, got %s %d", seen.unit, seen.duration)
	}
	if seen.finish != "http://localhost:5173/orders/0f0e-uuid" {
		t.Fatalf("finish callback not wired to return URL: %q", seen.finish)
	}
}

func TestNewGatewayFactory(t *testing.T) {
	if _, err := NewGateway("", "http://x", "", "", nil); err != nil {
		t.Fatalf("default should be sandbox: %v", err)
	}
	if _, err := NewGateway("midtrans", "", "", "sandbox", nil); err == nil {
		t.Fatal("midtrans without server key must fail closed")
	}
	mt, err := NewGateway("midtrans", "", "SB-Mid-server-test", "sandbox", nil)
	if err != nil || mt.Name() != "midtrans" {
		t.Fatalf("midtrans construction failed: %v", err)
	}
	if _, err := NewGateway("stripe", "http://x", "", "", nil); err == nil {
		t.Fatal("unknown gateway names must fail closed, not fall back to sandbox")
	}
}
