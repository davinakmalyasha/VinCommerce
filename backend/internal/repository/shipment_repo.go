package repository

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/vincommerce/backend/internal/db"
	"github.com/vincommerce/backend/internal/domain"
)

// ShipmentRepository owns parcels: how an order came to be more than one box.
//
// THE INVARIANT THIS FILE EXISTS TO PROTECT
//
// The sum of shipment_items.quantity for an order_item, across every live
// shipment, must never exceed order_items.quantity. Not "should not" -- never.
//
// The seller is the one supplying these numbers. Nothing in the schema stops a
// parcel claiming four units of a line that was bought as three, and if that
// happens the seller is told to ship four, the buyer is told four are coming, and
// the extra unit has no order behind it.
//
// Three things enforce it, and they are not interchangeable:
//
//   1. A CHECK on order_items (shipped_quantity <= quantity). The invariant, in
//      the database. It only fires when the ITEM row is written, so on its own it
//      would let two concurrent parcels each pass it and jointly overshoot.
//
//   2. The conditional UPDATE below. `WHERE shipped_quantity + $2 <= quantity` --
//      its zero rows-affected IS the refusal, decided atomically against the row
//      lock, so two concurrent parcels cannot both be admitted. This is the
//      mechanism.
//
//   3. The transaction. Reservation, shipment_items insert and the shipment row
//      all commit together or not at all.
//
// A UNIQUE constraint cannot help: a unique can say "no two rows alike", not "no
// two rows summing too high". That is stated here so nobody reaches for one.

// Shipment is one parcel.
type Shipment struct {
	ID          string
	OrderID     string
	SellerID    string
	Sequence    int
	Kind        string
	Status      string
	Carrier     string
	Service     string
	CarrierRef  string
	Tracking    string
	LabelFormat string
	LabelURL    string
	WeightGrams int
	ShippedAt   *time.Time
	DeliveredAt *time.Time
	LastEventAt *time.Time
	CreatedAt   time.Time
	UpdatedAt   time.Time
}

// Shipment statuses, from the CHECK in 00050.
const (
	ShipmentReady        = "ready"
	ShipmentLabelCreated = "label_created"
	ShipmentInTransit    = "in_transit"
	ShipmentDelivered    = "delivered"
	ShipmentException    = "exception"
	ShipmentCancelled    = "cancelled"
	ShipmentVoided       = "voided"
)

// Shipment kinds. 'return' carries a return label and travels buyer -> seller.
const (
	ShipmentOutbound = "outbound"
	ShipmentReturn   = "return"
)

// ShipmentItemLine is what a seller says goes in a parcel.
type ShipmentItemLine struct {
	OrderItemID string
	Quantity    int
}

// ErrWouldOvership is returned when a parcel would ship more than was bought.
var ErrWouldOvership = domain.E(domain.KindConflict, "SHIPMENT_WOULD_OVERSHIP",
	"this parcel claims more units of a line than were bought; a line of quantity "+
		"3 cannot have 4 units shipped across all parcels")

// ShipmentRepository reads and writes parcels.
type ShipmentRepository struct {
	pool *db.Pool
}

// NewShipmentRepository creates a ShipmentRepository.
func NewShipmentRepository(pool *db.Pool) *ShipmentRepository {
	return &ShipmentRepository{pool: pool}
}

// Pool exposes the pool for callers that need it.
func (r *ShipmentRepository) Pool() *db.Pool { return r.pool }

const shipmentColumns = `id::text, order_id::text, seller_id::text, sequence, kind,
	status, carrier, COALESCE(carrier_service,''), COALESCE(carrier_shipment_id,''),
	COALESCE(tracking_number,''), COALESCE(label_format,''), COALESCE(label_url,''),
	weight_grams, shipped_at, delivered_at, last_event_at, created_at, updated_at`

func scanShipment(row pgx.Row) (*Shipment, error) {
	var s Shipment
	err := row.Scan(&s.ID, &s.OrderID, &s.SellerID, &s.Sequence, &s.Kind, &s.Status,
		&s.Carrier, &s.Service, &s.CarrierRef, &s.Tracking, &s.LabelFormat,
		&s.LabelURL, &s.WeightGrams, &s.ShippedAt, &s.DeliveredAt, &s.LastEventAt,
		&s.CreatedAt, &s.UpdatedAt)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, domain.ErrNotFound
	}
	if err != nil {
		return nil, err
	}
	return &s, nil
}

