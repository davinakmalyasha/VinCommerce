package repository

import (
	"context"
	"errors"

	"github.com/jackc/pgx/v5"
	"github.com/vincommerce/backend/internal/domain"
)

// ===========================================================================
// Return parcels
// ===========================================================================
//
// A return parcel is the journey of goods coming BACK to the seller. It is the same
// physical movement as an outbound parcel described the other way round, which is why
// it is the same table with `kind = 'return'` rather than a second table: the carrier
// call, the label, the tracking query and the webhook path are identical.
//
// THE INVARIANT, and it is the reason this is not just CreateShipment with a
// different kind:
//
// A return parcel MUST NOT touch `order_items.shipped_quantity`.
//
// `shipped_quantity` is the over-shipment counter, and the arithmetic that
// `DeriveShippingStatus` reads compares it against `quantity` to decide whether an
// order is `partially_shipped` or `shipped`. Incrementing it for goods travelling in
// the OTHER direction means:
//
//   * an order that was fully delivered now reads as "3 of 3 shipped" when it was
//     "3 of 3 shipped and now 1 came back", which is a different fact;
//   * `DeriveShippingStatus` sees shipped >= ordered and re-asserts `shipped`, so the
//     return silently re-delivers the order's shipping status;
//   * and the one quantity in the line is counted twice, once outbound and once back,
//     so a line ordered as 1 becomes 2 and can no longer be reasoned about.
//
// None of that is a subtle arithmetic error. It is the same defect 00050 had to fix
// for the ORDER status -- a row claiming something its own contents contradict --
// arriving one level down and through the return path, where nobody is watching.
//
// So the reservation function used by the outbound path is NOT reused here. The only
// writer of `shipped_quantity` stays `AddShipmentItems`, which is outbound-only.

// ReturnParcel is the parcel a buyer's return travels in.
type ReturnParcel struct {
	ReturnID    string
	ShipmentID  string
	LabelURL    string
	LabelFormat string
	AttachedAt  *string
}

// ErrReturnAlreadyParcelled is returned when a second parcel is created for one
// return.
var ErrReturnAlreadyParcelled = domain.E(domain.KindConflict, "RETURN_ALREADY_PARCELED",
	"this return already has a parcel; two boxes arriving against one approved "+
		"refund means one of them matches nothing and the seller has to decide by eye "+
		"which is which")

// CreateReturnParcel records the parcel a buyer's return travels in.
//
// It does NOT reserve shipped units -- see the section comment. That is the whole
// difference from the outbound path, and it is why this is a separate method rather
// than a `kind` argument on `CreateShipment`: a shared method would need a branch that
// skips the counter, and the next caller would not know to include it.
func (r *ShipmentRepository) CreateReturnParcel(
	ctx context.Context, q Querier, returnID, orderID, orderItemID, buyerID, sellerID, carrier string,
	quantity int,
) (*ReturnParcel, error) {
	if quantity <= 0 {
		return nil, domain.E(domain.KindInvalid, "RETURN_QUANTITY_INVALID",
			"a return parcel must carry at least one unit")
	}

	// One parcel per return. The check is taken UNDER a lock, which is the whole
	// point, and the ordering is the part that was wrong.
	//
	// This read used to happen before CreateShipment took its advisory lock, so it was
	// a check performed outside the critical section: two concurrent requests both read
	// "no parcel yet", then serialised on the lock inside CreateShipment, and the
	// second one never re-read. Both then wrote a parcel, and because the write is an
	// UPDATE of `return_requests.return_shipment_id`, the second OVERWROTE the first
	// rather than conflicting with it. The result was a real return parcel row that no
	// return pointed at -- goods in motion that nothing tracked, and a return that read
	// as never sent back.
	//
	// The UNIQUE index is not the backstop for this. It is on
	// `return_requests.return_shipment_id`, which stops two RETURNS sharing one parcel;
	// it cannot stop two parcels for one return.
	//
	// Locking on the return id rather than (order, kind) is deliberate: the invariant
	// here is per-RETURN, whereas CreateShipment's is per-(order, kind). Using the same
	// key would have serialised unrelated returns on the same order for no benefit.
	if _, err := q.Exec(ctx, `SELECT pg_advisory_xact_lock(hashtext($1))`, returnID); err != nil {
		return nil, err
	}

	var existing string
	err := q.QueryRow(ctx,
		`SELECT return_shipment_id::text FROM return_requests
		  WHERE id = $1::uuid AND return_shipment_id IS NOT NULL`, returnID).Scan(&existing)
	if err == nil {
		return nil, ErrReturnAlreadyParcelled
	}
	if !errors.Is(err, pgx.ErrNoRows) {
		return nil, err
	}

	shipment, err := r.CreateShipment(ctx, q, orderID, sellerID, ShipmentReturn, carrier)
	if err != nil {
		return nil, err
	}

	// The returned line goes in the parcel WITHOUT reserving anything.
	//
	// Written as a bare INSERT rather than through `AddShipmentItems`, because that
	// method's entire job is the reservation. Reusing it with a "skip the counter"
	// flag would put the invariant one boolean away from being wrong, and a boolean
	// that disables an over-shipment check is exactly the shape of flag that gets
	// flipped by accident.
	if _, err := q.Exec(ctx, `
		INSERT INTO shipment_items (shipment_id, order_item_id, quantity)
		VALUES ($1::uuid, $2::uuid, $3)
		ON CONFLICT (shipment_id, order_item_id) DO NOTHING`,
		shipment.ID, orderItemID, quantity); err != nil {
		return nil, err
	}

	// Weight is still derived, not typed. A return travels the same distance and is
	// charged the same way.
	if err := r.RefreshShipmentWeight(ctx, q, shipment.ID); err != nil {
		return nil, err
	}

	var out ReturnParcel
	if err := q.QueryRow(ctx, `
		UPDATE return_requests
		   SET return_shipment_id = $2::uuid, updated_at = now()
		 WHERE id = $1::uuid
		RETURNING id::text, return_shipment_id::text`,
		returnID, shipment.ID).Scan(&out.ReturnID, &out.ShipmentID); err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, domain.ErrNotFound
		}
		return nil, err
	}
	return &out, nil
}

