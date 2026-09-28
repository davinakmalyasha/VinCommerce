package handler

import (
	"encoding/csv"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"

	"github.com/go-chi/chi/v5"
	"github.com/vincommerce/backend/internal/domain"
	"github.com/vincommerce/backend/internal/httpapi/middleware"
	"github.com/vincommerce/backend/internal/service"
)

// Seller exposes store management and seller tools.
type Seller struct {
	svc *service.SellerService
}

// NewSeller creates a Seller handler.
func NewSeller(svc *service.SellerService) *Seller {
	return &Seller{svc: svc}
}

// MyStore handles GET /seller/store.
func (h *Seller) MyStore(w http.ResponseWriter, r *http.Request) {
	user := middleware.UserFrom(r.Context())
	store, err := h.svc.MyStore(r.Context(), user.ID)
	if err != nil {
		writeErr(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"store": store})
}

type openStoreRequest struct {
	Name string `json:"name"`
}

// OpenStore handles POST /seller/store.
func (h *Seller) OpenStore(w http.ResponseWriter, r *http.Request) {
	user := middleware.UserFrom(r.Context())
	var req openStoreRequest
	if err := decode(r, &req); err != nil {
		writeErr(w, r, domain.E(domain.KindInvalid, "BAD_JSON", err.Error()))
		return
	}
	store, err := h.svc.OpenStore(r.Context(), user.ID, req.Name)
	if err != nil {
		writeErr(w, r, err)
		return
	}
	writeJSON(w, http.StatusCreated, map[string]any{"store": store})
}

// UpdateStore handles PUT /seller/store.
func (h *Seller) UpdateStore(w http.ResponseWriter, r *http.Request) {
	user := middleware.UserFrom(r.Context())
	var req struct {
		Name        string `json:"name"`
		Description string `json:"description,omitempty"`
		LogoURL     string `json:"logo_url,omitempty"`
		BannerURL   string `json:"banner_url,omitempty"`
	}
	if err := decode(r, &req); err != nil {
		writeErr(w, r, domain.E(domain.KindInvalid, "BAD_JSON", err.Error()))
		return
	}
	store, err := h.svc.UpdateStore(r.Context(), user.ID, &domain.Store{
		Name: req.Name, Description: req.Description, LogoURL: req.LogoURL, BannerURL: req.BannerURL,
	})
	if err != nil {
		writeErr(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"store": store})
}

type kycRequest struct {
	OwnerName     string `json:"owner_name"`
	IDNumber      string `json:"id_number"`
	IDDocumentURL string `json:"id_document_url,omitempty"`
	BankName      string `json:"bank_name"`
	BankAccount   string `json:"bank_account"`
}

// SubmitKYC handles POST /seller/kyc.
func (h *Seller) SubmitKYC(w http.ResponseWriter, r *http.Request) {
	user := middleware.UserFrom(r.Context())
	var req kycRequest
	if err := decode(r, &req); err != nil {
		writeErr(w, r, domain.E(domain.KindInvalid, "BAD_JSON", err.Error()))
		return
	}
	kyc, err := h.svc.SubmitKYC(r.Context(), user.ID, &domain.SellerKYC{
		OwnerName: req.OwnerName, IDNumber: req.IDNumber, IDDocumentURL: req.IDDocumentURL,
		BankName: req.BankName, BankAccount: req.BankAccount,
	})
	if err != nil {
		writeErr(w, r, err)
		return
	}
	writeJSON(w, http.StatusCreated, map[string]any{"kyc": kyc})
}

// KYC handles GET /seller/kyc.
func (h *Seller) KYC(w http.ResponseWriter, r *http.Request) {
	user := middleware.UserFrom(r.Context())
	kyc, err := h.svc.KYC(r.Context(), user.ID)
	if err != nil {
		writeErr(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"kyc": kyc})
}

