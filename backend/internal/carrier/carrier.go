// Package carrier is the boundary between a parcel and whatever physically moves it.
//
// The payment side of this system went through a gateway interface for its whole
// life, so refunds could be sent back through the provider instead of into an
// internal balance nobody could spend. Shipping had no equivalent: `FulfillOrder`
// took a tracking number as a string and a carrier as a string, both typed by a
// seller, and there was nowhere to get a label, nowhere to ask where a parcel was,
// and no way for a carrier to tell us anything.
//
// # WHY `manual` IS IMPLEMENTED RATHER THAN STUBBED
//
// A stub would have been the obvious first step and it would have been useless. The
// `manual` implementation is a COMPLETE carrier: it mints a tracking number, issues
// a printable label, and answers tracking queries. It just does none of it by talking
// to anybody. That is not a placeholder; it is the correct adapter for COD and local
// delivery, which have no AWB, and it means the parcel path is fully exercised in
// production today with no credentials at all.
//
// The alternative -- refusing to record a parcel until a real carrier is configured
// -- would have left every COD order unrepresentable, which is the exact class of
// defect `00050` was written to remove.
package carrier

import (
	"context"
	"fmt"
	"strings"
)

// Carrier is a way to move a parcel.
//
// Deliberately SMALL. Every method here is one this system has an actual caller for.
// A shipping integration is where speculative interface methods go to die: an
// `EstimateCost` nobody calls, a `Cancel` nobody wires, and a mock that implements
// twelve methods to satisfy a test that uses one.
type Carrier interface {
	// Name is the identifier stored on `shipments.carrier`. It must be stable and
	// lower-case, because it is part of the UNIQUE index on (carrier, tracking_number):
	// a carrier that reports itself as "JNE" on one path and "jne" on another splits
	// its own tracking numbers across two namespaces and loses the duplicate check.
	Name() string

	// BuyLabel reserves a shipping label for a parcel.
	//
	// Costing money at a carrier is IRREVERSIBLE, so this is idempotent on
	// `Idempotency`. A retry after a timeout must not buy a second label for the same
	// parcel: the first one is already paid for, and the seller now has two labels
	// for one box and no way to tell which is live.
	BuyLabel(ctx context.Context, in BuyLabelInput) (*Label, error)

	// Track reports where a parcel is now. Optional for a carrier that cannot say --
	// see TrackingCarrier.
	Track(ctx context.Context, reference string) (*Tracking, error)
}

// BuyLabelInput is a request for a label.
type BuyLabelInput struct {
	ParcelID    string
	OrderID     string
	OrderNumber string
	Carrier     string
	Service     string
	// Weight is in GRAMS and is DERIVED from the parcel's contents by the shipment
	// service. It is not accepted from a seller, because a weight the seller types is
	// a weight the carrier charges for and a parcel is delivered against.
	WeightGrams int
	// Format is pdf or zpl. ZPL is what a thermal label printer wants; a seller
	// printing a 4x6 from a PDF re-sizes it and half the barcodes stop scanning.
	Format      string
	Origin      Address
	Destination Address
	// Idempotency must be stable across retries of the SAME label purchase and
	// different for a genuinely new one. `shipments.id` is the right key: one label
	// per parcel, for ever.
	Idempotency string
	// ContainsCod tells the carrier to collect money on delivery. It changes the
	// label's service class and its price, so a parcel that is COD and one that is
	// prepaid must never share a label.
	ContainsCod bool
}

// Address is where a parcel goes, and where it comes from.
type Address struct {
	Name     string
	Phone    string
	Street   string
	City     string
	Province string
	Postcode string
	Country  string
}

// IsZero reports whether nothing at all was supplied.
//
// Used to refuse a label request for an order whose shipping address is empty,
// rather than sending a blank address to a carrier and getting back a label for
// nowhere.
func (a Address) IsZero() bool {
	return strings.TrimSpace(a.Name) == "" && strings.TrimSpace(a.Street) == "" &&
		strings.TrimSpace(a.City) == "" && strings.TrimSpace(a.Postcode) == ""
}

// Label is a bought, printable label.
type Label struct {
	CarrierRef string // the carrier's own id for the parcel
	Tracking   string // what the buyer is shown
	Format     string // pdf | zpl
	// URL is a pointer to the label document. It is signed and EXPIRING in every real
	// carrier, which is why `shipments.label_url` is not treated as durable state and
	// the audit trail keeps `label_format` and `label_created_at` instead.
	URL string
	// ExpiresAt is zero when the carrier does not say. A label that expires and is
	// still believed valid is a parcel that is refused at the depot.
	ExpiresAt  string
	CostAmount float64
	CostCurr   string
}

