package handler

import (
	"net/http"

	"github.com/go-chi/chi/v5"
	"github.com/redis/go-redis/v9"
	"github.com/vincommerce/backend/internal/domain"
	"github.com/vincommerce/backend/internal/httpapi/middleware"
	"github.com/vincommerce/backend/internal/service"
)

// AdminOps exposes coupon, user and feature-flag management.
type AdminOps struct {
	svc   *service.SellerService
	flags *service.FeatureFlagService
	rdb   *redis.Client
	auth  *service.AuthService
}

// NewAdminOps creates an AdminOps handler.
func NewAdminOps(svc *service.SellerService, flags *service.FeatureFlagService, rdb *redis.Client) *AdminOps {
	return &AdminOps{svc: svc, flags: flags, rdb: rdb}
}

// SetAuth enables impersonation support.
func (h *AdminOps) SetAuth(a *service.AuthService) { h.auth = a }

// --- users ---

// Users handles GET /admin/users.
func (h *AdminOps) Users(w http.ResponseWriter, r *http.Request) {
	users, total, err := h.svc.ListUsers(r.Context(), intQuery(r.URL.Query().Get("page"), 1), intQuery(r.URL.Query().Get("page_size"), 20))
	if err != nil {
		writeErr(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"users": users, "total": total})
}

type userStatusRequest struct {
	Status string `json:"status"`
}

// SetUserStatus handles POST /admin/users/{id}/status.
func (h *AdminOps) SetUserStatus(w http.ResponseWriter, r *http.Request) {
	var req userStatusRequest
	if err := decode(r, &req); err != nil {
		writeErr(w, r, domain.E(domain.KindInvalid, "BAD_JSON", err.Error()))
		return
	}
	if err := h.svc.SetUserStatus(r.Context(), chi.URLParam(r, "id"), req.Status); err != nil {
		writeErr(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]bool{"updated": true})
}

// GrantSeller handles POST /admin/users/{id}/grant-seller.
func (h *AdminOps) GrantSeller(w http.ResponseWriter, r *http.Request) {
	if err := h.svc.GrantSellerRole(r.Context(), chi.URLParam(r, "id")); err != nil {
		writeErr(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]bool{"granted": true})
}

// RevokeRole handles POST /admin/users/{id}/revoke-role.
func (h *AdminOps) RevokeRole(w http.ResponseWriter, r *http.Request) {
	admin := middleware.UserFrom(r.Context())
	var req struct {
		Role string `json:"role"`
	}
	if err := decode(r, &req); err != nil {
		writeErr(w, r, domain.E(domain.KindInvalid, "BAD_JSON", err.Error()))
		return
	}
	if err := h.svc.RevokeUserRole(r.Context(), admin.ID, chi.URLParam(r, "id"), req.Role); err != nil {
		writeErr(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]bool{"revoked": true})
}

// Impersonate handles POST /admin/users/{id}/impersonate — mints a
// short-lived access token for the target user (support tooling).
func (h *AdminOps) Impersonate(w http.ResponseWriter, r *http.Request) {
	user := middleware.UserFrom(r.Context())
	target, token, err := h.auth.Impersonate(r.Context(), user.ID, chi.URLParam(r, "id"), clientIP(r), r.UserAgent())
	if err != nil {
		writeErr(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"access_token": token, "user": target})
}

// --- coupons ---

// Coupons handles GET /admin/coupons.
func (h *AdminOps) Coupons(w http.ResponseWriter, r *http.Request) {
	items, err := h.svc.ListCoupons(r.Context())
	if err != nil {
		writeErr(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"coupons": items})
}

type couponCreateRequest struct {
	Code         string   `json:"code"`
	Type         string   `json:"type"`
	Value        float64  `json:"value"`
	MinSubtotal  float64  `json:"min_subtotal"`
	MaxDiscount  *float64 `json:"max_discount,omitempty"`
	UsageLimit   int      `json:"usage_limit"`
	PerUserLimit int      `json:"per_user_limit"`
	ValidDays    int      `json:"valid_days"`
}

// CreateCoupon handles POST /admin/coupons.
func (h *AdminOps) CreateCoupon(w http.ResponseWriter, r *http.Request) {
	var req couponCreateRequest
	if err := decode(r, &req); err != nil {
		writeErr(w, r, domain.E(domain.KindInvalid, "BAD_JSON", err.Error()))
		return
	}
	c, err := h.svc.CreateCoupon(r.Context(), service.CreateCouponInput{
		Code: req.Code, Type: req.Type, Value: req.Value, MinSubtotal: req.MinSubtotal,
		MaxDiscount: req.MaxDiscount, UsageLimit: req.UsageLimit,
		PerUserLimit: req.PerUserLimit, ValidDays: req.ValidDays,
	})
	if err != nil {
		writeErr(w, r, err)
		return
	}
	writeJSON(w, http.StatusCreated, map[string]any{"coupon": c})
}

type couponToggleRequest struct {
	Active bool `json:"active"`
}

// ToggleCoupon handles POST /admin/coupons/{id}/toggle.
func (h *AdminOps) ToggleCoupon(w http.ResponseWriter, r *http.Request) {
	var req couponToggleRequest
	if err := decode(r, &req); err != nil {
		writeErr(w, r, domain.E(domain.KindInvalid, "BAD_JSON", err.Error()))
		return
	}
	if err := h.svc.ToggleCoupon(r.Context(), chi.URLParam(r, "id"), req.Active); err != nil {
		writeErr(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]bool{"updated": true})
}

// --- feature flags ---
//
// The repository and the middleware both used to live here. The repository
// because the handler held it directly, and the middleware because the router
// reached through a handler constructor to get at it -- which meant a
// cross-cutting kill-switch policy had its only home inside a transport type.
// Both now belong to service.FeatureFlagService and middleware.RequireFeature.