// isUniqueViolation reports whether err is a Postgres unique-constraint violation.
//
// 23505 is SQLSTATE unique_violation. The codebase inlines `pgErr.Code == "23505"` in
// several places; this is a local helper rather than a shared one so the change stays
// scoped to the parcel allocation.
func isUniqueViolation(err error) bool {
	var pgErr *pgconn.PgError
	return errors.As(err, &pgErr) && pgErr.Code == "23505"
}

// CreateShipment opens a new parcel for an order.
//
// `sequence` is derived from the order's existing parcels rather than accepted from
// the caller: two parcels both claiming to be the first would either fail the
// UNIQUE (order_id, kind, sequence) -- which is safe but is a confusing error for
// a seller who just double-tapped -- or, worse, be renumbered by a retry and leave
// a hole in the audit trail. The number is a fact about the order, not a request.
//
// THE ALLOCATION IS LOCKED, BECAUSE READ-THEN-INSERT IS A LOST UPDATE
//
//	SELECT MAX(sequence) + 1 ...   -- both transactions read 0
//	INSERT ... VALUES (1)           -- both write 1
//
// Under READ COMMITTED neither transaction sees the other's uncommitted row, so both
// compute sequence = 1. The UNIQUE index then makes the second one fail -- after it has
// already blocked on the first. Nothing is corrupted, but the seller who double-tapped
// gets a raw database error naming an index, not a domain error he can act on, and the
// block duration is however long the first parcel's transaction runs.
//
// A transaction-scoped advisory lock on (order_id, kind) serialises just this
// allocation: two sellers parcelling DIFFERENT orders never contend, and the row that
// the sequence is derived from is protected for exactly as long as the parcel insert.
//
// The lock is keyed on TWO values rather than one hash of the pair, so an order whose
// outbound and return parcels are created concurrently take two different locks. A
// single `hashtext(orderID || kind)` would serialise them against each other, which is
// merely wasteful, while a single `hashtext(orderID)` alone would be sufficient for
// safety. Two keys is chosen because it documents the intent: the sequence space is
// per (order, kind).
//
// REQUIRES A TRANSACTION. `pg_advisory_xact_lock` outside an explicit transaction
// block is released immediately, so passing a pool here would silently restore the old
// race. Every caller passes a transaction -- CreateParcel and CreateReturnParcel both
// wrap this in the order transaction -- and `TestCreateShipmentRequiresATransaction`
// pins that.
func (r *ShipmentRepository) CreateShipment(
	ctx context.Context, q Querier, orderID, sellerID, kind, carrier string,
) (*Shipment, error) {
	if kind == "" {
		kind = ShipmentOutbound
	}
	if strings.TrimSpace(carrier) == "" {
		// 'manual' is a real carrier, not a placeholder: COD and local deliveries
		// routinely have no AWB, and those parcels must still be recordable.
		carrier = "manual"
	}

	if _, err := q.Exec(ctx,
		`SELECT pg_advisory_xact_lock(hashtext($1), hashtext($2))`, orderID, kind); err != nil {
		return nil, err
	}

	var nextSeq int
	if err := q.QueryRow(ctx, `
		SELECT COALESCE(MAX(sequence), 0) + 1
		  FROM shipments WHERE order_id = $1 AND kind = $2`, orderID, kind).Scan(&nextSeq); err != nil {
		return nil, err
	}

	var id string
	if err := q.QueryRow(ctx, `
		INSERT INTO shipments (order_id, seller_id, sequence, kind, carrier)
		VALUES ($1::uuid, $2::uuid, $3, $4, $5)
		RETURNING id::text`, orderID, sellerID, nextSeq, kind, carrier).Scan(&id); err != nil {
		// With the lock held a UNIQUE violation here means something outside this
		// function wrote a parcel for the order while we waited -- a migration, a
		// manual fix, or a second code path. Surface it as a conflict the caller can
		// retry rather than as a raw constraint name, because the seller-facing
		// message for "double-tapped" is the whole reason this lock exists.
		if isUniqueViolation(err) {
			return nil, domain.E(domain.KindConflict, "PARCEL_SEQUENCE_TAKEN",
				"another parcel for this order was created at the same moment; "+
					"reload the order and try again")
		}
		return nil, err
	}
	return r.ShipmentByID(ctx, q, id)
}

