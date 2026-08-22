package handler

import (
	"net/http"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/vincommerce/backend/internal/domain"
	"github.com/vincommerce/backend/internal/httpapi/middleware"
	"github.com/vincommerce/backend/internal/service"
)

// Market exposes Q&A, bundles, price alerts and flash-sale admin.
type Market struct {
	svc *service.MarketService
}

// NewMarket creates a Market handler.
func NewMarket(svc *service.MarketService) *Market {
	return &Market{svc: svc}
}

// Ask handles POST /products/{id}/qa.
func (h *Market) Ask(w http.ResponseWriter, r *http.Request) {
	user := middleware.UserFrom(r.Context())
	var req struct {
		Question string `json:"question"`
	}
	if err := decode(r, &req); err != nil {
		writeErr(w, r, domain.E(domain.KindInvalid, "BAD_JSON", err.Error()))
		return
	}
	q, err := h.svc.AskQuestion(r.Context(), chi.URLParam(r, "id"), user.ID, req.Question)
	if err != nil {
		writeErr(w, r, err)
		return
	}
	writeJSON(w, http.StatusCreated, map[string]any{"question": q})
}

// QA handles GET /products/{id}/qa.
func (h *Market) QA(w http.ResponseWriter, r *http.Request) {
	items, err := h.svc.QAByProduct(r.Context(), chi.URLParam(r, "id"), intQuery(r.URL.Query().Get("limit"), 20))
	if err != nil {
		writeErr(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"questions": items})
}

// Answer handles POST /qa/{id}/answer (seller).
func (h *Market) Answer(w http.ResponseWriter, r *http.Request) {
	user := middleware.UserFrom(r.Context())
	var req struct {
		Answer string `json:"answer"`
	}
	if err := decode(r, &req); err != nil {
		writeErr(w, r, domain.E(domain.KindInvalid, "BAD_JSON", err.Error()))
		return
	}
	q, err := h.svc.AnswerQuestion(r.Context(), chi.URLParam(r, "id"), user.ID, req.Answer)
	if err != nil {
		writeErr(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"question": q})
}

// Bundles handles GET /bundles?seller=.
func (h *Market) Bundles(w http.ResponseWriter, r *http.Request) {
	items, err := h.svc.Bundles(r.Context(), r.URL.Query().Get("seller"))
	if err != nil {
		writeErr(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"bundles": items})
}

// CreateBundle handles POST /seller/bundles.
func (h *Market) CreateBundle(w http.ResponseWriter, r *http.Request) {
	user := middleware.UserFrom(r.Context())
	var req struct {
		Name        string              `json:"name"`
		Description string              `json:"description,omitempty"`
		Price       float64             `json:"price"`
		Items       []domain.BundleItem `json:"items"`
	}
	if err := decode(r, &req); err != nil {
		writeErr(w, r, domain.E(domain.KindInvalid, "BAD_JSON", err.Error()))
		return
	}
	b, err := h.svc.CreateBundle(r.Context(), &domain.Bundle{
		SellerID: user.ID, Name: req.Name, Description: req.Description, Price: req.Price, IsActive: true,
	}, req.Items)
	if err != nil {
		writeErr(w, r, err)
		return
	}
	writeJSON(w, http.StatusCreated, map[string]any{"bundle": b})
}

// Watch handles POST /price-alerts.
func (h *Market) Watch(w http.ResponseWriter, r *http.Request) {
	user := middleware.UserFrom(r.Context())
	var req struct {
		VariantID   string  `json:"variant_id"`
		TargetPrice float64 `json:"target_price"`
	}
	if err := decode(r, &req); err != nil {
		writeErr(w, r, domain.E(domain.KindInvalid, "BAD_JSON", err.Error()))
		return
	}
	a, err := h.svc.WatchPrice(r.Context(), user.ID, req.VariantID, req.TargetPrice)
	if err != nil {
		writeErr(w, r, err)
		return
	}
	writeJSON(w, http.StatusCreated, map[string]any{"alert": a})
}

// PriceAlerts handles GET /price-alerts.
func (h *Market) PriceAlerts(w http.ResponseWriter, r *http.Request) {
	user := middleware.UserFrom(r.Context())
	items, err := h.svc.MyPriceAlerts(r.Context(), user.ID)
	if err != nil {
		writeErr(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"alerts": items})
}

// CancelAlert handles DELETE /price-alerts/{id}.
func (h *Market) CancelAlert(w http.ResponseWriter, r *http.Request) {
	user := middleware.UserFrom(r.Context())
	if err := h.svc.CancelPriceAlert(r.Context(), chi.URLParam(r, "id"), user.ID); err != nil {
		writeErr(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]bool{"cancelled": true})
}

// WatchRestock handles POST /back-in-stock.
func (h *Market) WatchRestock(w http.ResponseWriter, r *http.Request) {
	user := middleware.UserFrom(r.Context())
	var req struct {
		VariantID string `json:"variant_id"`
	}
	if err := decode(r, &req); err != nil {
		writeErr(w, r, domain.E(domain.KindInvalid, "BAD_JSON", err.Error()))
		return
	}
	a, err := h.svc.WatchRestock(r.Context(), user.ID, req.VariantID)
	if err != nil {
		writeErr(w, r, err)
		return
	}
	writeJSON(w, http.StatusCreated, map[string]any{"alert": a})
}

// BackInStock handles GET /back-in-stock.
func (h *Market) BackInStock(w http.ResponseWriter, r *http.Request) {
	user := middleware.UserFrom(r.Context())
	items, err := h.svc.MyBackInStockAlerts(r.Context(), user.ID)
	if err != nil {
		writeErr(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"alerts": items})
}

