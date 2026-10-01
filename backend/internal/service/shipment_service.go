package service

import (
	"context"
	"fmt"

	"github.com/vincommerce/backend/internal/domain"
	"github.com/vincommerce/backend/internal/repository"
)

// ShipmentService turns "I put some of this order in a box" into a parcel.
//
// WHY A SERVICE AND NOT MORE REPOSITORY METHODS
//
// Three writes have to be one commit: the parcel, its contents (which reserve
// shipped units), and the order's status (which is DERIVED from those contents).
// A repository method cannot compose, and the pieces are meaningless apart -- a
// parcel with no status update reads as "packed" to a buyer forever, and a status
// update with no parcel claims goods went somewhere that cannot be named.
//
// THE STATUS IS DERIVED, NOT REQUESTED
//
// No method here accepts a status. `DeriveShippingStatus` reads what is actually in
// the boxes. This is the same correction the payout work made about
// `orders.shipped_at` meaning "a seller pressed a button", and split shipping makes
// it unavoidable: with two parcels, "is it shipped" has no single answer to type in.

// ShipmentService owns parcels.
//
// It sends NO email. `mail.Client.Send` takes (ctx, to, subject, templateName, data)
// -- subject before template -- and a first draft here declared an interface with
// those two the other way round, which would have compiled and swapped every
// subject line in production. Worse, SellerService already has `emailBuyer`, which
// resolves the buyer for an order; a second mail seam would have been a second,
// subtly different path to the same inbox.
//
// So DispatchParcel reports whether this was the LAST parcel and leaves the
// notification to the caller, which already has the right one.
type ShipmentService struct {
	orders    *repository.OrderRepository
	shipments *repository.ShipmentRepository
}

// NewShipmentService creates a ShipmentService.
func NewShipmentService(orders *repository.OrderRepository, shipments *repository.ShipmentRepository) *ShipmentService {
	return &ShipmentService{orders: orders, shipments: shipments}
}

// ParcelLine is one line the seller says goes in a parcel.
type ParcelLine struct {
	OrderItemID string
	Quantity    int
}

// CreateParcel records a parcel and everything in it, and moves the order to
// whatever shipping status those contents imply.
//
// REFUSES a cancelled order, and REFUSES a second call that would re-ship units
// already shipped. The second refusal comes from the conditional update in
// `AddShipmentItems`, not from a check here, because a check here and a check there
// are two chances to disagree under concurrency.
func (s *ShipmentService) CreateParcel(
	ctx context.Context, actorID, orderID, carrier string, lines []ParcelLine,
) (*repository.Shipment, error) {
	order, err := s.orders.ByID(ctx, orderID)
	if err != nil {
		return nil, err
	}
	// The seller owns the order. An actorID of "" means an internal caller (a
	// carrier webhook), which is allowed through -- and only that, because the
	// check is an identity test rather than an absence of a check.
	if actorID != "" && order.SellerID != actorID {
		return nil, domain.E(domain.KindForbidden, "NOT_OWNED",
			"this order does not belong to your store")
	}
	if order.Status == domain.OrderCancelled || order.Status == domain.OrderReturned {
		return nil, domain.E(domain.KindConflict, "ORDER_NOT_SHIPPABLE",
			fmt.Sprintf("this order is %s, so nothing in it can be shipped", order.Status))
	}
	if order.Status != domain.OrderPaid && order.Status != domain.OrderPacked &&
		order.Status != domain.OrderPartiallyShipped {
		return nil, domain.E(domain.KindConflict, "ORDER_NOT_PACKABLE",
			fmt.Sprintf("an order can only be packed from paid; this one is %s",
				order.Status))
	}

	tx, err := s.orders.Begin(ctx)
	if err != nil {
		return nil, err
	}
	defer tx.Rollback(ctx)
	q := tx.Querier()

	shipment, err := s.shipments.CreateShipment(ctx, q, orderID, order.SellerID,
		repository.ShipmentOutbound, carrier)
	if err != nil {
		return nil, err
	}

	items := make([]repository.ShipmentItemLine, 0, len(lines))
	for _, l := range lines {
		items = append(items, repository.ShipmentItemLine{
			OrderItemID: l.OrderItemID, Quantity: l.Quantity,
		})
	}
	if err := s.shipments.AddShipmentItems(ctx, q, shipment.ID, items); err != nil {
		return nil, err
	}
	if err := s.shipments.RefreshShipmentWeight(ctx, q, shipment.ID); err != nil {
		return nil, err
	}
	if err := s.orders.SyncOrderItemStatuses(ctx, q, orderID); err != nil {
		return nil, err
	}

	// Derived from what is in the boxes, in the same transaction, and only when the
	// state machine allows it. A derivation the machine refuses is a BUG, not a
	// seller error, so it is reported as such rather than surfaced as a transition
	// conflict the seller cannot act on.
	if err := s.applyDerivedStatus(ctx, q, orderID, order.Status); err != nil {
		return nil, err
	}
	if err := tx.Commit(ctx); err != nil {
		return nil, err
	}
	return s.shipments.ShipmentByID(ctx, s.shipments.Pool(), shipment.ID)
}