// Flags handles GET /admin/flags.
func (h *AdminOps) Flags(w http.ResponseWriter, r *http.Request) {
	items, err := h.flags.List(r.Context())
	if err != nil {
		writeErr(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"flags": items})
}

// ToggleFlag handles POST /admin/flags/{key}/toggle.
func (h *AdminOps) ToggleFlag(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Enabled bool `json:"enabled"`
	}
	if err := decode(r, &req); err != nil {
		writeErr(w, r, domain.E(domain.KindInvalid, "BAD_JSON", err.Error()))
		return
	}
	if err := h.flags.SetEnabled(r.Context(), chi.URLParam(r, "key"), req.Enabled); err != nil {
		writeErr(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]bool{"updated": true})
}

// --- shipping methods ---

// Shipping handles GET /admin/shipping.
func (h *AdminOps) Shipping(w http.ResponseWriter, r *http.Request) {
	items, err := h.svc.ListShippingMethods(r.Context())
	if err != nil {
		writeErr(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"methods": items})
}

// PublicShipping handles GET /shipping/methods — active options for ETA display.
func (h *AdminOps) PublicShipping(w http.ResponseWriter, r *http.Request) {
	items, err := h.svc.ListShippingMethods(r.Context())
	if err != nil {
		writeErr(w, r, err)
		return
	}
	type opt struct {
		Code     string  `json:"code"`
		Name     string  `json:"name"`
		BaseFee  float64 `json:"base_fee"`
		PerKgFee float64 `json:"per_kg_fee"`
		MinDays  int     `json:"min_days"`
		MaxDays  int     `json:"max_days"`
	}
	out := make([]opt, 0, len(items))
	for _, m := range items {
		if !m.IsActive {
			continue
		}
		out = append(out, opt{Code: m.Code, Name: m.Name, BaseFee: m.BaseFee, PerKgFee: m.PerKgFee, MinDays: m.MinDays, MaxDays: m.MaxDays})
	}
	writeJSON(w, http.StatusOK, map[string]any{"methods": out})
}

type shippingCreateRequest struct {
	Code     string  `json:"code"`
	Name     string  `json:"name"`
	BaseFee  float64 `json:"base_fee"`
	PerKgFee float64 `json:"per_kg_fee"`
	MinDays  int     `json:"min_days"`
	MaxDays  int     `json:"max_days"`
}

// CreateShipping handles POST /admin/shipping.
func (h *AdminOps) CreateShipping(w http.ResponseWriter, r *http.Request) {
	var req shippingCreateRequest
	if err := decode(r, &req); err != nil {
		writeErr(w, r, domain.E(domain.KindInvalid, "BAD_JSON", err.Error()))
		return
	}
	m, err := h.svc.CreateShippingMethod(r.Context(), &domain.ShippingMethod{
		Code: req.Code, Name: req.Name, BaseFee: req.BaseFee, PerKgFee: req.PerKgFee,
		MinDays: req.MinDays, MaxDays: req.MaxDays, IsActive: true,
	})
	if err != nil {
		writeErr(w, r, err)
		return
	}
	writeJSON(w, http.StatusCreated, map[string]any{"method": m})
}

// ToggleShipping handles POST /admin/shipping/{id}/toggle.
func (h *AdminOps) ToggleShipping(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Active bool `json:"active"`
	}
	if err := decode(r, &req); err != nil {
		writeErr(w, r, domain.E(domain.KindInvalid, "BAD_JSON", err.Error()))
		return
	}
	if err := h.svc.ToggleShippingMethod(r.Context(), chi.URLParam(r, "id"), req.Active); err != nil {
		writeErr(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]bool{"updated": true})
}

// --- admin: orders, audit, commission ---

// Orders handles GET /admin/orders?q=&status=.
func (h *AdminOps) Orders(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()
	items, total, err := h.svc.SearchOrders(r.Context(), q.Get("q"), q.Get("status"),
		intQuery(q.Get("page"), 1), intQuery(q.Get("page_size"), 10))
	if err != nil {
		writeErr(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"orders": items, "total": total})
}

// Audit handles GET /admin/audit?actor=&action=.
func (h *AdminOps) Audit(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()
	items, err := h.svc.AuditLog(r.Context(), q.Get("actor"), q.Get("action"), intQuery(q.Get("limit"), 50))
	if err != nil {
		writeErr(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"entries": items})
}

// Commission handles GET /admin/commission.
func (h *AdminOps) Commission(w http.ResponseWriter, r *http.Request) {
	cfg, err := h.svc.CommissionConfig(r.Context())
	if err != nil {
		writeErr(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"fee": cfg})
}

// SetCommission handles PUT /admin/commission.
func (h *AdminOps) SetCommission(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Pct   float64 `json:"pct"`
		Fixed float64 `json:"fixed"`
	}
	if err := decode(r, &req); err != nil {
		writeErr(w, r, domain.E(domain.KindInvalid, "BAD_JSON", err.Error()))
		return
	}
	if err := h.svc.SetCommissionConfig(r.Context(), req.Pct, req.Fixed); err != nil {
		writeErr(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]bool{"updated": true})
}

// UserDetail handles GET /admin/users/{id}.
func (h *AdminOps) UserDetail(w http.ResponseWriter, r *http.Request) {
	detail, err := h.svc.UserDetail(r.Context(), chi.URLParam(r, "id"))
	if err != nil {
		writeErr(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"user": detail})
}

// Vouchers handles GET /vouchers (public collectible store coupons).
func (h *AdminOps) Vouchers(w http.ResponseWriter, r *http.Request) {
	items, err := h.svc.AllVouchers(r.Context())
	if err != nil {
		writeErr(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"vouchers": items})
}
