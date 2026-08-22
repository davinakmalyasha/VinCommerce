package handler

import (
	"net/http"
	"strconv"
	"strings"

	"github.com/go-chi/chi/v5"
	"github.com/vincommerce/backend/internal/domain"
	"github.com/vincommerce/backend/internal/httpapi/middleware"
	"github.com/vincommerce/backend/internal/service"
)

// Catalog exposes category/brand/attribute queries.
type Catalog struct {
	svc *service.CatalogService
}

// NewCatalog creates a Catalog handler.
func NewCatalog(svc *service.CatalogService) *Catalog {
	return &Catalog{svc: svc}
}

// Tree handles GET /catalog/categories.
func (h *Catalog) Tree(w http.ResponseWriter, r *http.Request) {
	tree, err := h.svc.Tree(r.Context())
	if err != nil {
		writeErr(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"categories": tree})
}

// Brands handles GET /catalog/brands.
func (h *Catalog) Brands(w http.ResponseWriter, r *http.Request) {
	brands, err := h.svc.Brands(r.Context())
	if err != nil {
		writeErr(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"brands": brands})
}

// Attributes handles GET /catalog/attributes.
func (h *Catalog) Attributes(w http.ResponseWriter, r *http.Request) {
	attrs, err := h.svc.Attributes(r.Context())
	if err != nil {
		writeErr(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"attributes": attrs})
}

// CreateCategory handles POST /catalog/categories (admin).
func (h *Catalog) CreateCategory(w http.ResponseWriter, r *http.Request) {
	var req struct {
		ParentID *string `json:"parent_id,omitempty"`
		Name     string  `json:"name"`
		Position int     `json:"position"`
	}
	if err := decode(r, &req); err != nil {
		writeErr(w, r, domain.E(domain.KindInvalid, "BAD_JSON", err.Error()))
		return
	}
	if strings.TrimSpace(req.Name) == "" {
		writeErr(w, r, domain.E(domain.KindInvalid, "NAME_REQUIRED", "category name is required"))
		return
	}
	cat, err := h.svc.CreateCategory(r.Context(), req.ParentID, req.Name, req.Position)
	if err != nil {
		writeErr(w, r, err)
		return
	}
	writeJSON(w, http.StatusCreated, map[string]any{"category": cat})
}

// CreateBrand handles POST /catalog/brands (admin).
func (h *Catalog) CreateBrand(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Name    string `json:"name"`
		LogoURL string `json:"logo_url,omitempty"`
	}
	if err := decode(r, &req); err != nil {
		writeErr(w, r, domain.E(domain.KindInvalid, "BAD_JSON", err.Error()))
		return
	}
	b, err := h.svc.CreateBrand(r.Context(), req.Name, req.LogoURL)
	if err != nil {
		writeErr(w, r, err)
		return
	}
	writeJSON(w, http.StatusCreated, map[string]any{"brand": b})
}

// ToggleBrand handles POST /catalog/brands/{id}/toggle (admin).
func (h *Catalog) ToggleBrand(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Active bool `json:"active"`
	}
	if err := decode(r, &req); err != nil {
		writeErr(w, r, domain.E(domain.KindInvalid, "BAD_JSON", err.Error()))
		return
	}
	if err := h.svc.SetBrandActive(r.Context(), chi.URLParam(r, "id"), req.Active); err != nil {
		writeErr(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]bool{"updated": true})
}

// CreateAttribute handles POST /catalog/attributes (admin).
func (h *Catalog) CreateAttribute(w http.ResponseWriter, r *http.Request) {
	var req domain.Attribute
	if err := decode(r, &req); err != nil {
		writeErr(w, r, domain.E(domain.KindInvalid, "BAD_JSON", err.Error()))
		return
	}
	a, err := h.svc.CreateAttribute(r.Context(), &req)
	if err != nil {
		writeErr(w, r, err)
		return
	}
	writeJSON(w, http.StatusCreated, map[string]any{"attribute": a})
}

// Product exposes search and product detail.
type Product struct {
	svc *service.ProductService
}

// NewProduct creates a Product handler.
func NewProduct(svc *service.ProductService) *Product {
	return &Product{svc: svc}
}

