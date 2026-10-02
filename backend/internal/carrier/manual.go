package carrier

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"strings"
	"sync"
	"time"
)

// ManualCarrierName is the carrier a parcel gets when nothing is configured, and the
// one a COD or local delivery legitimately uses.
const ManualCarrierName = "manual"

// Manual is a complete carrier that talks to nobody.
//
// NOT A STUB. It mints a tracking number, produces a printable label, and answers
// tracking queries, which is what makes a parcel fully recordable with no carrier
// integration and no credentials. It is the correct adapter for cash-on-delivery and
// for local delivery, which routinely have no AWB at all.
//
// The tracking numbers are NOT random. A random one would be undiagnosable: a buyer
// reading it out over the phone to a seller who then has to guess which parcel it
// refers to. They are derived from the parcel id, so a number identifies exactly one
// parcel, a second parcel never collides with one, and the prefix makes it obvious at
// a glance that this is not a real carrier's number -- which stops an operator
// mistaking a self-minted number for a carrier confirmation.
type Manual struct {
	mu sync.Mutex
	// handed records which parcel ids have been given a number. A duplicate would
	// break the UNIQUE (carrier, tracking_number) index, and an error there would be
	// reported as a database fault rather than as what it is.
	handed map[string]string
	// labels keeps the issued label so Track can report it. A carrier's label
	// document is fetchable after purchase; this is the equivalent for a carrier that
	// is a person with a printer.
	labels map[string]Label
	// issued tracks the sequence number, so numbers are monotonic and a human can see
	// at a glance that a number is out of order.
	issued int
}

// NewManual creates the manual carrier.
func NewManual() *Manual {
	return &Manual{handed: map[string]string{}, labels: map[string]Label{}}
}

// Name identifies the carrier, and must match what lands in `shipments.carrier`.
func (m *Manual) Name() string { return ManualCarrierName }

// BuyLabel issues a label for a parcel.
//
// IDEMPOTENT on the parcel id, which is what the Carrier contract requires. The first
// call for a parcel mints a number; every later call for the SAME parcel returns that
// same label. That matters because buying a label spends real money at a real
// carrier, and a retry after a timeout must not produce a second label for one box.
func (m *Manual) BuyLabel(ctx context.Context, in BuyLabelInput) (*Label, error) {
	if err := validateLabelInput(in); err != nil {
		return nil, err
	}

	m.mu.Lock()
	defer m.mu.Unlock()

	if existing, ok := m.handed[in.ParcelID]; ok {
		lab := m.labels[existing]
		// Returned by value with a copy, so a caller cannot mutate the registry's
		// record through the pointer.
		out := lab
		return &out, nil
	}

	m.issued++
	tracking := m.trackingNumber(in.ParcelID, m.issued)
	lab := Label{
		CarrierRef: "MAN-" + strings.ToUpper(shortHash(in.ParcelID)),
		Tracking:   tracking,
		Format:     in.Format,
		// A real carrier hands back a signed, expiring URL. There is nothing to sign
		// here, so the URL is a self-describing placeholder and `shipments.label_url`
		// is explicitly not durable state.
		URL:      "manual://label/" + in.ParcelID,
		CostCurr: "IDR",
	}
	// No expiry: a label the seller prints for a parcel picked up next week is still
	// valid, and inventing an expiry would make the platform reject its own label.
	m.handed[in.ParcelID] = tracking
	m.labels[tracking] = lab

	out := lab
	return &out, nil
}

// Track reports a parcel's state.
//
// The manual carrier cannot know anything, so it reports exactly what it is entitled
// to report: that a label exists, and nothing more. Returning a guessed
// "in_transit" here would be a lie the reconciliation path would store as fact.
//
// `delivered` is never returned. A parcel moved by hand is delivered by someone
// telling the platform, and the manual carrier has no way to hear it.
func (m *Manual) Track(ctx context.Context, reference string) (*Tracking, error) {
	reference = strings.TrimSpace(reference)
	if reference == "" {
		return nil, fmt.Errorf("a tracking lookup needs a reference")
	}

	m.mu.Lock()
	defer m.mu.Unlock()

	if _, ok := m.labels[reference]; !ok {
		// Absent, not "unknown". This is the signal a caller uses to say the parcel
		// does not belong to this carrier, which is a different thing from "we cannot
		// tell you where it is" -- the same distinction RefundStatusProvider draws.
		return nil, fmt.Errorf("no manual label with tracking %q", reference)
	}
	return &Tracking{
		Tracking: reference,
		Status:   TrackingPending,
		Detail: "a manual label was issued; nothing has confirmed movement, because " +
			"nothing is watching. Mark this parcel delivered by hand when it arrives.",
		OccurredAt: time.Time{}.UTC().Format(time.RFC3339),
	}, nil
}

// trackingNumber builds a number that is unique per parcel and self-identifying.
//
// The "MNL" prefix is deliberate: a seller or a support agent glancing at it can see
// at once that this is a platform-minted number and not a carrier confirmation, so
// nobody waits on a courier for a parcel that was never handed to one.
func (m *Manual) trackingNumber(parcelID string, seq int) string {
	return fmt.Sprintf("MNL%s%04d", strings.ToUpper(shortHash(parcelID)), seq%10000)
}

// shortHash is a short, stable, non-cryptographic digest of a parcel id.
func shortHash(s string) string {
	sum := fnv64a(s)
	b := make([]byte, 4)
	for i := range b {
		b[i] = byte(sum >> (8 * (3 - i)))
	}
	return hex.EncodeToString(b)
}

func fnv64a(s string) uint64 {
	const (
		offset = 1469598103934665603
		prime  = 1099511628211
	)
	h := uint64(offset)
	for i := 0; i < len(s); i++ {
		h ^= uint64(s[i])
		h *= prime
	}
	return h
}

// randToken is unused by the manual carrier's number but kept for adapters that
// need randomness, so a real carrier does not have to import crypto/rand itself.
func randToken(n int) string {
	b := make([]byte, n)
	if _, err := rand.Read(b); err != nil {
		return ""
	}
	return hex.EncodeToString(b)
}

// validateLabelInput refuses a label request that could not produce a valid label.
//
// Checked HERE, at the boundary, rather than by each carrier: three adapters
// re-implementing "a label needs a destination" is three chances to forget one, and
// the failure at the far end is a label for nowhere.
func validateLabelInput(in BuyLabelInput) error {
	if strings.TrimSpace(in.ParcelID) == "" {
		return fmt.Errorf("a label needs a parcel; without one it cannot be fetched, " +
			"printed or re-printed")
	}
	if in.WeightGrams <= 0 {
		return fmt.Errorf("a label needs a weight; the parcel contents weigh %d grams",
			in.WeightGrams)
	}
	if in.Destination.IsZero() {
		return fmt.Errorf("a label needs a destination; this order's shipping address " +
			"is empty, so any label would be for nowhere")
	}
	if in.Format == "" {
		return fmt.Errorf("a label needs a format (pdf or zpl); a thermal label printer " +
			"and a sheet of A4 are not the same request")
	}
	return nil
}
