package service

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/vincommerce/backend/internal/carrier"
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

// ParcelView is a parcel as the API exposes it.
//
// The service layer owns wire shapes -- that is why `internal/httpapi/handler` is
// forbidden from importing `internal/repository`, and this is the same reason
// `PayoutBatchView` exists. A handler that reaches for a repository type forces
// either the violation or a re-declared duplicate of the same struct, and the
// duplicate drifts.
type ParcelView struct {
	ID             string `json:"id"`
	OrderID        string `json:"order_id"`
	Sequence       int    `json:"sequence"`
	Kind           string `json:"kind"`
	Status         string `json:"status"`
	Carrier        string `json:"carrier"`
	CarrierService string `json:"carrier_service,omitempty"`
	Tracking       string `json:"tracking_number,omitempty"`
	LabelFormat    string `json:"label_format,omitempty"`
	LabelURL       string `json:"label_url,omitempty"`
	WeightGrams    int    `json:"weight_grams"`
	ShippedAt      string `json:"shipped_at,omitempty"`
	DeliveredAt    string `json:"delivered_at,omitempty"`
}

func parcelView(sh *repository.Shipment) ParcelView {
	v := ParcelView{
		ID:             sh.ID,
		OrderID:        sh.OrderID,
		Sequence:       sh.Sequence,
		Kind:           sh.Kind,
		Status:         sh.Status,
		Carrier:        sh.Carrier,
		CarrierService: sh.Service,
		Tracking:       sh.Tracking,
		LabelFormat:    sh.LabelFormat,
		LabelURL:       sh.LabelURL,
		WeightGrams:    sh.WeightGrams,
	}
	if sh.ShippedAt != nil {
		v.ShippedAt = sh.ShippedAt.UTC().Format(time.RFC3339)
	}
	if sh.DeliveredAt != nil {
		v.DeliveredAt = sh.DeliveredAt.UTC().Format(time.RFC3339)
	}
	return v
}

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
	carriers  *carrier.LabelRegistry
}

// NewShipmentService creates a ShipmentService.
func NewShipmentService(
	orders *repository.OrderRepository, shipments *repository.ShipmentRepository, carriers *carrier.LabelRegistry,
) *ShipmentService {
	if carriers == nil {
		// Never nil. A nil registry would panic on the first label request, and the
		// panic would be in whichever seller happened to press the button first.
		// An empty registry answers every lookup with a NAMED error naming the
		// carrier, which is the useful failure.
		carriers = carrier.NewLabelRegistry()
	}
	return &ShipmentService{orders: orders, shipments: shipments, carriers: carriers}
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
) (ParcelView, error) {
	order, err := s.orders.ByID(ctx, orderID)
	if err != nil {
		return ParcelView{}, err
	}
	// The seller owns the order. An actorID of "" means an internal caller (a
	// carrier webhook), which is allowed through -- and only that, because the
	// check is an identity test rather than an absence of a check.
	if actorID != "" && order.SellerID != actorID {
		return ParcelView{}, domain.E(domain.KindForbidden, "NOT_OWNED",
			"this order does not belong to your store")
	}
	if order.Status == domain.OrderCancelled || order.Status == domain.OrderReturned {
		return ParcelView{}, domain.E(domain.KindConflict, "ORDER_NOT_SHIPPABLE",
			fmt.Sprintf("this order is %s, so nothing in it can be shipped", order.Status))
	}
	if order.Status != domain.OrderPaid && order.Status != domain.OrderPacked &&
		order.Status != domain.OrderPartiallyShipped {
		return ParcelView{}, domain.E(domain.KindConflict, "ORDER_NOT_PACKABLE",
			fmt.Sprintf("an order can only be packed from paid; this one is %s",
				order.Status))
	}

	tx, err := s.orders.Begin(ctx)
	if err != nil {
		return ParcelView{}, err
	}
	defer tx.Rollback(ctx)
	q := tx.Querier()

	shipment, err := s.shipments.CreateShipment(ctx, q, orderID, order.SellerID,
		repository.ShipmentOutbound, carrier)
	if err != nil {
		return ParcelView{}, err
	}

	items := make([]repository.ShipmentItemLine, 0, len(lines))
	for _, l := range lines {
		items = append(items, repository.ShipmentItemLine{
			OrderItemID: l.OrderItemID, Quantity: l.Quantity,
		})
	}
	if err := s.shipments.AddShipmentItems(ctx, q, shipment.ID, items); err != nil {
		return ParcelView{}, err
	}
	if err := s.shipments.RefreshShipmentWeight(ctx, q, shipment.ID); err != nil {
		return ParcelView{}, err
	}
	if err := s.orders.SyncOrderItemStatuses(ctx, q, orderID); err != nil {
		return ParcelView{}, err
	}

	// Derived from what is in the boxes, in the same transaction, and only when the
	// state machine allows it. A derivation the machine refuses is a BUG, not a
	// seller error, so it is reported as such rather than surfaced as a transition
	// conflict the seller cannot act on.
	if err := s.applyDerivedStatus(ctx, q, orderID, order.Status); err != nil {
		return ParcelView{}, err
	}
	if err := tx.Commit(ctx); err != nil {
		return ParcelView{}, err
	}
	out, err := s.shipments.ShipmentByID(ctx, s.shipments.Pool(), shipment.ID)
	if err != nil {
		return ParcelView{}, err
	}
	return parcelView(out), nil
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
) (ParcelView, bool, error) {
	tx, err := s.orders.Begin(ctx)
	if err != nil {
		return ParcelView{}, false, err
	}
	defer tx.Rollback(ctx)
	q := tx.Querier()

	shipment, err := s.shipments.ShipmentByID(ctx, q, shipmentID)
	if err != nil {
		return ParcelView{}, false, err
	}
	if actorID != "" && shipment.SellerID != actorID {
		return ParcelView{}, false, domain.E(domain.KindForbidden, "NOT_OWNED",
			"this parcel does not belong to your store")
	}
	if err := s.shipments.MarkShipmentShipped(ctx, q, shipmentID, tracking); err != nil {
		return ParcelView{}, false, err
	}

	// The order status is already correct -- it was derived when the parcel was
	// created, because the units are reserved at that point, not at hand-over.
	// Moving it again here would be a second transition for the same fact.
	derived, _, err := s.orders.DeriveShippingStatus(ctx, q, shipment.OrderID)
	if err != nil {
		return ParcelView{}, false, err
	}
	if err := tx.Commit(ctx); err != nil {
		return ParcelView{}, false, err
	}

	out, err := s.shipments.ShipmentByID(ctx, s.shipments.Pool(), shipmentID)
	if err != nil {
		return ParcelView{}, false, err
	}
	return parcelView(out), derived == domain.OrderShipped, nil
}

