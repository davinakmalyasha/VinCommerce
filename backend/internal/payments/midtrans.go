package payments

import (
	"bytes"
	"context"
	"crypto/sha512"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"math"
	"net/http"
	"strconv"
	"strings"
	"time"
)

// MidtransGateway implements Gateway against Midtrans Snap
// (popup/redirect checkout over QRIS, VA, e-wallets and cards).
type MidtransGateway struct {
	serverKey      string
	apiBase        string
	snapBase       string
	enabledMethods []string // optional channel allow-list; empty = all
	http           *http.Client
}

// NewMidtransGateway builds a Snap adapter. env is "sandbox" or "production".
// enabledMethods optionally restricts checkout channels server-side.
func NewMidtransGateway(serverKey, env string, enabledMethods []string) *MidtransGateway {
	apiBase, snapBase := "https://api.midtrans.com", "https://app.midtrans.com"
	if env != "production" {
		apiBase = "https://api.sandbox.midtrans.com"
		snapBase = "https://app.sandbox.midtrans.com"
	}
	return &MidtransGateway{
		serverKey:      serverKey,
		apiBase:        apiBase,
		snapBase:       snapBase,
		enabledMethods: enabledMethods,
		http:           &http.Client{Timeout: 15 * time.Second},
	}
}

func (g *MidtransGateway) Name() string { return "midtrans" }

// snapItem is one item_details entry. Prices are integer IDR (no minor units).
type snapItem struct {
	ID       string `json:"id"`
	Price    int64  `json:"price"`
	Quantity int    `json:"quantity"`
	Name     string `json:"name"`
}

type snapTransactionDetails struct {
	OrderID     string `json:"order_id"`
	GrossAmount int64  `json:"gross_amount"`
}

type snapExpiry struct {
	Unit     string `json:"unit"`
	Duration int    `json:"duration"`
}

type snapCallbacks struct {
	Finish   string `json:"finish,omitempty"`
	Unfinish string `json:"unfinish,omitempty"`
	Error    string `json:"error,omitempty"`
}

type snapCreateRequest struct {
	TransactionDetails snapTransactionDetails `json:"transaction_details"`
	ItemDetails        []snapItem             `json:"item_details"`
	Expiry             *snapExpiry            `json:"expiry,omitempty"`
	Callbacks          *snapCallbacks         `json:"callbacks,omitempty"`
	EnabledPayments    []string               `json:"enabled_payments,omitempty"`
}

type snapCreateResponse struct {
	StatusCode    string `json:"status_code"`
	StatusMessage string `json:"status_message"`
	Token         string `json:"token"`
	RedirectURL   string `json:"redirect_url"`
}

// CreatePayment opens a Snap transaction. The Midtrans order_id is our
// human-readable order number, which also becomes the intent's gateway_ref so
// notifications resolve back to the order without extra lookups.
func (g *MidtransGateway) CreatePayment(ctx context.Context, in CreatePaymentInput) (*GatewayPayment, error) {
	gross := idrAmount(in.Amount)
	req := snapCreateRequest{
		TransactionDetails: snapTransactionDetails{OrderID: in.OrderNumber, GrossAmount: gross},
		// A single aggregate line keeps the documented invariant
		// sum(item_details.price * qty) == gross_amount trivially true.
		ItemDetails: []snapItem{{
			ID:       in.OrderNumber,
			Price:    gross,
			Quantity: 1,
			Name:     truncateString("Order "+in.OrderNumber, 50),
		}},
		// Expire before the 30-minute inventory reservation window lapses.
		Expiry:    &snapExpiry{Unit: "minutes", Duration: 25},
		Callbacks: &snapCallbacks{Finish: in.ReturnURL, Unfinish: in.ReturnURL, Error: in.ReturnURL},
	}
	if len(g.enabledMethods) > 0 {
		req.EnabledPayments = g.enabledMethods
	}
	body, err := json.Marshal(req)
	if err != nil {
		return nil, err
	}
	httpReq, err := http.NewRequestWithContext(ctx, http.MethodPost, g.snapBase+"/snap/v1/transactions", bytes.NewReader(body))
	if err != nil {
		return nil, err
	}
	httpReq.Header.Set("Content-Type", "application/json")
	httpReq.SetBasicAuth(g.serverKey, "")

	resp, err := g.http.Do(httpReq)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	var out snapCreateResponse
	if err := json.NewDecoder(io.LimitReader(resp.Body, 1<<20)).Decode(&out); err != nil {
		return nil, fmt.Errorf("midtrans: decode response: %w", err)
	}
	if resp.StatusCode >= 300 || out.Token == "" {
		return nil, fmt.Errorf("midtrans: create transaction failed (%s): %s", out.StatusCode, out.StatusMessage)
	}
	return &GatewayPayment{
		Reference:   in.OrderNumber,
		Token:       out.Token,
		RedirectURL: out.RedirectURL,
		Status:      "pending",
	}, nil
}