// applyDerivedStatus moves the order to its derived shipping status.
//
// The guard is the interesting part. `DeriveShippingStatus` can return `shipped` for
// an order the machine will not let go straight to shipped -- an order already
// `delivered` that gets a late parcel recorded, for instance. Silently ignoring
// that would leave the parcel recorded and the status lying; erroring would blame
// the seller for a state they did not create. So the derivation is only applied
// when it is legal, and the caller is told when it was not.
func (s *ShipmentService) applyDerivedStatus(
	ctx context.Context, q repository.Querier, orderID, current string,
) error {
	derived, changed, err := s.orders.DeriveShippingStatus(ctx, q, orderID)
	if err != nil {
		return err
	}
	if !changed || derived == current {
		return nil
	}
	if !domain.CanTransition(current, derived) {
		return domain.E(domain.KindInternal, "SHIPMENT_STATUS_NOT_REACHABLE",
			fmt.Sprintf("this order is %s and its parcels imply %s, which the state "+
				"machine does not allow; the parcel was recorded and the order status "+
				"was left alone rather than being moved illegally", current, derived))
	}
	return s.orders.TransitionOrderTx(ctx, q, orderID, current, derived, "")
}

// DispatchParcel records hand-over to the carrier.
//
// It does NOT email the buyer, and it reports whether this was the LAST parcel so
// the caller can. Announcing parcel one of two tells a buyer their order is on its
// way when most of it is still in a warehouse, which produces exactly the "where is
// my order" ticket that sending one email instead of two would have avoided.
func (s *ShipmentService) DispatchParcel(
	ctx context.Context, actorID, shipmentID, tracking string,
) (*repository.Shipment, bool, error) {
	tx, err := s.orders.Begin(ctx)
	if err != nil {
		return nil, false, err
	}
	defer tx.Rollback(ctx)
	q := tx.Querier()

	shipment, err := s.shipments.ShipmentByID(ctx, q, shipmentID)
	if err != nil {
		return nil, false, err
	}
	if actorID != "" && shipment.SellerID != actorID {
		return nil, false, domain.E(domain.KindForbidden, "NOT_OWNED",
			"this parcel does not belong to your store")
	}
	if err := s.shipments.MarkShipmentShipped(ctx, q, shipmentID, tracking); err != nil {
		return nil, false, err
	}

	// The order status is already correct -- it was derived when the parcel was
	// created, because the units are reserved at that point, not at hand-over.
	// Moving it again here would be a second transition for the same fact.
	derived, _, err := s.orders.DeriveShippingStatus(ctx, q, shipment.OrderID)
	if err != nil {
		return nil, false, err
	}
	if err := tx.Commit(ctx); err != nil {
		return nil, false, err
	}

	out, err := s.shipments.ShipmentByID(ctx, s.shipments.Pool(), shipmentID)
	if err != nil {
		return nil, false, err
	}
	return out, derived == domain.OrderShipped, nil
}

// OrderParcels lists an order's parcels for the seller and buyer views.
func (s *ShipmentService) OrderParcels(ctx context.Context, orderID string) ([]*repository.Shipment, error) {
	return s.shipments.ShipmentsForOrder(ctx, s.shipments.Pool(), orderID)
}

// ParcelContents lists what is in one parcel.
func (s *ShipmentService) ParcelContents(ctx context.Context, shipmentID string) ([]map[string]any, error) {
	return s.shipments.ShipmentItems(ctx, s.shipments.Pool(), shipmentID)
}

// ParcelDue reports whether a parcel is waiting to be handed over, for the seller
// reminder list.
func (s *ShipmentService) ParcelDue(ctx context.Context, shipmentID string) (bool, error) {
	shipment, err := s.shipments.ShipmentByID(ctx, s.shipments.Pool(), shipmentID)
	if err != nil {
		return false, err
	}
	return shipment.Status == repository.ShipmentLabelCreated ||
		(shipment.Status == repository.ShipmentReady && shipment.Carrier == "manual"), nil
}