// Dashboard handles GET /seller/dashboard.
func (h *Seller) Dashboard(w http.ResponseWriter, r *http.Request) {
	user := middleware.UserFrom(r.Context())
	h.svc.Heartbeat(r.Context(), user.ID)
	stats, err := h.svc.Dashboard(r.Context(), user.ID)
	if err != nil {
		writeErr(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, stats)
}

// Products handles GET /seller/products.
func (h *Seller) Products(w http.ResponseWriter, r *http.Request) {
	user := middleware.UserFrom(r.Context())
	items, total, err := h.svc.ListSellerProducts(r.Context(), user.ID, intQuery(r.URL.Query().Get("page"), 1), intQuery(r.URL.Query().Get("page_size"), 20))
	if err != nil {
		writeErr(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"products": items, "total": total})
}

type variantRequest struct {
	SKU            string            `json:"sku"`
	Name           string            `json:"name"`
	Price          float64           `json:"price"`
	CompareAtPrice *float64          `json:"compare_at_price,omitempty"`
	Stock          int               `json:"stock"`
	WeightGrams    int               `json:"weight_grams"`
	ImageURL       string            `json:"image_url,omitempty"`
	Attributes     map[string]string `json:"attributes,omitempty"`
}

type productRequest struct {
	Name        string            `json:"name"`
	CategoryID  string            `json:"category_id,omitempty"`
	BrandID     string            `json:"brand_id,omitempty"`
	Description string            `json:"description,omitempty"`
	Attributes  map[string]string `json:"attributes,omitempty"`
	Variants    []variantRequest  `json:"variants"`
	Images      []string          `json:"images,omitempty"`
}

// LowStock handles GET /seller/low-stock.
func (h *Seller) LowStock(w http.ResponseWriter, r *http.Request) {
	user := middleware.UserFrom(r.Context())
	items, err := h.svc.LowStockForSeller(r.Context(), user.ID)
	if err != nil {
		writeErr(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"variants": items})
}

// CreateProduct handles POST /seller/products.
func (h *Seller) CreateProduct(w http.ResponseWriter, r *http.Request) {
	user := middleware.UserFrom(r.Context())
	var req productRequest
	if err := decode(r, &req); err != nil {
		writeErr(w, r, domain.E(domain.KindInvalid, "BAD_JSON", err.Error()))
		return
	}
	variants := make([]service.VariantInput, 0, len(req.Variants))
	for _, v := range req.Variants {
		variants = append(variants, service.VariantInput{
			SKU: v.SKU, Name: v.Name, Price: v.Price, CompareAtPrice: v.CompareAtPrice,
			Stock: v.Stock, WeightGrams: v.WeightGrams, ImageURL: v.ImageURL, Attributes: v.Attributes, IsActive: true,
		})
	}
	p, err := h.svc.CreateProduct(r.Context(), service.CreateProductInput{
		SellerID: user.ID, Name: req.Name, CategoryID: req.CategoryID, BrandID: req.BrandID,
		Description: req.Description, Attributes: req.Attributes, Variants: variants, Images: req.Images,
	})
	if err != nil {
		writeErr(w, r, err)
		return
	}
	writeJSON(w, http.StatusCreated, map[string]any{"product": p})
}

// UpdateProduct handles PUT /seller/products/{id}.
func (h *Seller) UpdateProduct(w http.ResponseWriter, r *http.Request) {
	user := middleware.UserFrom(r.Context())
	var req productRequest
	if err := decode(r, &req); err != nil {
		writeErr(w, r, domain.E(domain.KindInvalid, "BAD_JSON", err.Error()))
		return
	}
	variants := make([]service.VariantInput, 0, len(req.Variants))
	for _, v := range req.Variants {
		variants = append(variants, service.VariantInput{
			SKU: v.SKU, Name: v.Name, Price: v.Price, CompareAtPrice: v.CompareAtPrice,
			Stock: v.Stock, WeightGrams: v.WeightGrams, ImageURL: v.ImageURL, Attributes: v.Attributes, IsActive: true,
		})
	}
	p, err := h.svc.UpdateProduct(r.Context(), user.ID, chi.URLParam(r, "id"), service.CreateProductInput{
		Name: req.Name, CategoryID: req.CategoryID, BrandID: req.BrandID,
		Description: req.Description, Attributes: req.Attributes, Variants: variants, Images: req.Images,
	})
	if err != nil {
		writeErr(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"product": p})
}

type transitionRequest struct {
	To             string `json:"to"`
	TrackingNumber string `json:"tracking_number,omitempty"`
	Carrier        string `json:"carrier,omitempty"`
}

// AdjustStock handles POST /seller/stock/{variantId}/adjust.
func (h *Seller) AdjustStock(w http.ResponseWriter, r *http.Request) {
	user := middleware.UserFrom(r.Context())
	var req struct {
		Delta int `json:"delta"`
	}
	if err := decode(r, &req); err != nil {
		writeErr(w, r, domain.E(domain.KindInvalid, "BAD_JSON", err.Error()))
		return
	}
	if req.Delta == 0 {
		writeErr(w, r, domain.E(domain.KindInvalid, "BAD_DELTA", "delta must be non-zero"))
		return
	}
	if err := h.svc.AdjustStock(r.Context(), user.ID, chi.URLParam(r, "variantId"), req.Delta); err != nil {
		writeErr(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]bool{"adjusted": true})
}

// ImportTemplate serves a downloadable CSV template.
func (h *Seller) ImportTemplate(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "text/csv; charset=utf-8")
	w.Header().Set("Content-Disposition", "attachment; filename=vincommerce-product-import.csv")
	w.Write([]byte("name,category_slug,sku,price,stock,weight_grams\nContoh Produk,electronics,SKU-001,100000,10,500\n"))
}

