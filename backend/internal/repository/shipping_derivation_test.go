package repository

import (
	"strings"
	"testing"
)

// MarkShipmentDelivered is a primitive for an OBSERVED carrier arrival. Nothing in
// this codebase receives one -- there are payment webhooks but no shipment webhooks
// and no tracking poll -- so it has no caller and parcel delivery is never recorded.
//
// The tempting "fix" is to call it from the order's delivered transition. That is
// wrong: it would record an arrival nobody observed as fact, and it would set
// `delivered_at`, which BuyLabel reads to refuse a label. A fabricated record would
// turn into money not spent.
//
// The reason the shortcut is not merely wrong but UNNECESSARY is that order delivery
// runs through its own path (ConfirmDelivery, AdvanceShippedOrders, CompleteDelivered)
// and never consults parcel status. This test pins the part of that which is local to
// this package: the shipping derivation cannot claim `delivered`, so an unwired
// parcel-level recorder cannot leave an order stuck short of one.
func TestShippingDerivationCannotClaimDelivery(t *testing.T) {
	src := orderRepoSource(t)
	at := strings.Index(src, "func (r *OrderRepository) DeriveShippingStatus(")
	if at < 0 {
		t.Fatal("DeriveShippingStatus is gone; update this test")
	}
	rest := src[at+1:]
	end := strings.Index(rest, "\nfunc ")
	if end < 0 {
		t.Fatal("could not bound DeriveShippingStatus")
	}
	body := src[at : at+1+end]

	if strings.Contains(body, "OrderDelivered") || strings.Contains(body, `"delivered"`) {
		t.Errorf("DeriveShippingStatus can now derive `delivered`.\n%s\n"+
			"Parcel status never reaches delivered -- MarkShipmentDelivered has no "+
			"caller -- so an order derived from parcel state would be stuck short of "+
			"delivered and escrow would not release. Order delivery must stay on its "+
			"own path (ConfirmDelivery / AdvanceShippedOrders)", body)
	}
	// And it must still derive the two states it is responsible for.
	if !strings.Contains(body, "domain.OrderShipped") {
		t.Errorf("DeriveShippingStatus no longer derives `shipped`.\n%s\n"+
			"Without it, dispatching a parcel would leave the order stuck at paid", body)
	}
	if !strings.Contains(body, "domain.OrderPartiallyShipped") {
		t.Errorf("DeriveShippingStatus no longer derives `partially_shipped`.\n%s\n"+
			"A multi-parcel order would jump straight to shipped on the first parcel", body)
	}
}