// AttachReturnLabel records the bought return label.
//
// The URL is mirrored onto the return row as a POINTER, not as state. It is signed
// and expiring in every real carrier; the durable record is the parcel's
// `label_format` and `label_created_at`, which 00051's FK already reaches. A second
// copy of an expiring URL is a thing that stops working and cannot be told apart from
// one that does not.
func (r *ShipmentRepository) AttachReturnLabel(
	ctx context.Context, q Querier, returnID, shipmentID, format, url string,
) error {
	_, err := q.Exec(ctx, `
		UPDATE return_requests r
		   SET return_label_url = NULLIF($3, ''), updated_at = now()
		  FROM shipments s
		 WHERE s.id = r.return_shipment_id
		   AND r.id = $1::uuid
		   AND s.id = $2::uuid`, returnID, shipmentID, url)
	return err
}

// ReturnParcelFor returns the parcel a return travels in, or nil.
func (r *ShipmentRepository) ReturnParcelFor(
	ctx context.Context, q Querier, returnID string,
) (*ReturnParcel, error) {
	var p ReturnParcel
	var labelURL *string
	err := q.QueryRow(ctx, `
		SELECT r.id::text, r.return_shipment_id::text, r.return_label_url,
		       COALESCE(s.label_format,''), s.label_created_at
		  FROM return_requests r
		  JOIN shipments s ON s.id = r.return_shipment_id
		 WHERE r.id = $1::uuid`, returnID).
		Scan(&p.ReturnID, &p.ShipmentID, &labelURL, &p.LabelFormat, &p.AttachedAt)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	if labelURL != nil {
		p.LabelURL = *labelURL
	}
	return &p, nil
}

// MarkReturnArrived moves an approved return to returned, once its parcel has
// actually been delivered.
//
// GUARDED on the PARCEL being delivered, not on the caller's word. This is the
// decision from the plan: the journey lives on the parcel, and the return row never
// claims something the parcel table contradicts. A return marked returned with the
// box still sitting in a depot is a refund paid for goods nobody has.
//
// Idempotent through the `status = 'approved'` predicate rather than an error, because
// a carrier webhook and a seller clicking "it arrived" will both arrive here, and
// losing that race is the normal case, not a fault.
func (r *ShipmentRepository) MarkReturnArrived(
	ctx context.Context, q Querier, returnID string,
) (bool, error) {
	tag, err := q.Exec(ctx, `
		UPDATE return_requests r
		   SET status = 'returned', updated_at = now()
		  FROM shipments s
		 WHERE r.id = $1::uuid
		   AND r.status = 'approved'
		   AND s.id = r.return_shipment_id
		   AND s.status = 'delivered'
		   AND s.delivered_at IS NOT NULL`, returnID)
	if err != nil {
		return false, err
	}
	return tag.RowsAffected() > 0, nil
}
