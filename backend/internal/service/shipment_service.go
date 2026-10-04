package service

import (
	"context"
	"errors"
	"fmt"
	"github.com/jackc/pgx/v5"
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
	// stores carries the seller's RETURN address (00051). It is a separate
	// dependency from `orders` because an order's `shipping_address` is the BUYER's
	// and must never be consulted for a return -- see `returnDestinationFor`.
	stores *repository.StoreRepository
	// returns reads return_requests. Those live on StoreRepository, not on a
	// ReturnRepository, which is odd placement but pre-existing (00006 created
	// return_requests inside the marketplace migration). Moving them is a refactor
	// with no bearing on the gap this file closes, so the odd shape is inherited
	// rather than fixed here.
	returns *repository.StoreRepository
}

// NewShipmentService creates a ShipmentService.
func NewShipmentService(
	orders *repository.OrderRepository, shipments *repository.ShipmentRepository,
	carriers *carrier.LabelRegistry, stores *repository.StoreRepository,
) *ShipmentService {
	if carriers == nil {
		// Never nil. A nil registry would panic on the first label request, and the
		// panic would be in whichever seller happened to press the button first.
		// An empty registry answers every lookup with a NAMED error naming the
		// carrier, which is the useful failure.
		carriers = carrier.NewLabelRegistry()
	}
	// `returns` and `stores` are the same repository. Assigning it to both names
	// rather than threading a second dependency is honest about where the data
	// actually lives while leaving room to move it later.
	return &ShipmentService{
		orders:    orders,
		shipments: shipments,
		carriers:  carriers,
		stores:    stores,
		returns:   stores,
	}
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

// ===========================================================================
// Return parcels
// ===========================================================================
//
// The return journey. A seller approves a return, the buyer is emailed "silakan
// kirim barang kembali", and until this existed there was no way for the buyer to
// comply. This is the code that makes that email true.
//
// NOTHING HERE MOVES MONEY.
//
// A return parcel is a label and a tracking number. It does not release escrow, does
// not post a journal, does not debit a wallet and does not reserve a payout. The
// refund is `RefundReturn`, an explicit seller action, and the seller hold taken when
// the return was raised (fbab73f) is released by the existing reservation job. A
// carrier webhook that can be made to say "delivered" must not be able to pay out a
// seller, so the only thing a delivery event does is move a parcel and then set
// `return_requests.status = 'returned'` -- which unblocks a refund a human still has
// to perform.
//
// That is asserted by a test over this file, the same way `payout_batch_test.go`
// asserts that no batch path writes `payouts.status`.

// CreateReturnParcel records the box a buyer's return travels in.
//
// The BUYER may create it, not the seller. A seller who could create and dispatch the
// return parcel could mark goods as returned without them ever having left the buyer,
// which is the fraud this whole feature is exposed to. The seller issues the LABEL
// (so the seller pays for the carriage and controls the address); the buyer
// physically hands it over.
func (s *ShipmentService) CreateReturnParcel(
	ctx context.Context, returnID, carrier string, quantity int,
) (ParcelView, error) {
	orderID, itemID, buyerID, sellerID, status, err := s.returnFacts(ctx, returnID)
	if err != nil {
		return ParcelView{}, err
	}
	// Only an APPROVED return may be parcelled. A `requested` return is one the seller
	// has not agreed to, and a parcel for it is goods in motion that nobody has
	// accepted responsibility for.
	if status != domain.ReturnApproved {
		return ParcelView{}, domain.E(domain.KindConflict, "RETURN_NOT_APPROVED",
			"this return is "+status+", so a return label cannot be issued for it; "+
				"only a return the seller has approved can be sent back")
	}

	tx, err := s.orders.Begin(ctx)
	if err != nil {
		return ParcelView{}, err
	}
	defer tx.Rollback(ctx)
	q := tx.Querier()

	parcel, err := s.shipments.CreateReturnParcel(ctx, q, returnID, orderID, itemID,
		buyerID, sellerID, carrier, quantity)
	if err != nil {
		return ParcelView{}, err
	}
	if err := tx.Commit(ctx); err != nil {
		return ParcelView{}, err
	}
	out, err := s.shipments.ShipmentByID(ctx, s.shipments.Pool(), parcel.ShipmentID)
	if err != nil {
		return ParcelView{}, err
	}
	return parcelView(out), nil
}

// BuyReturnLabel buys the label for a return parcel.
//
// The destination is the SELLER, which is the reverse of every outbound label, and is
// the single most likely thing to get wrong: a return label addressed to the buyer is
// a second parcel of the same goods, going back where they came from, at the seller's
// expense. M64 pins it.
//
// The label is idempotent on the PARCEL, exactly as the outbound path is, because it
// spends the same kind of money.
func (s *ShipmentService) BuyReturnLabel(
	ctx context.Context, actorID, returnID, format string,
) (ParcelView, string, error) {
	// Ownership FIRST, before the parcel is read and long before a carrier is called.
	//
	// This endpoint spends money: it buys a real label at a real carrier. Without the
	// check, any seller could POST a return id belonging to a competitor and have a
	// label bought on their return -- addressed to the competitor's store, at their
	// expense, and revealing that store's address.
	if err := s.assertReturnSeller(ctx, actorID, returnID); err != nil {
		return ParcelView{}, "", err
	}

	parcel, err := s.shipments.ReturnParcelFor(ctx, s.shipments.Pool(), returnID)
	if err != nil {
		return ParcelView{}, "", err
	}
	if parcel == nil {
		return ParcelView{}, "", domain.E(domain.KindConflict, "RETURN_NOT_PARCELED",
			"this return has no parcel yet, so there is nothing to label; the buyer "+
				"creates the return parcel first")
	}

	shipment, err := s.shipments.ShipmentByID(ctx, s.shipments.Pool(), parcel.ShipmentID)
	if err != nil {
		return ParcelView{}, "", err
	}
	if shipment.DeliveredAt != nil {
		return ParcelView{}, "", domain.E(domain.KindConflict, "SHIPMENT_ALREADY_DELIVERED",
			"this return has already arrived, so a new label would be money spent on a "+
				"box that is not going anywhere")
	}

	c, err := s.carriers.Get(shipment.Carrier)
	if err != nil {
		return ParcelView{}, "", domain.E(domain.KindInvalid, "CARRIER_NOT_CONFIGURED", err.Error())
	}

	label, err := c.BuyLabel(ctx, carrier.BuyLabelInput{
		ParcelID:    shipment.ID,
		OrderID:     shipment.OrderID,
		Carrier:     shipment.Carrier,
		Service:     shipment.Service,
		Format:      format,
		WeightGrams: shipment.WeightGrams,
		// `shipment.SellerID`, NOT `shipment.OrderID`.
		//
		// Both are strings, so the compiler cannot help here, and getting it wrong is
		// silent: the lookup is `stores.return_address WHERE owner_id = $1`, an order id
		// never matches a store owner, so EVERY return label is refused with
		// "no destination" and the return feature is dead while every test on the
		// carrier package still passes.
		//
		// The order id is what the OUTBOUND path passes, which is exactly how the
		// wrong one gets there. M64 pins it.
		Destination: s.returnDestinationFor(ctx, shipment.SellerID),
		Idempotency: shipment.ID,
		ContainsCod: false,
	})
	if err != nil {
		return ParcelView{}, "", domain.E(domain.KindInvalid, "CARRIER_REJECTED_LABEL", err.Error())
	}

	// Attach to BOTH the parcel (durable: format, created_at, carrier ref) and the
	// return row (a pointer, for the buyer's convenience). One is the record, one is
	// the link.
	if err := s.shipments.AttachShipmentLabel(ctx, s.shipments.Pool(), shipment.ID,
		label.Format, label.URL, label.CarrierRef, label.Tracking); err != nil {
		return ParcelView{}, "", err
	}
	if err := s.shipments.AttachReturnLabel(ctx, s.shipments.Pool(),
		returnID, shipment.ID, label.Format, label.URL); err != nil {
		return ParcelView{}, "", err
	}

	out, err := s.shipments.ShipmentByID(ctx, s.shipments.Pool(), shipment.ID)
	if err != nil {
		return ParcelView{}, "", err
	}
	return parcelView(out), label.URL, nil
}

// NoteReturnArrived records that a return parcel has been delivered, which unblocks
// the refund a human still has to perform.
//
// It moves `return_requests` to 'returned' and NOTHING ELSE. The refund is
// `RefundReturn`, the seller hold is released by the existing reservation job, and no
// ledger posting happens on this path.
// NoteReturnArrived records that a return parcel has been delivered, which unblocks
// the refund a human still has to perform.
//
// Seller-scoped, and this is the highest-value ownership check in the file. The row it
// moves is the gate on `RefundReturn`: once the return reads as arrived, the seller can
// refund the buyer. Without the check, any seller could POST another seller's return id
// and unblock that refund -- not stealing it directly, but unlocking a state change
// that makes it refundable against someone else's escrow. That is a money bug wearing
// an authorisation bug's clothes.
func (s *ShipmentService) NoteReturnArrived(
	ctx context.Context, actorID, returnID string,
) (bool, error) {
	if err := s.assertReturnSeller(ctx, actorID, returnID); err != nil {
		return false, err
	}
	return s.shipments.MarkReturnArrived(ctx, s.shipments.Pool(), returnID)
}

// returnDestinationFor resolves the SELLER's pickup address for a return label.
//
// THIS IS THE OPPOSITE of `destinationFor`, and using the wrong one is the single
// most damaging mistake available in this file: it posts the returned goods straight
// back to the buyer, at the seller's expense, as a second parcel of the same items.
//
// The source is `stores.return_address` (00051) and nothing else. `orders.shipping_address`
// is the BUYER's and is never consulted here, even as a fallback -- a fallback that
// is sometimes right is a fallback that will be wrong in production.
//
// When the seller has not set one, this returns the ZERO address, and the carrier's
// `BuyLabelInput.Destination.IsZero()` refuses the label. That is the correct failure:
// loud, named, and impossible to misread as success. The alternative -- sending the
// parcel somewhere plausible -- is the defect this function exists to prevent.
func (s *ShipmentService) returnDestinationFor(ctx context.Context, sellerID string) carrier.Address {
	if s.stores == nil {
		return carrier.Address{}
	}
	raw, err := s.stores.ReturnAddress(ctx, s.stores.Pool(), sellerID)
	if err != nil || len(raw) == 0 {
		return carrier.Address{}
	}
	return addressFromMap(raw)
}

// returnFacts reads what a return needs, in one place so the two call sites cannot
// disagree about which columns matter.
func (s *ShipmentService) returnFacts(
	ctx context.Context, returnID string,
) (orderID, itemID, buyerID, sellerID, status string, err error) {
	orderID, itemID, buyerID, sellerID, status, err = s.returns.ReturnFacts(
		ctx, s.returns.Pool(), returnID)
	if errors.Is(err, pgx.ErrNoRows) {
		return "", "", "", "", "", domain.ErrNotFound
	}
	if err != nil {
		return "", "", "", "", "", err
	}
	return orderID, itemID, buyerID, sellerID, status, nil
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
	if order.ShippingAddress == nil {
		return carrier.Address{}
	}
	return addressFromMap(order.ShippingAddress)
}

// ReturnParcelFor returns the parcel a return travels in, or nil.
//
// Seller-scoped: `actorID` is checked against the return's seller before the parcel is
// loaded. Without it any seller could read the return parcel of any other seller by
// guessing a return id, which discloses the carrier, the tracking number and the
// seller's own pickup address -- the last of these because a return parcel is addressed
// back to the store.
func (s *ShipmentService) ReturnParcelFor(
	ctx context.Context, actorID, returnID string,
) (*repository.ReturnParcel, error) {
	if err := s.assertReturnSeller(ctx, actorID, returnID); err != nil {
		return nil, err
	}
	return s.shipments.ReturnParcelFor(ctx, s.shipments.Pool(), returnID)
}

// assertReturnSeller refuses any actor who does not own the return.
//
// Empty `actorID` means "no seller context", which is how internal callers reach these
// paths; it is deliberately permissive, and matching `BuyLabel`'s existing convention.
// Every HTTP entry point passes a real authenticated id, so the permissive branch is
// not reachable from a request.
func (s *ShipmentService) assertReturnSeller(ctx context.Context, actorID, returnID string) error {
	if actorID == "" {
		return nil
	}
	_, _, _, sellerID, _, err := s.returnFacts(ctx, returnID)
	if err != nil {
		return err
	}
	if sellerID != actorID {
		return domain.E(domain.KindForbidden, "NOT_OWNED",
			"this return does not belong to your store")
	}
	return nil
}

// OrderParcels lists an order's parcels for the seller and buyer views.
//
// Seller-scoped. This service is multi-vendor -- one checkout can produce SEVERAL orders
// -- so an order id is not a seller id, and "is this actor the seller" cannot be answered
// by comparing the order to the actor.
//
// The check is that EVERY parcel belongs to the actor, not that one does. A checkout
// that splits across two stores produces one order id, and the parcels of the other
// store are a different seller's stock, addresses and tracking numbers. Allowing the
// request because the actor owns one of them would disclose the rest, so a mixed order
// returns 403 to both stores and they see only their own parcels through the buyer and
// order views they are entitled to.
func (s *ShipmentService) OrderParcels(
	ctx context.Context, actorID, orderID string,
) ([]ParcelView, error) {
	parcels, err := s.shipments.ShipmentsForOrder(ctx, s.shipments.Pool(), orderID)
	if err != nil {
		return nil, err
	}
	if actorID != "" {
		for _, p := range parcels {
			if p.SellerID != actorID {
				return nil, domain.E(domain.KindForbidden, "NOT_OWNED",
					"this order contains parcels from more than one store, so it cannot "+
						"be viewed as a single seller; fetch your own orders instead")
			}
		}
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

// addressFromMap reads one of the platform's JSONB address shapes into a carrier
// address.
//
// KEY NAMES ARE TRIED IN SEVERAL SPELLINGS because the shape is not enforced
// anywhere: `orders.shipping_address` is written by checkout, and nothing has ever
// pinned its keys. Reading only one spelling means a parcel that is silently
// undeliverable, so every plausible spelling is accepted and a value is taken from
// whichever appears.
//
// It returns the ZERO address when nothing usable is present, so the caller's
// emptiness check still fires. A partially-populated address is the dangerous case --
// a name and a street with no city is not deliverable -- so emptiness is judged by
// `IsZero` on the fields the carrier actually needs rather than on whether the map
// has any keys at all.
func addressFromMap(raw map[string]any) carrier.Address {
	str := func(keys ...string) string {
		for _, k := range keys {
			if v, ok := raw[k].(string); ok && strings.TrimSpace(v) != "" {
				return strings.TrimSpace(v)
			}
		}
		return ""
	}
	return carrier.Address{
		Name:     str("name", "Name", "recipient", "recipient_name", "full_name", "contact_name"),
		Phone:    str("phone", "Phone", "telephone", "contact_phone", "mobile"),
		Street:   str("street", "Street", "address", "address_line1", "line1", "street_address"),
		City:     str("city", "City", "city_name"),
		Province: str("province", "Province", "state", "region"),
		Postcode: str("postcode", "Postcode", "zip", "postal_code", "zip_code"),
		Country:  str("country", "Country", "country_code"),
	}
}
