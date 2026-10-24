package service

import "github.com/vincommerce/backend/internal/domain"

// matchedBundle is one bundle that applies to a cart, with the discount it yields.
type matchedBundle struct {
	bundle   *domain.Bundle
	discount float64
}

// matchBundles finds, per seller, the active bundle whose items EXACTLY match that
// seller's lines, choosing the deepest discount when several match.
//
// STRICT MATCH is the only rule the schema can express: `bundle_items` is keyed on
// (bundle_id, variant_id) with a quantity, so a bundle is a fixed set, not a
// threshold. A cart that contains one extra item from that seller does NOT match --
// otherwise every bundle would become a discount on an arbitrary selection.
//
// cartBySeller maps seller -> variant -> quantity. subtotals holds each seller's
// line subtotal, which is what the bundle's price is compared against; a bundle that
// is not cheaper than its parts yields no discount and is ignored, so a
// mis-configured bundle can never RAISE the price.
func matchBundles(
	cartBySeller map[string]map[string]int,
	bundles []*domain.Bundle,
	subtotals map[string]float64,
) map[string]matchedBundle {
	out := map[string]matchedBundle{}
	for _, b := range bundles {
		if b == nil || !b.IsActive || len(b.Items) == 0 {
			continue
		}
		cart, ok := cartBySeller[b.SellerID]
		if !ok {
			continue
		}
		// A bundle with a repeated variant would need quantity summing; the primary
		// key forbids it, and the length check below is what rejects an extra line.
		if len(cart) != len(b.Items) {
			continue
		}
		matches := true
		for _, it := range b.Items {
			if cart[it.VariantID] != it.Quantity {
				matches = false
				break
			}
		}
		if !matches {
			continue
		}
		discount := moneyRound(subtotals[b.SellerID] - b.Price)
		if discount <= 0 {
			continue
		}
		if cur, seen := out[b.SellerID]; !seen || discount > cur.discount {
			out[b.SellerID] = matchedBundle{bundle: b, discount: discount}
		}
	}
	return out
}