// SellerCoupons handles GET /seller/coupons.
func (h *Seller) SellerCoupons(w http.ResponseWriter, r *http.Request) {
	user := middleware.UserFrom(r.Context())
	items, err := h.svc.SellerCoupons(r.Context(), user.ID, r.URL.Query().Get("active") != "false")
	if err != nil {
		writeErr(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"coupons": items})
}

// CreateSellerCoupon handles POST /seller/coupons.
func (h *Seller) CreateSellerCoupon(w http.ResponseWriter, r *http.Request) {
	user := middleware.UserFrom(r.Context())
	var req struct {
		Code         string  `json:"code"`
		Type         string  `json:"type"`
		Value        float64 `json:"value"`
		MinSubtotal  float64 `json:"min_subtotal"`
		UsageLimit   int     `json:"usage_limit"`
		PerUserLimit int     `json:"per_user_limit"`
		ValidDays    int     `json:"valid_days"`
	}
	if err := decode(r, &req); err != nil {
		writeErr(w, r, domain.E(domain.KindInvalid, "BAD_JSON", err.Error()))
		return
	}
	c, err := h.svc.CreateSellerCoupon(r.Context(), user.ID, service.CreateCouponInput{
		Code: req.Code, Type: req.Type, Value: req.Value, MinSubtotal: req.MinSubtotal,
		UsageLimit: req.UsageLimit, PerUserLimit: req.PerUserLimit, ValidDays: req.ValidDays,
	})
	if err != nil {
		writeErr(w, r, err)
		return
	}
	writeJSON(w, http.StatusCreated, map[string]any{"coupon": c})
}

// SetFreeShipping handles PUT /seller/free-shipping.
func (h *Seller) SetFreeShipping(w http.ResponseWriter, r *http.Request) {
	user := middleware.UserFrom(r.Context())
	var req struct {
		Threshold *float64 `json:"threshold"`
	}
	if err := decode(r, &req); err != nil {
		writeErr(w, r, domain.E(domain.KindInvalid, "BAD_JSON", err.Error()))
		return
	}
	if err := h.svc.SetFreeShipping(r.Context(), user.ID, req.Threshold); err != nil {
		writeErr(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]bool{"updated": true})
}

// FulfillOrder handles POST /seller/orders/{id}/transition (pack/ship).
func (h *Seller) FulfillOrder(w http.ResponseWriter, r *http.Request) {
	user := middleware.UserFrom(r.Context())
	var req transitionRequest
	if err := decode(r, &req); err != nil {
		writeErr(w, r, domain.E(domain.KindInvalid, "BAD_JSON", err.Error()))
		return
	}
	if err := h.svc.FulfillOrder(r.Context(), user.ID, chi.URLParam(r, "id"), req.To, req.TrackingNumber, req.Carrier); err != nil {
		writeErr(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]bool{"transitioned": true})
}

