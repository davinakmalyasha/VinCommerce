package handler

import (
	"net/http"

	"github.com/go-chi/chi/v5"
	"github.com/vincommerce/backend/internal/domain"
	"github.com/vincommerce/backend/internal/httpapi/middleware"
	"github.com/vincommerce/backend/internal/service"
)

// AdminClaims is the queue for off-platform payment claims.
//
// A claim is the buyer's assertion that a transfer was made. It is not money: the
// order stays `pending` until an admin approves it, and approval is the only route
// from a claim to a paid order.
type AdminClaims struct {
	svc *service.OrderService
}

// NewAdminClaims creates the handler.
func NewAdminClaims(svc *service.OrderService) *AdminClaims {
	return &AdminClaims{svc: svc}
}

// Pending handles GET /admin/external-payments.
func (h *AdminClaims) Pending(w http.ResponseWriter, r *http.Request) {
	claims, err := h.svc.PendingExternalPaymentClaims(r.Context())
	if err != nil {
		writeErr(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"claims": claims})
}

// Decide handles POST /admin/external-payments/{id}/decide.
func (h *AdminClaims) Decide(w http.ResponseWriter, r *http.Request) {
	admin := middleware.UserFrom(r.Context())
	var req struct {
		Decision string `json:"decision"` // approved | rejected
		Note     string `json:"note,omitempty"`
	}
	if err := decode(r, &req); err != nil {
		writeErr(w, r, domain.E(domain.KindInvalid, "BAD_JSON", err.Error()))
		return
	}
	var err error
	switch req.Decision {
	case "approved":
		err = h.svc.ApproveExternalPaymentClaim(r.Context(), admin.ID, chi.URLParam(r, "id"))
	case "rejected":
		err = h.svc.RejectExternalPaymentClaim(r.Context(), admin.ID, chi.URLParam(r, "id"), req.Note)
	default:
		writeErr(w, r, domain.E(domain.KindInvalid, "BAD_DECISION",
			"decision must be approved or rejected"))
		return
	}
	if err != nil {
		writeErr(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]bool{"ok": true})
}
