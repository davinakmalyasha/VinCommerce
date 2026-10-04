package payments

import (
	"bytes"
	"context"
	"crypto/hmac"
	"crypto/sha512"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"math"
	"net/http"
	"net/url"
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

// snapRefundRequest is the body of POST /v2/{type}/{id}/refund.
type snapRefundRequest struct {
	// Amount is a string in Midtrans's API even though it is a number, and
	// "0.00" is the documented form. It is sent as an integer-rupiah string.
	Amount string `json:"amount"`
	Reason string `json:"reason,omitempty"`
	// RefundRequestedAt is optional; Midtrans accepts RFC3339. Omitted rather than
	// defaulted, because a wrong timestamp here is harder to explain than none.
	RefundRequestedAt string `json:"refund_requested_at,omitempty"`
}

// snapRefundResponse is the provider's reply.
type snapRefundResponse struct {
	StatusCode    string `json:"status_code"`
	StatusMessage string `json:"status_message"`
	// RefundID identifies THIS refund, distinct from the charge's transaction_id.
	RefundID      string   `json:"refund_id"`
	ErrorMessages []string `json:"error_messages"`
	RefundTime    string   `json:"refund_time"`
}

// snapTransactionStatusResponse is the GET /v2/{id}/status shape, used to confirm
// the resulting state of a charge after a refund is submitted.
type snapTransactionStatusResponse struct {
	TransactionStatus string          `json:"transaction_status"`
	GrossAmount       string          `json:"gross_amount"`
	Refunds           []snapRefundDTO `json:"refunds"`
}

type snapRefundDTO struct {
	RefundID      string `json:"refund_id"`
	RefundAmount  string `json:"refund_amount"`
	RefundStatus  string `json:"refund_status"`
	RefundReason  string `json:"refund_reason"`
	RefundTime    string `json:"refund_time"`
	TransactionID string `json:"transaction_id"`
}

// Refund submits a refund to Midtrans.
//
// Endpoint: POST /v2/{type}/{id}/refund, where {id} MUST be the transaction_id
// from the original charge -- not our order_id. This is the single most common
// integration mistake with this API and it fails confusingly: refunding by
// order_id returns 404 for orders that are plainly paid, and refunding by the
// wrong id moves a real amount against an unrelated transaction.
//
// Success is 200 with status_code "200". The response is NOT a confirmation that
// money has moved: Midtrans settles later, and the durable signal is a
// refund notification. So a successful call maps to RefundStatusPending, and only
// an explicit refund status from a later lookup or notification maps to
// Succeeded. Reporting Succeeded here would let a caller mark a refund done days
// before the money is actually returned.
func (g *MidtransGateway) Refund(ctx context.Context, in RefundInput) (*RefundResult, error) {
	if strings.TrimSpace(in.Reference) == "" {
		return nil, fmt.Errorf("midtrans: refund requires the charge reference (transaction_id)")
	}
	if in.Amount <= 0 {
		return nil, fmt.Errorf("midtrans: refund amount must be positive, got %v", in.Amount)
	}
	amount := idrAmount(in.Amount)
	if amount <= 0 {
		// A sub-rupiah amount rounds to zero. Posting it would be a 400, and
		// silently dropping it would be worse.
		return nil, fmt.Errorf("midtrans: refund amount Rp%v rounds to zero", in.Amount)
	}

	body, err := json.Marshal(snapRefundRequest{
		Amount: strconv.FormatInt(amount, 10),
		Reason: truncateString(in.Reason, 255),
	})
	if err != nil {
		return nil, err
	}
	url := g.apiBase + "/v2/" + refundResourceType(in.Reference) + "/" +
		url.PathEscape(in.Reference) + "/refund"
	httpReq, err := http.NewRequestWithContext(ctx, http.MethodPost, url, bytes.NewReader(body))
	if err != nil {
		return nil, err
	}
	httpReq.Header.Set("Content-Type", "application/json")
	httpReq.Header.Set("Accept", "application/json")
	httpReq.SetBasicAuth(g.serverKey, "")

	resp, err := g.http.Do(httpReq)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	raw, err := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if err != nil {
		return nil, fmt.Errorf("midtrans: read refund response: %w", err)
	}
	var out snapRefundResponse
	// A non-JSON error body must not mask the HTTP status, which is the part
	// that actually diagnoses the failure.
	if err := json.Unmarshal(raw, &out); err != nil {
		return nil, fmt.Errorf("midtrans: refund %s returned HTTP %d with an undecodable body: %w",
			in.Reference, resp.StatusCode, err)
	}

	result := &RefundResult{
		ProviderRef: out.RefundID,
		Message:     out.StatusMessage,
		Raw: map[string]any{
			"http_status":    resp.StatusCode,
			"status_code":    out.StatusCode,
			"status_message": out.StatusMessage,
			"refund_id":      out.RefundID,
			"refund_time":    out.RefundTime,
			"transaction_id": in.Reference,
			"amount":         amount,
		},
	}

	switch {
	case resp.StatusCode == http.StatusOK && out.StatusCode == "200":
		// Accepted, not completed. See the method comment.
		result.Status = RefundStatusPending
	case resp.StatusCode == http.StatusNotFound:
		// The charge id is unknown. This is the order-id-instead-of-transaction-id
		// mistake, so the message says so rather than reporting a bare 404.
		result.Status = RefundStatusFailed
		result.Message = "midtrans: charge not found; the reference must be the " +
			"transaction_id from the original charge, not the order id"
	case resp.StatusCode == http.StatusUnauthorized || resp.StatusCode == http.StatusForbidden:
		// Credentials, not the refund. Retrying will never help.
		result.Status = RefundStatusFailed
		if len(out.ErrorMessages) > 0 {
			result.Message = strings.Join(out.ErrorMessages, "; ")
		}
	case resp.StatusCode == http.StatusRequestEntityTooLarge,
		resp.StatusCode == http.StatusConflict,
		resp.StatusCode == http.StatusUnprocessableEntity:
		// The refund exceeds what is left to refund, or conflicts with an existing
		// one. A human has to look: a conflict is usually a refund that already
		// went through under a different retry.
		result.Status = RefundStatusManual
		if len(out.ErrorMessages) > 0 {
			result.Message = strings.Join(out.ErrorMessages, "; ")
		}
	case resp.StatusCode >= 500:
		// The provider is down or erroring. Retryable, and the money has almost
		// certainly not moved.
		result.Status = RefundStatusFailed
		result.Message = "midtrans: provider error, safe to retry"
	case resp.StatusCode >= 400:
		result.Status = RefundStatusFailed
		if len(out.ErrorMessages) > 0 {
			result.Message = strings.Join(out.ErrorMessages, "; ")
		}
	default:
		result.Status = RefundStatusFailed
	}

	// A success with no refund id is not a success we can reconcile later: the
	// uniqueness guard in the refunds table keys on this value, and without it a
	// retry would create a second refund row for one refund.
	if result.Status == RefundStatusPending && out.RefundID == "" {
		return nil, fmt.Errorf("midtrans: refund accepted for %s but returned no refund_id; "+
			"cannot be reconciled or retried safely", in.Reference)
	}
	return result, nil
}

// RefundStatus reports the authoritative state of every refund against a charge.
//
// This exists because Refund returns Pending and the only thing that ever moves it
// to Succeeded is the provider's own report. Without a way to ask, a refund stays
// pending forever and the reconciliation job has nothing to reconcile against --
// the operator sees a queue of refunds that may or may not have been paid.
//
// It is the endpoint a reconciliation worker polls, and the endpoint an operator
// hits when a refund has been "pending" for longer than the settlement window.
//
// Note this returns the refunds Midtrans knows about, not the ones we submitted.
// A refund we submitted that Midtrans rejected will simply be absent, which is
// exactly the signal needed to move it to failed rather than waiting forever.
func (g *MidtransGateway) RefundStatus(ctx context.Context, reference string) ([]GatewayRefund, error) {
	if strings.TrimSpace(reference) == "" {
		return nil, fmt.Errorf("midtrans: refund status requires the charge reference")
	}
	url := g.apiBase + "/v2/" + refundResourceType(reference) + "/" +
		url.PathEscape(reference) + "/status"
	httpReq, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return nil, err
	}
	httpReq.Header.Set("Accept", "application/json")
	httpReq.SetBasicAuth(g.serverKey, "")

	resp, err := g.http.Do(httpReq)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode == http.StatusNotFound {
		return nil, fmt.Errorf("midtrans: charge %s not found", reference)
	}
	raw, err := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if err != nil {
		return nil, fmt.Errorf("midtrans: read status response: %w", err)
	}
	var out snapTransactionStatusResponse
	if err := json.Unmarshal(raw, &out); err != nil {
		return nil, fmt.Errorf("midtrans: decode status for %s: %w", reference, err)
	}

	refunds := make([]GatewayRefund, 0, len(out.Refunds))
	for _, r := range out.Refunds {
		amount, err := strconv.ParseFloat(r.RefundAmount, 64)
		if err != nil {
			// One unparseable amount must not hide the other refunds; skip it and
			// let the caller's reconciliation notice a refund is missing rather
			// than failing the entire lookup.
			continue
		}
		refunds = append(refunds, GatewayRefund{
			ProviderRef: r.RefundID,
			Amount:      amount,
			Status:      normaliseRefundStatus(r.RefundStatus),
			Reason:      r.RefundReason,
			RequestedAt: r.RefundTime,
		})
	}
	return refunds, nil
}