// PublicStore handles GET /stores/{slug}.
func (h *Seller) PublicStore(w http.ResponseWriter, r *http.Request) {
	userID := ""
	if u := middleware.UserFrom(r.Context()); u != nil {
		userID = u.ID
	}
	store, products, err := h.svc.PublicStore(r.Context(), chi.URLParam(r, "slug"), userID)
	if err != nil {
		writeErr(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"store": store, "products": products})
}

// FollowStore handles POST /stores/{id}/follow.
func (h *Seller) FollowStore(w http.ResponseWriter, r *http.Request) {
	user := middleware.UserFrom(r.Context())
	count, err := h.svc.FollowStore(r.Context(), user.ID, chi.URLParam(r, "id"))
	if err != nil {
		writeErr(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"following": true, "follower_count": count})
}

// UnfollowStore handles DELETE /stores/{id}/follow.
func (h *Seller) UnfollowStore(w http.ResponseWriter, r *http.Request) {
	user := middleware.UserFrom(r.Context())
	if err := h.svc.UnfollowStore(r.Context(), user.ID, chi.URLParam(r, "id")); err != nil {
		writeErr(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]bool{"following": false})
}

// FollowedStores handles GET /followed-stores.
func (h *Seller) FollowedStores(w http.ResponseWriter, r *http.Request) {
	user := middleware.UserFrom(r.Context())
	stores, err := h.svc.FollowedStores(r.Context(), user.ID)
	if err != nil {
		writeErr(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"stores": stores})
}

// FollowedFeed handles GET /followed-stores/feed.
func (h *Seller) FollowedFeed(w http.ResponseWriter, r *http.Request) {
	user := middleware.UserFrom(r.Context())
	products, err := h.svc.FollowedFeed(r.Context(), user.ID, intQuery(r.URL.Query().Get("limit"), 12))
	if err != nil {
		writeErr(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"products": products})
}

// Import row and body caps.
//
// The previous version called reader.ReadAll() on an unbounded multipart body
// and then looped over every row, so a 5.000-row import held one HTTP request
// open past the 60s request timeout (the client then retried, creating
// duplicate products) and a large file could be buffered entirely in memory.
const (
	MaxImportBytes = 5 << 20 // 5 MiB of CSV
	MaxImportRows  = 5_000
)