// CancelBackInStock handles DELETE /back-in-stock/{id}.
func (h *Market) CancelBackInStock(w http.ResponseWriter, r *http.Request) {
	user := middleware.UserFrom(r.Context())
	if err := h.svc.CancelBackInStock(r.Context(), chi.URLParam(r, "id"), user.ID); err != nil {
		writeErr(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]bool{"cancelled": true})
}

// FlashSales handles GET /admin/flash-sales.
func (h *Market) FlashSales(w http.ResponseWriter, r *http.Request) {
	items, err := h.svc.ListFlashSales(r.Context())
	if err != nil {
		writeErr(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"sales": items})
}

// CreateFlashSale handles POST /admin/flash-sales.
func (h *Market) CreateFlashSale(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Name        string `json:"name"`
		Description string `json:"description,omitempty"`
		StartsAt    string `json:"starts_at"`
		EndsAt      string `json:"ends_at"`
	}
	if err := decode(r, &req); err != nil {
		writeErr(w, r, domain.E(domain.KindInvalid, "BAD_JSON", err.Error()))
		return
	}
	start, err1 := time.Parse(time.RFC3339, req.StartsAt)
	end, err2 := time.Parse(time.RFC3339, req.EndsAt)
	if err1 != nil || err2 != nil {
		writeErr(w, r, domain.E(domain.KindInvalid, "BAD_TIME", "times must be RFC3339"))
		return
	}
	sale, err := h.svc.CreateFlashSale(r.Context(), req.Name, req.Description, start, end)
	if err != nil {
		writeErr(w, r, err)
		return
	}
	writeJSON(w, http.StatusCreated, map[string]any{"flash_sale": sale})
}

// AddFlashSaleItems handles POST /admin/flash-sales/{id}/items.
func (h *Market) AddFlashSaleItems(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Items []domain.FlashSaleItem `json:"items"`
	}
	if err := decode(r, &req); err != nil {
		writeErr(w, r, domain.E(domain.KindInvalid, "BAD_JSON", err.Error()))
		return
	}
	if err := h.svc.AddFlashSaleItems(r.Context(), chi.URLParam(r, "id"), req.Items); err != nil {
		writeErr(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]bool{"added": true})
}

// ToggleFlashSale handles POST /admin/flash-sales/{id}/toggle.
func (h *Market) ToggleFlashSale(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Active bool `json:"active"`
	}
	if err := decode(r, &req); err != nil {
		writeErr(w, r, domain.E(domain.KindInvalid, "BAD_JSON", err.Error()))
		return
	}
	if err := h.svc.SetFlashSaleActive(r.Context(), chi.URLParam(r, "id"), req.Active); err != nil {
		writeErr(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]bool{"updated": true})
}

// Loyalty handles GET /loyalty.
func (h *Market) Loyalty(w http.ResponseWriter, r *http.Request) {
	user := middleware.UserFrom(r.Context())
	balance, ledger, err := h.svc.LoyaltyBalance(r.Context(), user.ID)
	if err != nil {
		writeErr(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"balance": balance, "ledger": ledger})
}

// Referral handles GET /referral/code.
func (h *Market) Referral(w http.ResponseWriter, r *http.Request) {
	user := middleware.UserFrom(r.Context())
	code, err := h.svc.MyReferralCode(r.Context(), user.ID)
	if err != nil {
		writeErr(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"code": code})
}

// RedeemReferral handles POST /auth/referral/redeem (during registration).
func (h *Market) RedeemReferral(w http.ResponseWriter, r *http.Request) {
	user := middleware.UserFrom(r.Context())
	var req struct {
		Code string `json:"code"`
	}
	if err := decode(r, &req); err != nil {
		writeErr(w, r, domain.E(domain.KindInvalid, "BAD_JSON", err.Error()))
		return
	}
	if err := h.svc.RedeemReferral(r.Context(), user.ID, req.Code); err != nil {
		writeErr(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]bool{"redeemed": true})
}

// OpenDispute handles POST /disputes.
func (h *Market) OpenDispute(w http.ResponseWriter, r *http.Request) {
	user := middleware.UserFrom(r.Context())
	var req struct {
		ReturnID    string `json:"return_id"`
		Subject     string `json:"subject"`
		Description string `json:"description"`
	}
	if err := decode(r, &req); err != nil {
		writeErr(w, r, domain.E(domain.KindInvalid, "BAD_JSON", err.Error()))
		return
	}
	d, err := h.svc.OpenDispute(r.Context(), req.ReturnID, user.ID, req.Subject, req.Description)
	if err != nil {
		writeErr(w, r, err)
		return
	}
	writeJSON(w, http.StatusCreated, map[string]any{"dispute": d})
}

// Disputes handles GET /admin/disputes.
func (h *Market) Disputes(w http.ResponseWriter, r *http.Request) {
	items, err := h.svc.Disputes(r.Context(), r.URL.Query().Get("status"))
	if err != nil {
		writeErr(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"disputes": items})
}

// ResolveDispute handles POST /admin/disputes/{id}/resolve.
func (h *Market) ResolveDispute(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Decision string `json:"decision"`
		Note     string `json:"note,omitempty"`
	}
	if err := decode(r, &req); err != nil {
		writeErr(w, r, domain.E(domain.KindInvalid, "BAD_JSON", err.Error()))
		return
	}
	if err := h.svc.ResolveDispute(r.Context(), chi.URLParam(r, "id"), req.Decision, req.Note); err != nil {
		writeErr(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]bool{"resolved": true})
}