// normaliseRefundStatus maps Midtrans refund_status values onto ours.
//
// Midtrans reports "pending" and "success"; a refund it has accepted but not yet
// moved stays "pending" for the settlement window. Anything else is a failure we
// did not cause and cannot retry blind, so it is Manual.
func normaliseRefundStatus(s string) string {
	switch strings.ToLower(strings.TrimSpace(s)) {
	case "success", "succeeded":
		return RefundStatusSucceeded
	case "pending":
		return RefundStatusPending
	case "":
		// No status means the provider could not place it. Guessing "succeeded"
		// here would mark a refund paid that was never processed.
		return RefundStatusManual
	default:
		return RefundStatusManual
	}
}

// refundResourceType returns the first path segment of Midtrans's v2 API.
//
// The endpoint is /v2/{type}/{id}/refund, and {type} is the payment channel, not
// an arbitrary string. Sending "snaps" for a bank transfer returns 404 against a
// charge that is plainly paid, so the channel has to be resolved.
//
// It is derived from the transaction_id prefix because that is what Midtrans
// encodes: va-*, qris-*, gop-*, dana-*, bca_*, cc-* and so on. That is a
// heuristic on a string, and the honest thing is to be conservative about it:
//
//   - Separator-insensitive. The channel codes are not consistent about using
//     "-" versus "_" (VA is "va-", several bank codes are "bca_"), and an earlier
//     version of this function matched only one form. Every refund on the
//     unmatched channel then 404s, which is the exact failure this mapping
//     exists to avoid.
//   - Defaults to "snaps", which is the product charges are created through in
//     this integration and which Midtrans resolves to the underlying channel.
//
// A caller that knows the channel from the charge record should pass it rather
// than rely on this; RefundInput has no such override yet, which is a known gap
// rather than a settled design.
func refundResourceType(reference string) string {
	// Only the prefix up to the first separator carries the channel; the rest is
	// the provider's own id.
	head := reference
	if i := strings.IndexAny(reference, "-_"); i >= 0 {
		head = reference[:i]
	}
	switch strings.ToLower(head) {
	case "va", "bsi", "bni", "bca", "bri", "mandiri", "permata", "cimb", "bnc":
		return "bank_transfer"
	case "qris":
		return "qris"
	case "gop", "ovo", "dana", "linkaja", "shopeepay":
		return "ewallet"
	case "indomaret", "alfamart":
		return "retail"
	case "cc":
		return "credit_card"
	default:
		// Snap is the correct default: it is the product the charge was created
		// through in this integration, and Midtrans resolves the underlying
		// channel from the transaction id.
		return "snaps"
	}
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

	// Refund fields.
	//
	// These are ABSENT from the struct this file used to have, and that omission is
	// what made provider refunds unidentifiable. Midtrans sends them alongside the
	// original charge fields on a refund notification; we were dropping them on the
	// floor.
	//
	// `refund_id` is the provider's id for THIS refund and is the only thing that can
	// distinguish one refund from the next on the same payment. Without it the replay
	// guard cannot be keyed, so Midtrans' 24-hour retry of a notification we already
	// applied was indistinguishable from a new refund.
	RefundID     string `json:"refund_id"`
	RefundAmount string `json:"refund_amount"`
	RefundStatus string `json:"refund_status"`
	RefundTime   string `json:"refund_time"`
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
	// Constant-time compare. The previous strings.EqualFold short-circuited on
	// the first differing byte, which is a (weak) timing oracle on the
	// signature; the sandbox adapter already used hmac.Equal, so this also
	// removes an inconsistency between two implementations of one check.
	//
	// Both sides are lowercased first: SHA-512 hex is lowercase in practice,
	// but Midtrans has been observed sending the digest in either case, and
	// normalising before the compare keeps that tolerance without giving up
	// constant time (EqualFold on the raw values is what leaked the timing).
	got := strings.ToLower(strings.TrimSpace(n.SignatureKey))
	return hmac.Equal([]byte(expected), []byte(got)), nil
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
	// A malformed gross_amount silently became 0.0, which the handler then
	// rejected as AMOUNT_REQUIRED. Fails closed, but the swallowed error made
	// gateway misbehaviour undiagnosable — surface it instead.
	amount, err := strconv.ParseFloat(n.GrossAmount, 64)
	if err != nil {
		return nil, fmt.Errorf("webhook gross_amount %q: %w", n.GrossAmount, err)
	}
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

	// Refund notifications carry the REFUNDED amount in `refund_amount`, while
	// `gross_amount` remains the ORIGINAL charge and never changes. `ev.Amount` above
	// is gross_amount, which is why a partial refund has to overwrite it here.
	//
	// Without this, a Rp 30.000 partial refund on a Rp 100.000 charge arrived carrying
	// Amount = 100.000 and the service's `amount > intent.Amount` guard could not fire,
	// because 100.000 is not greater than 100.000. The buyer was paid the whole order
	// back for a partial refund -- the exact bug the `EventPartiallyRefunded` branch in
	// payment_service.go is commented about having fixed. That fix worked only for a
	// hand-written event; this adapter never supplied the field it depended on.
	//
	// Scoped to refund events on purpose: for `settlement` and `capture`, gross_amount
	// IS the payable amount, and `onPaid` compares it against the intent to reject a
	// tampered notification. Substituting here would break that check.
	if ev.Type == EventRefunded || ev.Type == EventPartiallyRefunded {
		// Present in `Raw` unconditionally so the service can key its replay guard on
		// it. It must be a STRING either way: the service does a type assertion, and a
		// missing key and a nil value must not be two different failures.
		ev.Raw["refund_id"] = n.RefundID

		if n.RefundAmount != "" {
			refunded, err := strconv.ParseFloat(n.RefundAmount, 64)
			if err != nil {
				// Surfaced rather than defaulted. Defaulting to 0 would take the
				// `REFUND_AMOUNT_REQUIRED` path in the service, which is safe but
				// reports a missing amount for what is actually an unparseable one.
				return nil, fmt.Errorf("webhook refund_amount %q: %w", n.RefundAmount, err)
			}
			ev.Amount = refunded
		} else if ev.Type == EventPartiallyRefunded {
			// Fail closed. Falling back to gross_amount here would refund the entire
			// charge for a partial refund, which is the whole defect.
			return nil, fmt.Errorf(
				"webhook partial_refund carried no refund_amount; gross_amount is the " +
					"original charge and refunding it would pay out the whole order")
		}
		// EventRefunded with no refund_amount is fine: the service uses the intent
		// amount for a full refund, which is the right figure and needs no field.
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
//
// partial_refund is deliberately a DISTINCT event from refund. Mapping both
// onto one "fully refunded" event meant a Rp 10 partial refund at the gateway
// triggered a credit of the entire order amount to the buyer's wallet, and it
// flipped the intent to `refunded` so a later legitimate refund could never
// be applied at all.
func mapStatus(status, fraud string) string {
	switch status {
	case "settlement":
		return EventPaid
	case "capture":
		switch fraud {
		case "accept":
			return EventPaid
		case "deny":
			return EventFailed
		default: // challenge etc: wait for the next notification
			return EventPending
		}
	case "pending":
		return EventPending
	case "deny", "cancel", "expire":
		return EventFailed
	case "partial_refund":
		return EventPartiallyRefunded
	case "refund":
		return EventRefunded
	default:
		return EventPending
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