// ImportProducts handles POST /seller/products/import (CSV multipart).
func (h *Seller) ImportProducts(w http.ResponseWriter, r *http.Request) {
	user := middleware.UserFrom(r.Context())

	r.Body = http.MaxBytesReader(w, r.Body, MaxImportBytes)
	if err := r.ParseMultipartForm(1 << 20); err != nil {
		if strings.Contains(err.Error(), "too large") {
			writeErr(w, r, domain.E(domain.KindInvalid, "FILE_TOO_LARGE",
				"CSV exceeds the maximum upload size"))
			return
		}
		writeErr(w, r, domain.E(domain.KindInvalid, "BAD_MULTIPART", "unable to parse upload"))
		return
	}
	defer func() {
		if r.MultipartForm != nil {
			_ = r.MultipartForm.RemoveAll()
		}
	}()

	file, _, err := r.FormFile("file")
	if err != nil {
		writeErr(w, r, domain.E(domain.KindInvalid, "FILE_REQUIRED", "CSV file is required"))
		return
	}
	defer file.Close()

	reader := csv.NewReader(file)
	reader.FieldsPerRecord = -1
	// NB: deliberately NOT setting ReuseRecord. It makes the reader reuse one
	// backing slice across calls, so appending the records would retain N
	// pointers to the same overwritten data.

	// Stream the rows instead of ReadAll, and stop at a hard row cap. A
	// rejected oversized import is a far better outcome than a duplicate
	// catalogue after a client-side retry.
	records := make([][]string, 0, 256)
	for {
		rec, err := reader.Read()
		if errors.Is(err, io.EOF) {
			break
		}
		if err != nil {
			writeErr(w, r, domain.E(domain.KindInvalid, "BAD_CSV", "unable to parse CSV: "+err.Error()))
			return
		}
		records = append(records, rec)
		if len(records) > MaxImportRows {
			writeErr(w, r, domain.E(domain.KindInvalid, "TOO_MANY_ROWS",
				fmt.Sprintf("import is limited to %d rows", MaxImportRows)))
			return
		}
	}

	if len(records) > 1 && strings.HasPrefix(strings.ToLower(records[0][0]), "name") {
		records = records[1:] // skip header
	}

	result, err := h.svc.ImportProductsCSV(r.Context(), user.ID, records)
	if err != nil {
		writeErr(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, result)
}

type productStatusRequest struct {
	Status string `json:"status"`
}

// UpdateProductStatus handles POST /seller/products/{id}/status.
func (h *Seller) UpdateProductStatus(w http.ResponseWriter, r *http.Request) {
	user := middleware.UserFrom(r.Context())
	var req productStatusRequest
	if err := decode(r, &req); err != nil {
		writeErr(w, r, domain.E(domain.KindInvalid, "BAD_JSON", err.Error()))
		return
	}
	if err := h.svc.UpdateProductStatus(r.Context(), user.ID, chi.URLParam(r, "id"), req.Status); err != nil {
		writeErr(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]bool{"updated": true})
}

// Returns handles GET /seller/returns.
func (h *Seller) Returns(w http.ResponseWriter, r *http.Request) {
	user := middleware.UserFrom(r.Context())
	items, err := h.svc.SellerReturns(r.Context(), user.ID, r.URL.Query().Get("status"))
	if err != nil {
		writeErr(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"returns": items})
}

type decideReturnRequest struct {
	Decision string `json:"decision"`
	Note     string `json:"note,omitempty"`
}

// DecideReturn handles POST /seller/returns/{id}/decide.
func (h *Seller) DecideReturn(w http.ResponseWriter, r *http.Request) {
	user := middleware.UserFrom(r.Context())
	var req decideReturnRequest
	if err := decode(r, &req); err != nil {
		writeErr(w, r, domain.E(domain.KindInvalid, "BAD_JSON", err.Error()))
		return
	}
	if err := h.svc.SellerDecideReturn(r.Context(), user.ID, chi.URLParam(r, "id"), req.Decision, req.Note); err != nil {
		writeErr(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]bool{"decided": true})
}

// BuyerReturns exposes buyer return flows.
type BuyerReturns struct {
	svc *service.SellerService
}

// NewBuyerReturns creates a BuyerReturns handler.
func NewBuyerReturns(svc *service.SellerService) *BuyerReturns {
	return &BuyerReturns{svc: svc}
}

// Request handles POST /returns.
func (h *BuyerReturns) Request(w http.ResponseWriter, r *http.Request) {
	user := middleware.UserFrom(r.Context())
	var req struct {
		OrderID      string   `json:"order_id"`
		OrderItemID  string   `json:"order_item_id"`
		IssueType    string   `json:"issue_type,omitempty"` // return | item_not_received
		Reason       string   `json:"reason"`
		Description  string   `json:"description"`
		EvidenceURLs []string `json:"evidence_urls"`
	}
	if err := decode(r, &req); err != nil {
		writeErr(w, r, domain.E(domain.KindInvalid, "BAD_JSON", err.Error()))
		return
	}
	item, err := h.svc.RequestReturn(r.Context(), service.CreateReturnInput{
		OrderID: req.OrderID, OrderItemID: req.OrderItemID, BuyerID: user.ID,
		IssueType: req.IssueType,
		Reason:    req.Reason, Description: req.Description, EvidenceURLs: req.EvidenceURLs,
	})
	if err != nil {
		writeErr(w, r, err)
		return
	}
	writeJSON(w, http.StatusCreated, map[string]any{"return_request": item})
}

// List handles GET /returns.
func (h *BuyerReturns) List(w http.ResponseWriter, r *http.Request) {
	user := middleware.UserFrom(r.Context())
	items, err := h.svc.MyReturns(r.Context(), user.ID)
	if err != nil {
		writeErr(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"returns": items})
}

// Admin exposes admin moderation endpoints.
type Admin struct {
	svc *service.SellerService
}

// NewAdmin creates an Admin handler.
func NewAdmin(svc *service.SellerService) *Admin {
	return &Admin{svc: svc}
}

// Stores handles GET /admin/stores.
func (h *Admin) Stores(w http.ResponseWriter, r *http.Request) {
	stores, err := h.svc.AdminListStores(r.Context(), true)
	if err != nil {
		writeErr(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"stores": stores})
}

// DecideStore handles POST /admin/stores/{id}/decide.
func (h *Admin) DecideStore(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Decision string `json:"decision"`
	}
	if err := decode(r, &req); err != nil {
		writeErr(w, r, domain.E(domain.KindInvalid, "BAD_JSON", err.Error()))
		return
	}
	if err := h.svc.AdminDecideStore(r.Context(), chi.URLParam(r, "id"), req.Decision); err != nil {
		writeErr(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]bool{"decided": true})
}