// AddShipmentItems puts lines in a parcel, refusing to overship.
//
// THE ORDER OF OPERATIONS IS THE POINT. Each line is reserved FIRST with the
// conditional UPDATE, and only a line that reserved cleanly gets a shipment_items
// row. Reserving after inserting would leave a row claiming units that were never
// reserved, and the two tables would disagree.
//
// The whole thing runs in the caller's transaction, so a refusal part-way through
// a multi-line parcel rolls back the lines already reserved.
func (r *ShipmentRepository) AddShipmentItems(
	ctx context.Context, q Querier, shipmentID string, lines []ShipmentItemLine,
) error {
	if len(lines) == 0 {
		return domain.E(domain.KindInvalid, "SHIPMENT_EMPTY",
			"a parcel with nothing in it is not a parcel; record the weight and "+
				"tracking instead if this is a carrier collection")
	}
	seen := map[string]bool{}
	for _, l := range lines {
		if l.Quantity <= 0 {
			return domain.E(domain.KindInvalid, "SHIPMENT_ITEM_QUANTITY",
				"a shipment line must carry at least one unit")
		}
		if seen[l.OrderItemID] {
			// The PRIMARY KEY (shipment_id, order_item_id) would reject the second
			// row, but with a constraint error that reads like a bug. Merging is
			// what the seller meant: "4 of this line", typed as two lines.
			return domain.E(domain.KindInvalid, "SHIPMENT_ITEM_DUPLICATE",
				"the same order line appears twice in one parcel; put its units in a "+
					"single line, since a parcel holds one quantity per line")
		}
		seen[l.OrderItemID] = true
	}

	for _, l := range lines {
		// The mechanism. Zero rows affected means the predicate was false: either
		// the line is already fully shipped, or this parcel would push it past
		// quantity. Both are the seller being wrong about what is left, and both
		// are refusals rather than clamps -- clamping would silently ship a
		// different quantity than the one recorded on the parcel.
		tag, err := q.Exec(ctx, `
			UPDATE order_items
			   SET shipped_quantity = shipped_quantity + $2
			 WHERE id = $1::uuid
			   AND status NOT IN ('cancelled', 'returned')
			   AND shipped_quantity + $2 <= quantity`,
			l.OrderItemID, l.Quantity)
		if err != nil {
			return err
		}
		if tag.RowsAffected() == 0 {
			return fmt.Errorf("%w: line %s, %d unit(s)", ErrWouldOvership, l.OrderItemID, l.Quantity)
		}

		if _, err := q.Exec(ctx, `
			INSERT INTO shipment_items (shipment_id, order_item_id, quantity)
			VALUES ($1::uuid, $2::uuid, $3)
			ON CONFLICT (shipment_id, order_item_id) DO NOTHING`,
			shipmentID, l.OrderItemID, l.Quantity); err != nil {
			return err
		}
	}
	return nil
}

// RefreshShipmentWeight recomputes a parcel's weight from what is in it.
//
// DERIVED, NEVER ACCEPTED. A weight the seller types is a weight the carrier
// charges for and a weight the parcel is measured against; if they disagree the
// parcel is billed wrong and any surcharge lands on the buyer for a reason nobody
// can reconstruct. `weight_grams` exists for the carrier API and for rate
// shopping, and its only writer is this.
func (r *ShipmentRepository) RefreshShipmentWeight(ctx context.Context, q Querier, shipmentID string) error {
	_, err := q.Exec(ctx, `
		UPDATE shipments s
		   SET weight_grams = COALESCE(agg.grams, 0)
		  FROM (
		      SELECT SUM(oi.weight_grams * si.quantity) AS grams
		        FROM shipment_items si
		        JOIN order_items oi ON oi.id = si.order_item_id
		       WHERE si.shipment_id = $1
		  ) agg
		 WHERE s.id = $1::uuid`, shipmentID)
	return err
}

// MarkShipmentShipped records hand-over to the carrier.
//
// Guarded on `status = 'label_created'`, because a parcel with no label that goes
// 'shipped' has skipped the step that produces the tracking number the buyer will
// be shown. 'ready' -> 'shipped' is allowed for `manual`, where there is no label
// to buy -- so the predicate permits 'ready' when the carrier is manual, and the
// two cases are distinguished rather than one of them being quietly dropped.
func (r *ShipmentRepository) MarkShipmentShipped(
	ctx context.Context, q Querier, shipmentID, tracking string,
) error {
	tag, err := q.Exec(ctx, `
		UPDATE shipments
		   SET status = 'in_transit',
		       tracking_number = COALESCE(NULLIF($2, ''), tracking_number),
		       shipped_at = COALESCE(shipped_at, now())
		 WHERE id = $1::uuid
		   AND (status = 'label_created' OR (status = 'ready' AND carrier = 'manual'))
		   AND delivered_at IS NULL`, shipmentID, tracking)
	if err != nil {
		return err
	}
	if tag.RowsAffected() == 0 {
		return domain.E(domain.KindConflict, "SHIPMENT_NOT_DISPATCHABLE",
			"this parcel is not in a state that can be handed over; a parcel needs a "+
				"label first, unless the carrier is manual and there is no AWB to buy")
	}
	return nil
}

