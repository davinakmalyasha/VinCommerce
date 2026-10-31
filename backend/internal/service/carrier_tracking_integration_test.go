package service

import (
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"testing"

	"github.com/vincommerce/backend/internal/carrier"
	"github.com/vincommerce/backend/internal/repository"
)

// `MarkShipmentDelivered` was written with a comment saying nothing could reach
// it: there was no shipment webhook and no polling loop, so a parcel stopped at
// dispatched forever. This drives the missing caller against Postgres.
func TestCarrierWebhookAdvancesAParcel(t *testing.T) {
	pool := integrationPool(t)
	ctx := context.Background()

	var orderID, sellerID string
	if err := pool.QueryRow(ctx, `
		SELECT id::text, seller_id::text FROM orders
		 WHERE status IN ('paid', 'delivered') ORDER BY created_at DESC LIMIT 1`).
		Scan(&orderID, &sellerID); err != nil {
		t.Fatalf("no seeded order to attach a parcel to: %v", err)
	}

	shipments := repository.NewShipmentRepository(pool)
	parcel, err := shipments.CreateShipment(ctx, pool, orderID, sellerID, "outbound", "manual")
	if err != nil {
		t.Fatalf("CreateShipment: %v", err)
	}
	const tracking = "MANUAL-TEST-AWB"
	if err := shipments.MarkShipmentShipped(ctx, pool, parcel.ID, tracking); err != nil {
		t.Fatalf("MarkShipmentShipped: %v", err)
	}

	svc := NewShipmentService(
		repository.NewOrderRepository(pool), shipments,
		carrier.NewLabelRegistry(carrier.NewManual()), repository.NewStoreRepository(pool))
	svc.SetCarrierWebhookSecret("carrier-secret")

	deliver := func(payload string) error {
		mac := hmac.New(sha256.New, []byte("carrier-secret"))
		mac.Write([]byte(payload))
		return svc.HandleCarrierWebhook(ctx, "manual", []byte(payload),
			hex.EncodeToString(mac.Sum(nil)))
	}

	if err := deliver(fmt.Sprintf(
		`{"tracking_number":%q,"status":"in_transit","detail":"picked up","event_id":"e1"}`, tracking)); err != nil {
		t.Fatalf("in_transit webhook: %v", err)
	}
	status := func() (string, bool) {
		t.Helper()
		var s string
		var delivered bool
		if err := pool.QueryRow(ctx,
			`SELECT status, delivered_at IS NOT NULL FROM shipments WHERE id = $1::uuid`, parcel.ID).
			Scan(&s, &delivered); err != nil {
			t.Fatalf("read shipment: %v", err)
		}
		return s, delivered
	}
	if s, _ := status(); s != "in_transit" {
		t.Fatalf("after in_transit webhook, shipment = %q", s)
	}

	// A retry of the SAME event must not insert a second row.
	if err := deliver(fmt.Sprintf(
		`{"tracking_number":%q,"status":"in_transit","detail":"picked up","event_id":"e1"}`, tracking)); err != nil {
		t.Fatalf("in_transit retry: %v", err)
	}
	var events int
	if err := pool.QueryRow(ctx,
		`SELECT count(*) FROM shipment_tracking_events WHERE shipment_id = $1::uuid`, parcel.ID).
		Scan(&events); err != nil {
		t.Fatalf("count events: %v", err)
	}
	if events != 1 {
		t.Fatalf("a retried event was stored %d times, want 1", events)
	}

	if err := deliver(fmt.Sprintf(
		`{"tracking_number":%q,"status":"delivered","detail":"received","event_id":"e2"}`, tracking)); err != nil {
		t.Fatalf("delivered webhook: %v", err)
	}
	if s, delivered := status(); s != "delivered" || !delivered {
		t.Fatalf("after delivered webhook, shipment = %q delivered=%v", s, delivered)
	}

	// A late in-transit event (new id, so not a duplicate) must not rewind a
	// parcel that already arrived.
	if err := deliver(fmt.Sprintf(
		`{"tracking_number":%q,"status":"in_transit","detail":"late scan","event_id":"e3"}`, tracking)); err != nil {
		t.Fatalf("late in_transit webhook: %v", err)
	}
	if s, _ := status(); s != "delivered" {
		t.Fatalf("a late in_transit event rewound a delivered parcel to %q", s)
	}
}
