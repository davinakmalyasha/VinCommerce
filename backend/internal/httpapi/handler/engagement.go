package handler

import (
	"net/http"

	"github.com/go-chi/chi/v5"
	"github.com/vincommerce/backend/internal/httpapi/middleware"
	"github.com/vincommerce/backend/internal/service"
)

// Engagement exposes wishlist, flash sales and recommendations.
type Engagement struct {
	svc *service.EngagementService
}

// NewEngagement creates an Engagement handler.
func NewEngagement(svc *service.EngagementService) *Engagement {
	return &Engagement{svc: svc}
}

// Add handles POST /wishlist/items/{variantId}.
func (h *Engagement) Add(w http.ResponseWriter, r *http.Request) {
	user := middleware.UserFrom(r.Context())
	if err := h.svc.AddToWishlist(r.Context(), user.ID, chi.URLParam(r, "variantId")); err != nil {
		writeErr(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]bool{"saved": true})
}

// Remove handles DELETE /wishlist/items/{variantId}.
func (h *Engagement) Remove(w http.ResponseWriter, r *http.Request) {
	user := middleware.UserFrom(r.Context())
	if err := h.svc.RemoveFromWishlist(r.Context(), user.ID, chi.URLParam(r, "variantId")); err != nil {
		writeErr(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]bool{"removed": true})
}

// List handles GET /wishlist.
func (h *Engagement) List(w http.ResponseWriter, r *http.Request) {
	user := middleware.UserFrom(r.Context())
	items, total, err := h.svc.Wishlist(r.Context(), user.ID, intQuery(r.URL.Query().Get("page"), 1), intQuery(r.URL.Query().Get("page_size"), 20))
	if err != nil {
		writeErr(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"products": items, "total": total})
}

// FlashSale handles GET /flash-sales/active.
func (h *Engagement) FlashSale(w http.ResponseWriter, r *http.Request) {
	sale, err := h.svc.ActiveFlashSale(r.Context())
	if err != nil {
		writeErr(w, r, err)
		return
	}
	items, err := h.svc.FlashSaleItems(r.Context(), sale.ID)
	if err != nil {
		writeErr(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"flash_sale": sale, "items": items})
}

// Recommended handles GET /recommendations.
func (h *Engagement) Recommended(w http.ResponseWriter, r *http.Request) {
	items, err := h.svc.Recommended(r.Context(), intQuery(r.URL.Query().Get("limit"), 10))
	if err != nil {
		writeErr(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"products": items})
}

// Feed handles GET /feed — mixed discovery stream.
func (h *Engagement) Feed(w http.ResponseWriter, r *http.Request) {
	user := middleware.UserFrom(r.Context())
	var userID string
	if user != nil {
		userID = user.ID
	}
	items, err := h.svc.Feed(r.Context(), userID,
		intQuery(r.URL.Query().Get("page"), 1), intQuery(r.URL.Query().Get("limit"), 12))
	if err != nil {
		writeErr(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"items": items})
}
