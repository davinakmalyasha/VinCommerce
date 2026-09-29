package payments

import (
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"time"
)

func jsonUnmarshal(data []byte, v any) error { return json.Unmarshal(data, v) }

// Gateway is the payment provider abstraction (Stripe/Xendit/Midtrans-style).
type Gateway interface {
	// Name identifies the gateway (e.g. "sandbox", "stripe").
	Name() string
	// CreatePayment registers a new charge at the provider.
	CreatePayment(ctx context.Context, in CreatePaymentInput) (*GatewayPayment, error)
	// Refund returns money to the buyer THROUGH the provider.
	//
	// This method did not exist. Every "refund" in this system was an internal
	// book transfer that credited a wallet balance, so a buyer who had paid by
	// QRIS or bank transfer was refunded in platform credit they had no way to
	// spend or withdraw, and the money stayed with us. For Midtrans that is also
	// a terms-of-service breach: refunds must go back through the provider.
	//
	// Refund must be idempotent with respect to in.Idempotency, because it is
	// called from a webhook handler and a worker retry, both of which will
	// re-attempt a refund that the provider may have already processed.
	Refund(ctx context.Context, in RefundInput) (*RefundResult, error)
	// VerifyWebhook authenticates an incoming webhook payload.
	VerifyWebhook(ctx context.Context, payload []byte, signature string) (bool, error)
	// ParseWebhook extracts the gateway event from a payload.
	ParseWebhook(payload []byte) (*GatewayEvent, error)
}

// RefundStatusProvider is implemented by gateways that can report the current
// state of the refunds against a charge.
//
// It is a separate interface from Gateway on purpose. RefundStatus is needed by
// the reconciliation worker and by the admin refund view, neither of which has
// any other use for the rest of Gateway, and folding it in would force every
// adapter and every test double to implement a method that a reconciliation path
// alone uses. A type assertion here fails loudly for a gateway that cannot
// support it rather than silently reporting every refund as pending forever.
type RefundStatusProvider interface {
	// RefundStatus returns the provider's own view of every refund against the
	// charge. A refund this system submitted and the provider does not know about
	// is absent from the result, which is the signal to mark it failed.
	RefundStatus(ctx context.Context, reference string) ([]GatewayRefund, error)
}

// GatewayRefund is one refund as the provider reports it.
type GatewayRefund struct {
	ProviderRef string
	Amount      float64
	Status      string // see the RefundStatus* constants
	Reason      string
	// RequestedAt is the provider's timestamp, which is not our requested_at: a
	// refund can sit queued for a day. Recording both is what makes a slow refund
	// diagnosable.
	RequestedAt string
}

// RefundInput describes a refund to submit to the provider.
//
// Reference is the provider's own identifier for the CHARGE being refunded, not
// our order number and not a refund id. Midtrans's API is
// POST /v2/{type}/{id}/refund where {id} is the transaction id from the original
// charge, and passing the wrong identifier means refunding a different
// transaction or a 404 that an operator has to chase.
type RefundInput struct {
	// Reference is the provider's charge identifier (Midtrans transaction_id).
	Reference string
	Amount    float64 // partial refunds are legitimate and repeatable
	Currency  string
	Reason    string
	// IdempotencyKey makes a retry safe. Required: the callers are a webhook
	// handler and a worker retry, and a duplicated refund is a real duplicate
	// outflow of money.
	IdempotencyKey string
}

// Refund outcome states.
//
// The distinction that matters is Pending vs Succeeded. A refund that was
// accepted for processing has NOT yet moved money, and treating it as done is how
// a marketplace ends up promising a buyer money it never sent. Failed and Manual
// are separate because a retry is correct for one and useless for the other:
// a provider rejection is safe to resubmit, whereas a refund the provider cannot
// perform at all needs a human to move the money out of band.
const (
	RefundStatusSucceeded = "succeeded"
	RefundStatusPending   = "pending"
	RefundStatusFailed    = "failed"
	// RefundStatusManual means the platform owes the buyer but the provider will
	// not or cannot move it. A real, expected state, not an error to hide.
	RefundStatusManual = "manual"
)

// RefundResult is the provider's response to a refund request.
type RefundResult struct {
	// ProviderRef is the provider's identifier for THIS refund, which is
	// distinct from the charge reference. Recording it is what stops the same
	// refund being submitted twice.
	ProviderRef string
	Status      string // see the RefundStatus* constants
	Message     string
	// Raw is the provider's response, retained for reconciliation. A refund that
	// an operator has to reconstruct by hand is a refund that will be.
	Raw map[string]any
}

// SandboxGateway's RefundStatus always reports Pending, mirroring what its
// Refund returns: the sandbox has no settlement engine, so a refund there is
// never actually finished. A double that returned Succeeded would let a test
// suite pass against a caller that cannot handle the real asynchronous shape.
func (g *SandboxGateway) RefundStatus(ctx context.Context, reference string) ([]GatewayRefund, error) {
	return []GatewayRefund{{
		ProviderRef: "sbxrf_pending",
		Amount:      0,
		Status:      RefundStatusPending,
		Reason:      "sandbox refunds never settle",
	}}, nil
}