// Search handles GET /products.
func (h *Product) Search(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()
	f := &domain.ProductFilter{
		Query:        q.Get("q"),
		CategoryID:   q.Get("category_id"),
		CategorySlug: q.Get("category"),
		Sort:         q.Get("sort"),
		Page:         intQuery(q.Get("page"), 1),
		PageSize:     intQuery(q.Get("page_size"), 24),
	}
	if brands := q.Get("brands"); brands != "" {
		f.BrandIDs = strings.Split(brands, ",")
	}
	if min := q.Get("min_price"); min != "" {
		if v, err := strconv.ParseFloat(min, 64); err == nil {
			f.MinPrice = &v
		}
	}
	if max := q.Get("max_price"); max != "" {
		if v, err := strconv.ParseFloat(max, 64); err == nil {
			f.MaxPrice = &v
		}
	}
	if rating := q.Get("rating"); rating != "" {
		if v, err := strconv.Atoi(rating); err == nil && v >= 1 && v <= 5 {
			f.Rating = &v
		}
	}
	f.AttrFilters = map[string][]string{}
	for key, vals := range q {
		if strings.HasPrefix(key, "attr_") && len(vals) > 0 {
			slug := strings.TrimPrefix(key, "attr_")
			f.AttrFilters[slug] = strings.Split(vals[0], ",")
		}
	}

	result, err := h.svc.Search(r.Context(), f)
	if err != nil {
		writeErr(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, result)
}

// BySlug handles GET /products/{slug}.
func (h *Product) BySlug(w http.ResponseWriter, r *http.Request) {
	p, err := h.svc.BySlug(r.Context(), chi.URLParam(r, "slug"))
	if err != nil {
		writeErr(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"product": p})
}

// Related handles GET /products/{id}/related.
func (h *Product) Related(w http.ResponseWriter, r *http.Request) {
	items, err := h.svc.Related(r.Context(), chi.URLParam(r, "id"), intQuery(r.URL.Query().Get("limit"), 8))
	if err != nil {
		writeErr(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"products": items})
}

// Reviews handles GET /products/{id}/reviews.
func (h *Product) Reviews(w http.ResponseWriter, r *http.Request) {
	page := intQuery(r.URL.Query().Get("page"), 1)
	pageSize := intQuery(r.URL.Query().Get("page_size"), 10)
	reviews, total, err := h.svc.ReviewsByProduct(r.Context(), chi.URLParam(r, "id"), page, pageSize)
	if err != nil {
		writeErr(w, r, err)
		return
	}
	distribution, _ := h.svc.RatingDistribution(r.Context(), chi.URLParam(r, "id"))
	writeJSON(w, http.StatusOK, map[string]any{
		"reviews": reviews, "total": total, "page": page, "page_size": pageSize, "distribution": distribution,
	})
}

// CreateReview handles POST /products/{id}/reviews (buyer).
func (h *Product) CreateReview(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Rating      int      `json:"rating"`
		Title       string   `json:"title,omitempty"`
		Content     string   `json:"content"`
		Images      []string `json:"images,omitempty"`
		OrderItemID *string  `json:"order_item_id,omitempty"`
	}
	if err := decode(r, &req); err != nil {
		writeErr(w, r, domain.E(domain.KindInvalid, "BAD_JSON", err.Error()))
		return
	}
	user := middleware.UserFrom(r.Context())
	rev, err := h.svc.CreateReview(r.Context(), service.CreateReviewInput{
		ProductID:   chi.URLParam(r, "id"),
		UserID:      user.ID,
		OrderItemID: req.OrderItemID,
		Rating:      req.Rating,
		Title:       req.Title,
		Content:     req.Content,
		Images:      req.Images,
	})
	if err != nil {
		writeErr(w, r, err)
		return
	}
	writeJSON(w, http.StatusCreated, map[string]any{"review": rev})
}

// ToggleReviewHelpful handles POST /reviews/{id}/helpful.
func (h *Product) ToggleReviewHelpful(w http.ResponseWriter, r *http.Request) {
	user := middleware.UserFrom(r.Context())
	helpful, count, err := h.svc.ToggleReviewHelpful(r.Context(), chi.URLParam(r, "id"), user.ID)
	if err != nil {
		writeErr(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"helpful": helpful, "helpful_count": count})
}

// ReportProduct handles POST /products/{id}/report (buyer).
func (h *Product) ReportProduct(w http.ResponseWriter, r *http.Request) {
	user := middleware.UserFrom(r.Context())
	var req struct {
		Reason      string `json:"reason"`
		Description string `json:"description,omitempty"`
	}
	if err := decode(r, &req); err != nil {
		writeErr(w, r, domain.E(domain.KindInvalid, "BAD_JSON", err.Error()))
		return
	}
	if err := h.svc.ReportProduct(r.Context(), service.ReportProductInput{
		ProductID: chi.URLParam(r, "id"), UserID: user.ID,
		Reason: req.Reason, Description: req.Description,
	}); err != nil {
		writeErr(w, r, err)
		return
	}
	writeJSON(w, http.StatusCreated, map[string]bool{"reported": true})
}

// AdminReports handles GET /admin/reports.
func (h *Product) AdminReports(w http.ResponseWriter, r *http.Request) {
	reports, err := h.svc.AdminReports(r.Context(), r.URL.Query().Get("status"))
	if err != nil {
		writeErr(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"reports": reports})
}

type resolveReportRequest struct {
	Note     string `json:"note,omitempty"`
	Takedown bool   `json:"takedown"`
}

// ResolveReport handles POST /admin/reports/{id}/resolve.
func (h *Product) ResolveReport(w http.ResponseWriter, r *http.Request) {
	var req resolveReportRequest
	if err := decode(r, &req); err != nil {
		writeErr(w, r, domain.E(domain.KindInvalid, "BAD_JSON", err.Error()))
		return
	}
	if err := h.svc.ResolveReport(r.Context(), chi.URLParam(r, "id"), req.Note, req.Takedown); err != nil {
		writeErr(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]bool{"resolved": true})
}

func intQuery(s string, def int) int {
	if s == "" {
		return def
	}
	v, err := strconv.Atoi(s)
	if err != nil {
		return def
	}
	return v
}
