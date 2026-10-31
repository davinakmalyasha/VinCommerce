package service

import (
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"testing"

	"github.com/vincommerce/backend/internal/domain"
)

func sign(secret string, payload []byte) string {
	mac := hmac.New(sha256.New, []byte(secret))
	mac.Write(payload)
	return hex.EncodeToString(mac.Sum(nil))
}

func TestCarrierWebhookRefusesUnsignedAndMisconfigured(t *testing.T) {
	svc := &ShipmentService{}
	payload := []byte(`{"tracking_number":"AWB1","status":"in_transit"}`)

	// Empty secret is DISABLED, not "verification skipped". An unsigned
	// "delivered" would let anyone release escrow for a parcel that never moved.
	svc.SetCarrierWebhookSecret("")
	err := svc.HandleCarrierWebhook(context.Background(), "jne", payload, sign("anything", payload))
	if err == nil || !domain.Is(err, domain.KindConflict, "CARRIER_WEBHOOK_DISABLED") {
		t.Fatalf("unconfigured webhook = %v, want CARRIER_WEBHOOK_DISABLED", err)
	}

	svc.SetCarrierWebhookSecret("top-secret")
	err = svc.HandleCarrierWebhook(context.Background(), "jne", payload, sign("wrong-secret", payload))
	if err == nil || !domain.Is(err, domain.KindUnauthenticated, "BAD_SIGNATURE") {
		t.Fatalf("wrong signature = %v, want BAD_SIGNATURE", err)
	}

	// A body modified after signing must not verify: the HMAC is over the raw
	// bytes the handler read, not over a re-serialised struct.
	tampered := []byte(`{"tracking_number":"AWB2","status":"delivered"}`)
	err = svc.HandleCarrierWebhook(context.Background(), "jne", tampered, sign("top-secret", payload))
	if err == nil || !domain.Is(err, domain.KindUnauthenticated, "BAD_SIGNATURE") {
		t.Fatalf("tampered body = %v, want BAD_SIGNATURE", err)
	}
}

func TestCarrierWebhookRejectsMalformedPayloadBeforeTouchingStorage(t *testing.T) {
	svc := &ShipmentService{}
	svc.SetCarrierWebhookSecret("top-secret")

	cases := map[string]string{
		"missing tracking": `{"status":"in_transit"}`,
		"missing status":   `{"tracking_number":"AWB1"}`,
		"bad timestamp":    `{"tracking_number":"AWB1","status":"in_transit","occurred_at":"yesterday"}`,
		"not json":         `tracking=AWB1`,
		"unknown field":    `{"tracking_number":"AWB1","status":"in_transit","surprise":1}`,
	}
	for name, payload := range cases {
		t.Run(name, func(t *testing.T) {
			err := svc.HandleCarrierWebhook(
				context.Background(), "jne", []byte(payload), sign("top-secret", []byte(payload)))
			if err == nil || !domain.Is(err, domain.KindInvalid, "") {
				t.Fatalf("payload %q = %v, want an invalid-payload error", payload, err)
			}
		})
	}
}
