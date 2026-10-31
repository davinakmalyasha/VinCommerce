package service

import (
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"github.com/vincommerce/backend/internal/carrier"
	"github.com/vincommerce/backend/internal/domain"
)

// Carrier tracking ingestion.
//
// `MarkShipmentDelivered` existed with a comment saying nothing could reach it
// because there was no shipment webhook and no polling loop. This is the
// missing caller: a signed carrier payload arrives, the shipment is resolved by
// (carrier, tracking number), the event is recorded idempotently, and a status
// the platform understands advances the parcel.
//
// The secret is per deployment and shared with the carrier/aggregator. An empty
// secret disables the route entirely rather than accepting unsigned events --
// an unauthenticated "delivered" would let anyone mark merchandise received,
// and delivery is what releases escrow.

// SetCarrierWebhookSecret enables carrier tracking ingestion.
func (s *ShipmentService) SetCarrierWebhookSecret(secret string) {
	s.carrierWebhookSecret = secret
}

// carrierEventPayload is the normalized shape every adapter is expected to map
// its own webhook into.
type carrierEventPayload struct {
	TrackingNumber string `json:"tracking_number"`
	Status         string `json:"status"`
	Detail         string `json:"detail"`
	OccurredAt     string `json:"occurred_at"`
	EventID        string `json:"event_id"`
}

// HandleCarrierWebhook verifies and applies one carrier event.
func (s *ShipmentService) HandleCarrierWebhook(
	ctx context.Context, carrierName string, payload []byte, signature string,
) error {
	if strings.TrimSpace(s.carrierWebhookSecret) == "" {
		return domain.E(domain.KindConflict, "CARRIER_WEBHOOK_DISABLED",
			"carrier tracking webhooks are not configured on this server")
	}
	if !verifyHMACSHA256(s.carrierWebhookSecret, payload, signature) {
		return domain.E(domain.KindUnauthenticated, "BAD_SIGNATURE", "invalid webhook signature")
	}

	var in carrierEventPayload
	dec := json.NewDecoder(strings.NewReader(string(payload)))
	dec.DisallowUnknownFields()
	if err := dec.Decode(&in); err != nil {
		return domain.E(domain.KindInvalid, "BAD_WEBHOOK", "malformed carrier payload")
	}
	in.TrackingNumber = strings.TrimSpace(in.TrackingNumber)
	in.Status = strings.ToLower(strings.TrimSpace(in.Status))
	in.Detail = strings.TrimSpace(in.Detail)
	if in.TrackingNumber == "" || in.Status == "" {
		return domain.E(domain.KindInvalid, "BAD_WEBHOOK",
			"carrier payload needs tracking_number and status")
	}

	var occurredAt *time.Time
	if in.OccurredAt != "" {
		parsed, err := time.Parse(time.RFC3339, in.OccurredAt)
		if err != nil {
			return domain.E(domain.KindInvalid, "BAD_WEBHOOK",
				"occurred_at must be RFC3339")
		}
		occurredAt = &parsed
	}

	carrierName = strings.ToLower(strings.TrimSpace(carrierName))
	shipment, err := s.shipments.ShipmentByTracking(ctx, carrierName, in.TrackingNumber)
	if err != nil {
		if domain.Is(err, domain.KindNotFound, "") {
			return domain.E(domain.KindNotFound, "SHIPMENT_NOT_FOUND",
				"no parcel matches that carrier and tracking number")
		}
		return err
	}

	key := in.EventID
	if key == "" {
		// No provider id: derive one from the event itself. Retries carry the
		// same fields, so they collapse; a genuinely later event with the same
		// status but a different timestamp does not.
		sum := sha256.Sum256([]byte(fmt.Sprintf("%s|%s|%s|%s",
			in.Status, in.Detail, in.OccurredAt, in.TrackingNumber)))
		key = hex.EncodeToString(sum[:16])
	}

	tx, err := s.orders.Begin(ctx)
	if err != nil {
		return err
	}
	defer tx.Rollback(ctx)

	inserted, err := s.shipments.InsertTrackingEventTx(
		ctx, tx.PgTx(), shipment.ID, key, in.Status, in.Detail, occurredAt, "webhook")
	if err != nil {
		return err
	}
	if !inserted {
		// The carrier retried a delivery we already recorded. A 2xx stops the
		// retries; moving the parcel again would be acting on an old event.
		return tx.Commit(ctx)
	}

	if mapped, ok := carrier.MapTrackingStatus(in.Status); ok {
		if err := s.shipments.ApplyCarrierStatusTx(ctx, tx.PgTx(), shipment.ID, mapped); err != nil {
			return err
		}
	}
	return tx.Commit(ctx)
}

// verifyHMACSHA256 compares a hex signature in constant time.
func verifyHMACSHA256(secret string, payload []byte, signature string) bool {
	mac := hmac.New(sha256.New, []byte(secret))
	mac.Write(payload)
	expected := hex.EncodeToString(mac.Sum(nil))
	return hmac.Equal([]byte(expected), []byte(strings.ToLower(strings.TrimSpace(signature))))
}
