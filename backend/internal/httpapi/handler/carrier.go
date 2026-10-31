package handler

import (
	"io"
	"net/http"

	"github.com/go-chi/chi/v5"
	"github.com/vincommerce/backend/internal/domain"
	"github.com/vincommerce/backend/internal/service"
)

// CarrierTracking receives signed parcel events from a carrier or aggregator.
type CarrierTracking struct {
	svc *service.ShipmentService
}

// NewCarrierTracking creates the handler.
func NewCarrierTracking(svc *service.ShipmentService) *CarrierTracking {
	return &CarrierTracking{svc: svc}
}

// Webhook handles POST /shipping/tracking/webhook/{carrier}.
//
// Public by necessity (the carrier has no session) and therefore authenticated
// by HMAC over the raw body. The body is read with a hard cap before parsing:
// an unbounded read on an unauthenticated route is a trivial memory DoS, and
// the signature covers the bytes that were read, so the cap cannot be used to
// smuggle a truncated-but-valid payload without the signature changing.
func (h *CarrierTracking) Webhook(w http.ResponseWriter, r *http.Request) {
	body, err := io.ReadAll(io.LimitReader(r.Body, 64<<10))
	if err != nil {
		writeErr(w, r, domain.E(domain.KindInvalid, "BAD_BODY", "unable to read payload"))
		return
	}
	if err := h.svc.HandleCarrierWebhook(
		r.Context(), chi.URLParam(r, "carrier"), body, r.Header.Get("X-Carrier-Signature"),
	); err != nil {
		writeErr(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]bool{"received": true})
}