// snapNotification is the HTTP notification Midtrans POSTs on status changes.
type snapNotification struct {
	OrderID           string `json:"order_id"`
	StatusCode        string `json:"status_code"`
	GrossAmount       string `json:"gross_amount"`
	SignatureKey      string `json:"signature_key"`
	TransactionID     string `json:"transaction_id"`
	TransactionStatus string `json:"transaction_status"`
	FraudStatus       string `json:"fraud_status"`
	PaymentType       string `json:"payment_type"`
}

// VerifyWebhook validates the notification's signature_key:
// SHA512(order_id + status_code + gross_amount + serverKey).
// The signature travels inside the JSON body, so the header argument is unused.
func (g *MidtransGateway) VerifyWebhook(_ context.Context, payload []byte, _ string) (bool, error) {
	n, err := parseNotification(payload)
	if err != nil {
		return false, err
	}
	if n.SignatureKey == "" {
		return false, nil
	}
	expected := SignatureKey(n.OrderID, n.StatusCode, n.GrossAmount, g.serverKey)
	return strings.EqualFold(expected, n.SignatureKey), nil
}

// ParseWebhook normalizes a verified notification into gateway events.
func (g *MidtransGateway) ParseWebhook(payload []byte) (*GatewayEvent, error) {
	n, err := parseNotification(payload)
	if err != nil {
		return nil, err
	}
	if n.OrderID == "" {
		return nil, fmt.Errorf("webhook missing order_id")
	}
	amount, _ := strconv.ParseFloat(n.GrossAmount, 64)
	ev := &GatewayEvent{
		Type:      mapStatus(n.TransactionStatus, n.FraudStatus),
		Reference: n.OrderID,
		Amount:    amount,
		Currency:  "IDR",
		Raw: map[string]any{
			"transaction_id":     n.TransactionID,
			"payment_type":       n.PaymentType,
			"transaction_status": n.TransactionStatus,
			"fraud_status":       n.FraudStatus,
		},
	}
	return ev, nil
}

func parseNotification(payload []byte) (*snapNotification, error) {
	var n snapNotification
	if err := json.Unmarshal(payload, &n); err != nil {
		return nil, err
	}
	return &n, nil
}

// SignatureKey computes Midtrans's webhook signature formula.
func SignatureKey(orderID, statusCode, grossAmount, serverKey string) string {
	h := sha512.Sum512([]byte(orderID + statusCode + grossAmount + serverKey))
	return hex.EncodeToString(h[:])
}

// mapStatus translates Midtrans states onto normalized payment events.
func mapStatus(status, fraud string) string {
	switch status {
	case "settlement":
		return "payment.paid"
	case "capture":
		switch fraud {
		case "accept":
			return "payment.paid"
		case "deny":
			return "payment.failed"
		default: // challenge etc: wait for the next notification
			return "payment.pending"
		}
	case "pending":
		return "payment.pending"
	case "deny", "cancel", "expire":
		return "payment.failed"
	case "refund", "partial_refund":
		return "payment.refunded"
	default:
		return "payment.pending"
	}
}

// idrAmount rounds a float amount to integer IDR rupiah.
func idrAmount(amount float64) int64 { return int64(math.Round(amount)) }

func truncateString(s string, max int) string {
	if len(s) <= max {
		return s
	}
	return s[:max]
}