// MarkShipmentDelivered records arrival.
//
// Guarded on `delivered_at IS NULL` so a repeated delivery callback is a no-op
// rather than a second arrival, and on the status being something that can
// actually still arrive. 'exception' is deliberately INCLUDED: a carrier that
// reported a problem usually re-delivers, and refusing the later successful
// delivery would leave a parcel stuck in a failure state forever.
func (r *ShipmentRepository) MarkShipmentDelivered(ctx context.Context, q Querier, shipmentID string) error {
	tag, err := q.Exec(ctx, `
		UPDATE shipments
		   SET status = 'delivered', delivered_at = now()
		 WHERE id = $1::uuid
		   AND delivered_at IS NULL
		   AND status IN ('ready', 'label_created', 'in_transit', 'exception')`,
		shipmentID)
	if err != nil {
		return err
	}
	if tag.RowsAffected() == 0 {
		return domain.E(domain.KindConflict, "SHIPMENT_NOT_DELIVERABLE",
			"this parcel is not in a state that can still arrive; check whether it "+
				"was already delivered or was cancelled")
	}
	return nil
}

// AttachShipmentLabel records the bought label.
//
// Guarded against being a second label for the same parcel: a re-buy is a real
// event, but it is a `void` then a `label`, and letting a plain re-post overwrite
// the first label would leave the audit trail showing only the newest.
func (r *ShipmentRepository) AttachShipmentLabel(
	ctx context.Context, q Querier, shipmentID, format, url, carrierRef, tracking string,
) error {
	_, err := q.Exec(ctx, `
		UPDATE shipments
		   SET label_format = NULLIF($2, ''),
		       label_url = NULLIF($3, ''),
		       label_created_at = now(),
		       status = CASE WHEN status = 'ready' THEN 'label_created' ELSE status END,
		       carrier_shipment_id = COALESCE(NULLIF($4, ''), carrier_shipment_id),
		       tracking_number = COALESCE(NULLIF($5, ''), tracking_number)
		 WHERE id = $1::uuid
		   AND label_created_at IS NULL
		   AND delivered_at IS NULL`, shipmentID, format, url, carrierRef, tracking)
	return err
}

// ShipmentByID reads one parcel.
func (r *ShipmentRepository) ShipmentByID(ctx context.Context, q Querier, id string) (*Shipment, error) {
	return scanShipment(q.QueryRow(ctx,
		`SELECT `+shipmentColumns+` FROM shipments WHERE id = $1::uuid`, id))
}

// ShipmentsForOrder lists an order's parcels, parcels first.
//
// Every parcel including cancelled and voided: the sequence numbers are the audit
// trail, and hiding parcel 2 because it was cancelled leaves "sequence 3" with no
// explanation.
func (r *ShipmentRepository) ShipmentsForOrder(ctx context.Context, q Querier, orderID string) ([]*Shipment, error) {
	rows, err := q.Query(ctx, `
		SELECT `+shipmentColumns+`
		  FROM shipments WHERE order_id = $1::uuid
		 ORDER BY kind, sequence`, orderID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	out := []*Shipment{}
	for rows.Next() {
		s, err := scanShipment(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, s)
	}
	return out, rows.Err()
}

// ShipmentItems lists what is in a parcel, with the product names the seller and
// buyer need to recognise it.
func (r *ShipmentRepository) ShipmentItems(
	ctx context.Context, q Querier, shipmentID string,
) ([]map[string]any, error) {
	rows, err := q.Query(ctx, `
		SELECT si.order_item_id::text, si.quantity, oi.product_name, oi.variant_name,
		       oi.sku, oi.quantity AS ordered_quantity, oi.shipped_quantity
		  FROM shipment_items si
		  JOIN order_items oi ON oi.id = si.order_item_id
		 WHERE si.shipment_id = $1::uuid
		 ORDER BY oi.product_name, oi.variant_name`, shipmentID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	out := []map[string]any{}
	for rows.Next() {
		var (
			id, name, variant, sku       string
			qty, ordered, alreadyShipped int
		)
		if err := rows.Scan(&id, &qty, &name, &variant, &sku, &ordered, &alreadyShipped); err != nil {
			return nil, err
		}
		out = append(out, map[string]any{
			"order_item_id":    id,
			"quantity":         qty,
			"product_name":     name,
			"variant_name":     variant,
			"sku":              sku,
			"ordered_quantity": ordered,
			"shipped_quantity": alreadyShipped,
			"remaining":        ordered - alreadyShipped,
		})
	}
	return out, rows.Err()
}
