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
	// VerifyWebhook authenticates an incoming webhook payload.
	VerifyWebhook(ctx context.Context, payload []byte, signature string) (bool, error)
	// ParseWebhook extracts the gateway event from a payload.
	ParseWebhook(payload []byte) (*GatewayEvent, error)
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
	RedirectURL string
	Status      string // pending | paid | failed
}

// GatewayEvent is a normalized webhook event.
type GatewayEvent struct {
	Type      string // payment.paid | payment.failed | payment.refunded
	Reference string
	Amount    float64
	Currency  string
	Raw       map[string]any
}

// NewGateway selects the configured adapter.
func NewGateway(name, sandboxBaseURL string) Gateway {
	if name == "" {
		name = "sandbox"
	}
	if name == "sandbox" {
		return &SandboxGateway{BaseURL: sandboxBaseURL, secret: "sandbox-webhook-secret"}
	}
	return &SandboxGateway{BaseURL: sandboxBaseURL, secret: "sandbox-webhook-secret"}
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