// BuyLabel buys a printable label for a parcel and records it.
//
// The registry resolves the carrier, and a carrier this platform has never heard of
// is NAMED in the error rather than silently answered with the manual one. That
// distinction is the whole reason a registry is a map and not a switch with a default:
// a seller asking for "jnE" and receiving a self-minted tracking number would believe
// a courier was involved, and would spend the next three days waiting on one.
//
// The idempotency key is the PARCEL, not the attempt: buying a label spends real
// money, and a retry after a timeout must return the label that was already paid for
// rather than buy a second one for the same box.
func (s *ShipmentService) BuyLabel(
	ctx context.Context, actorID, shipmentID, format string,
) (ParcelView, error) {
	shipment, err := s.shipments.ShipmentByID(ctx, s.shipments.Pool(), shipmentID)
	if err != nil {
		return ParcelView{}, err
	}
	if actorID != "" && shipment.SellerID != actorID {
		return ParcelView{}, domain.E(domain.KindForbidden, "NOT_OWNED",
			"this parcel does not belong to your store")
	}
	// A delivered parcel must not get a new label: the goods are already there, and a
	// second label is money spent on a box that will never move.
	if shipment.DeliveredAt != nil {
		return ParcelView{}, domain.E(domain.KindConflict, "SHIPMENT_ALREADY_DELIVERED",
			"this parcel is already delivered, so a new label would be money spent on "+
				"a box that is not going anywhere")
	}

	contents, err := s.shipments.ShipmentItems(ctx, s.shipments.Pool(), shipmentID)
	if err != nil {
		return ParcelView{}, err
	}
	// An empty parcel cannot be labelled. `AddShipmentItems` already refuses to
	// CREATE one, so this is belt-and-braces for a parcel emptied by a later change
	// -- and the failure is worth naming here rather than at the carrier, where it
	// arrives as an opaque rejection after the seller has waited on a request.
	if len(contents) == 0 {
		return ParcelView{}, domain.E(domain.KindConflict, "SHIPMENT_EMPTY",
			"this parcel has nothing in it, so there is no shipment to label")
	}

	c, err := s.carriers.Get(shipment.Carrier)
	if err != nil {
		return ParcelView{}, domain.E(domain.KindInvalid, "CARRIER_NOT_CONFIGURED", err.Error())
	}

	in := carrier.BuyLabelInput{
		ParcelID:    shipment.ID,
		OrderID:     shipment.OrderID,
		Carrier:     shipment.Carrier,
		Service:     shipment.Service,
		Format:      format,
		WeightGrams: shipment.WeightGrams,
		Destination: s.destinationFor(ctx, shipment.OrderID),
		Idempotency: shipment.ID,
	}

	label, err := c.BuyLabel(ctx, in)
	if err != nil {
		return ParcelView{}, domain.E(domain.KindInvalid, "CARRIER_REJECTED_LABEL", err.Error())
	}
	if err := s.shipments.AttachShipmentLabel(ctx, s.shipments.Pool(), shipmentID,
		label.Format, label.URL, label.CarrierRef, label.Tracking); err != nil {
		return ParcelView{}, err
	}
	out, err := s.shipments.ShipmentByID(ctx, s.shipments.Pool(), shipmentID)
	if err != nil {
		return ParcelView{}, err
	}
	return parcelView(out), nil
}