// KYCPending handles GET /admin/kyc — KYC submissions awaiting review.
func (h *Admin) KYCPending(w http.ResponseWriter, r *http.Request) {
	list, err := h.svc.AdminPendingKYC(r.Context())
	if err != nil {
		writeErr(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"kycs": list})
}

// DecideKYC handles POST /admin/stores/{id}/kyc/decide.
func (h *Admin) DecideKYC(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Decision string `json:"decision"`
		Note     string `json:"note,omitempty"`
	}
	if err := decode(r, &req); err != nil {
		writeErr(w, r, domain.E(domain.KindInvalid, "BAD_JSON", err.Error()))
		return
	}
	if err := h.svc.AdminDecideKYC(r.Context(), chi.URLParam(r, "id"), req.Decision, req.Note); err != nil {
		writeErr(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]bool{"decided": true})
}

// RefundReturn handles POST /admin/returns/{id}/refund.
func (h *Admin) RefundReturn(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Note string `json:"note,omitempty"`
	}
	if err := decode(r, &req); err != nil {
		writeErr(w, r, domain.E(domain.KindInvalid, "BAD_JSON", err.Error()))
		return
	}
	if err := h.svc.RefundReturn(r.Context(), chi.URLParam(r, "id"), req.Note); err != nil {
		writeErr(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]bool{"refunded": true})
}

// Returns handles GET /admin/returns?status=.
func (h *Admin) Returns(w http.ResponseWriter, r *http.Request) {
	items, err := h.svc.AdminReturns(r.Context(), r.URL.Query().Get("status"))
	if err != nil {
		writeErr(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"returns": items})
}

// AdminReviews exposes review moderation.
type AdminReviews struct {
	svc *service.ProductService
}

// NewAdminReviews creates an AdminReviews handler.
func NewAdminReviews(svc *service.ProductService) *AdminReviews {
	return &AdminReviews{svc: svc}
}

// Pending handles GET /admin/reviews/pending.
func (h *AdminReviews) Pending(w http.ResponseWriter, r *http.Request) {
	items, err := h.svc.PendingReviews(r.Context(), intQuery(r.URL.Query().Get("limit"), 50))
	if err != nil {
		writeErr(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"reviews": items})
}

type moderateRequest struct {
	Status string `json:"status"`
	Note   string `json:"note,omitempty"`
}

// Moderate handles POST /admin/reviews/{id}/moderate.
func (h *AdminReviews) Moderate(w http.ResponseWriter, r *http.Request) {
	var req moderateRequest
	if err := decode(r, &req); err != nil {
		writeErr(w, r, domain.E(domain.KindInvalid, "BAD_JSON", err.Error()))
		return
	}
	if err := h.svc.ModerateReview(r.Context(), chi.URLParam(r, "id"), req.Status, req.Note); err != nil {
		writeErr(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]bool{"moderated": true})
}