// Tracking is a carrier's current view of a parcel.
type Tracking struct {
	Tracking string
	// Status is one of the Tracking* constants, NOT the shipment status. They are
	// deliberately different vocabularies: a carrier says "delivered", the platform
	// says "delivered", but a carrier also says "attempted" and "returning" which have
	// no shipment equivalent, and forcing them into one enum loses information that is
	// the whole reason to ask.
	Status     string
	Detail     string
	OccurredAt string
	// Raw is the provider payload, kept so a disagreement about what the carrier said
	// can be settled from evidence rather than from memory.
	Raw string
}

// Carrier tracking states. Mapped onto shipment statuses by a translator, never
// assigned directly -- see `MapTrackingStatus`.
const (
	TrackingPending   = "pending"
	TrackingPickedUp  = "picked_up"
	TrackingInTransit = "in_transit"
	TrackingAttempted = "attempted"
	TrackingDelivered = "delivered"
	TrackingReturned  = "returning"
	TrackingFailed    = "failed"
	TrackingUnknown   = "unknown"
)

// TrackingCarrier is a carrier that can report where a parcel is.
//
// SEPARATE from Carrier on purpose, for the same reason RefundStatusProvider is
// separate from Gateway: tracking is needed by the webhook reconciliation path and
// the admin view, and folding it in would force every adapter and every test double
// to implement a method one path alone uses. The type assertion is checked with the
// two-value form so a carrier that cannot track fails loudly rather than reporting
// every parcel as `unknown` forever.
type TrackingCarrier interface {
	Track(ctx context.Context, reference string) (*Tracking, error)
}

// LabelRegistry resolves a carrier by name.
//
// A map rather than a switch, so registering a carrier is one line at wiring time and
// forgetting one is a lookup failure at the point of use -- which names the carrier
// that is missing -- instead of a default branch that quietly returns the manual
// carrier for a typo. THAT failure mode matters: a seller asking for "jnE" and
// silently receiving a self-minted tracking number is worse than being told the
// carrier is not configured.
type LabelRegistry struct {
	carriers map[string]Carrier
}

// NewLabelRegistry builds a registry. `manual` is always present, because COD and
// local delivery have no AWB and must stay recordable.
func NewLabelRegistry(carriers ...Carrier) *LabelRegistry {
	r := &LabelRegistry{carriers: map[string]Carrier{}}
	for _, c := range carriers {
		if c == nil {
			continue
		}
		name := strings.ToLower(strings.TrimSpace(c.Name()))
		if name == "" {
			// A nameless carrier would occupy the "" key and be reachable by an
			// empty carrier name on a parcel, which is a silent no-op label.
			continue
		}
		r.carriers[name] = c
	}
	return r
}

// Get resolves a carrier, defaulting an empty name to `manual`.
func (r *LabelRegistry) Get(name string) (Carrier, error) {
	key := strings.ToLower(strings.TrimSpace(name))
	if key == "" {
		key = ManualCarrierName
	}
	c, ok := r.carriers[key]
	if !ok {
		return nil, fmt.Errorf("carrier %q is not configured; configured carriers are %v", key, r.Names())
	}
	return c, nil
}

// Names lists the configured carriers, for an error message and an operator view.
func (r *LabelRegistry) Names() []string {
	out := make([]string, 0, len(r.carriers))
	for n := range r.carriers {
		out = append(out, n)
	}
	return out
}

// MapTrackingStatus translates a carrier state into a shipment status.
//
// Returns ok=false for a state with no shipment equivalent -- `attempted` and
// `returning` in particular. Those are NOT failures: a missed delivery usually
// re-delivers, and a parcel on its way back is a parcel that exists.
//
// The shipment is left at `in_transit` and the carrier's own words are kept, rather
// than inventing a terminal state. The F1 schema has `exception` for exactly this and
// deliberately did NOT make it terminal.
func MapTrackingStatus(carrierStatus string) (shipmentStatus string, ok bool) {
	switch strings.ToLower(strings.TrimSpace(carrierStatus)) {
	case TrackingPending:
		return "in_transit", true
	case TrackingPickedUp, TrackingInTransit:
		return "in_transit", true
	case TrackingAttempted, TrackingReturned:
		// Recoverable. Marked as an exception so an operator sees it, and explicitly
		// NOT a failure: a parcel stuck in a failure state cannot later be delivered
		// by `MarkShipmentDelivered`'s own guard, which is why that guard includes
		// 'exception'.
		return "exception", true
	case TrackingDelivered:
		return "delivered", true
	case TrackingFailed:
		// A hard carrier failure. `cancelled` rather than `voided`: the parcel was not
		// merely a spent label, the carrier is telling us it will not move.
		return "cancelled", true
	default:
		return "", false
	}
}