// TrackParcel asks the carrier where a parcel is.
//
// A carrier that cannot track says so by not implementing TrackingCarrier, and the
// two-value assertion makes that an error naming the carrier rather than a parcel
// silently stuck in `unknown` for ever.
func (s *ShipmentService) TrackParcel(ctx context.Context, shipmentID string) (*carrier.Tracking, error) {
	shipment, err := s.shipments.ShipmentByID(ctx, s.shipments.Pool(), shipmentID)
	if err != nil {
		return nil, err
	}
	ref := shipment.Tracking
	if ref == "" {
		ref = shipment.CarrierRef
	}
	if ref == "" {
		return nil, domain.E(domain.KindConflict, "SHIPMENT_NOT_TRACKABLE",
			"this parcel has neither a tracking number nor a carrier reference, so "+
				"there is nothing to ask a carrier about; it may not have a label yet")
	}
	c, err := s.carriers.Get(shipment.Carrier)
	if err != nil {
		return nil, domain.E(domain.KindInvalid, "CARRIER_NOT_CONFIGURED", err.Error())
	}
	tc, ok := c.(carrier.TrackingCarrier)
	if !ok {
		return nil, domain.E(domain.KindInvalid, "CARRIER_CANNOT_TRACK",
			"carrier "+shipment.Carrier+" cannot report where a parcel is; this is a "+
				"property of that carrier, not a fault with the parcel")
	}
	return tc.Track(ctx, ref)
}

// destinationFor reads an order's shipping address as a carrier address.
//
// An empty address is returned as zero rather than as a fabricated one, so
// `BuyLabelInput.Destination.IsZero()` refuses the label. A label addressed to a
// guess is money spent and a parcel nobody receives.
func (s *ShipmentService) destinationFor(ctx context.Context, orderID string) carrier.Address {
	order, err := s.orders.ByID(ctx, orderID)
	if err != nil {
		return carrier.Address{}
	}
	raw := order.ShippingAddress
	if raw == nil {
		return carrier.Address{}
	}
	str := func(keys ...string) string {
		for _, k := range keys {
			if v, ok := raw[k].(string); ok && strings.TrimSpace(v) != "" {
				return v
			}
		}
		return ""
	}
	return carrier.Address{
		Name:     str("name", "Name", "recipient", "full_name"),
		Phone:    str("phone", "Phone", "telephone"),
		Street:   str("street", "Street", "address", "address_line1", "line1"),
		City:     str("city", "City"),
		Province: str("province", "Province", "state"),
		Postcode: str("postcode", "Postcode", "zip", "postal_code"),
		Country:  str("country", "Country"),
	}
}

// OrderParcels lists an order's parcels for the seller and buyer views.
func (s *ShipmentService) OrderParcels(ctx context.Context, orderID string) ([]ParcelView, error) {
	parcels, err := s.shipments.ShipmentsForOrder(ctx, s.shipments.Pool(), orderID)
	if err != nil {
		return nil, err
	}
	out := make([]ParcelView, 0, len(parcels))
	for _, p := range parcels {
		out = append(out, parcelView(p))
	}
	return out, nil
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