// CreatePaymentInput describes the charge to create.
type CreatePaymentInput struct {
	OrderID     string
	OrderNumber string
	Amount      float64
	Currency    string
	BuyerEmail  string
	Idempotency string
	ReturnURL   string
}

// GatewayPayment is the provider's response.
type GatewayPayment struct {
	Reference   string
	Token       string // provider checkout token (e.g. Midtrans Snap token)
	RedirectURL string
	Status      string // pending | paid | failed
}

// Gateway event types.
const (
	EventPaid              = "payment.paid"
	EventPending           = "payment.pending"
	EventFailed            = "payment.failed"
	EventRefunded          = "payment.refunded"
	EventPartiallyRefunded = "payment.partially_refunded"
)

// GatewayEvent is a normalized webhook event.
type GatewayEvent struct {
	Type      string // see the Event* constants
	Reference string
	// Amount is the amount the notification is about. For a partial refund
	// this is the REFUNDED amount, not the original charge, so the handler
	// must not treat it as the order total.
	Amount   float64
	Currency string
	Raw      map[string]any
}

// NewGateway selects the configured adapter. Unknown names fail closed
// instead of silently falling back to the sandbox provider.
func NewGateway(name, sandboxBaseURL, midtransServerKey, midtransEnv string, midtransMethods []string) (Gateway, error) {
	switch name {
	case "", "sandbox":
		return &SandboxGateway{BaseURL: sandboxBaseURL, secret: "sandbox-webhook-secret"}, nil
	case "midtrans":
		if midtransServerKey == "" {
			return nil, fmt.Errorf("PAYMENT_GATEWAY=midtrans requires MIDTRANS_SERVER_KEY")
		}
		return NewMidtransGateway(midtransServerKey, midtransEnv, midtransMethods), nil
	default:
		return nil, fmt.Errorf("unknown payment gateway %q", name)
	}
}

// SandboxGateway simulates a real provider for local development.
// Flow: create -> pending with a payment URL -> buyer hits /approve -> webhook -> paid.
type SandboxGateway struct {
	BaseURL string
	secret  string
}

func (g *SandboxGateway) Name() string { return "sandbox" }

func (g *SandboxGateway) CreatePayment(ctx context.Context, in CreatePaymentInput) (*GatewayPayment, error) {
	ref := fmt.Sprintf("sbx_%d_%s", time.Now().UnixNano(), shortRef(in.OrderID))
	return &GatewayPayment{
		Reference:   ref,
		RedirectURL: fmt.Sprintf("%s/sandbox/pay/%s", g.BaseURL, ref),
		Status:      "pending",
	}, nil
}

// Refund simulates a provider refund.
//
// It returns Pending rather than Succeeded on purpose. The sandbox's job is to
// exercise the real async shape of a refund: a provider that immediately reports
// "done" lets a caller that treats Pending as Succeeded pass every test and then
// fail against Midtrans, where settlement takes T+1 to T+7. Returning Pending here
// is what forces the caller to have a reconciliation path.
func (g *SandboxGateway) Refund(ctx context.Context, in RefundInput) (*RefundResult, error) {
	if in.Reference == "" {
		return nil, fmt.Errorf("sandbox refund requires the charge reference")
	}
	if in.Amount <= 0 {
		return nil, fmt.Errorf("sandbox refund amount must be positive")
	}
	return &RefundResult{
		ProviderRef: fmt.Sprintf("sbxrf_%d_%s", time.Now().UnixNano(), shortRef(in.Reference)),
		Status:      RefundStatusPending,
		Message:     "sandbox refund accepted for processing",
		Raw: map[string]any{
			"charge_ref":      in.Reference,
			"amount":          in.Amount,
			"idempotency_key": in.IdempotencyKey,
			"refund_status":   "pending",
		},
	}, nil
}

func (g *SandboxGateway) VerifyWebhook(ctx context.Context, payload []byte, signature string) (bool, error) {
	mac := hmac.New(sha256.New, []byte(g.secret))
	mac.Write(payload)
	expected := hex.EncodeToString(mac.Sum(nil))
	return hmac.Equal([]byte(expected), []byte(signature)), nil
}

func (g *SandboxGateway) ParseWebhook(payload []byte) (*GatewayEvent, error) {
	var ev struct {
		Type      string  `json:"type"`
		Reference string  `json:"reference"`
		Amount    float64 `json:"amount"`
		Currency  string  `json:"currency"`
	}
	if err := jsonUnmarshal(payload, &ev); err != nil {
		return nil, err
	}
	if ev.Reference == "" {
		return nil, fmt.Errorf("webhook missing reference")
	}
	return &GatewayEvent{
		Type:      ev.Type,
		Reference: ev.Reference,
		Amount:    ev.Amount,
		Currency:  ev.Currency,
	}, nil
}

// SignForDev computes the webhook signature for a payload (dev driver tooling).
func (g *SandboxGateway) SignForDev(payload []byte) string {
	mac := hmac.New(sha256.New, []byte(g.secret))
	mac.Write(payload)
	return hex.EncodeToString(mac.Sum(nil))
}

func shortRef(s string) string {
	if len(s) > 8 {
		return s[:8]
	}
	return s
}
