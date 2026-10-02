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

// parcelLineRequest is one line the seller says is in a parcel.
type parcelLineRequest struct {
	OrderItemID string `json:"order_item_id"`
	Quantity    int    `json:"quantity"`
}

// createParcelRequest asks for a parcel.
//
// A whole-parcel request. There is deliberately no "contents" field: letting the
// caller name the contents freely is how a parcel ends up holding units that were
// never bought, and the shipped_quantity counter is the thing that has to be
// maintained by the server, not trusted to the request.
type createParcelRequest struct {
	Carrier string              `json:"carrier,omitempty"`
	Items   []parcelLineRequest `json:"items"`
}

// CreateParcel handles POST /seller/orders/{id}/parcels.
func (h *Seller) CreateParcel(w http.ResponseWriter, r *http.Request) {
	user := middleware.UserFrom(r.Context())
	var req createParcelRequest
	if err := decode(r, &req); err != nil {
		writeErr(w, r, domain.E(domain.KindInvalid, "BAD_JSON", err.Error()))
		return
	}
	if len(req.Items) == 0 {
		// Named, rather than letting an empty parcel be created and then found
		// meaningless later. A parcel with nothing in it has no weight, no contents
		// and no reason to exist.
		writeErr(w, r, domain.E(domain.KindInvalid, "SHIPMENT_EMPTY",
			"a parcel needs at least one line in it"))
		return
	}
	lines := make([]service.ParcelLine, 0, len(req.Items))
	for _, it := range req.Items {
		lines = append(lines, service.ParcelLine{OrderItemID: it.OrderItemID, Quantity: it.Quantity})
	}
	shipment, err := h.svc.CreateParcel(r.Context(), user.ID, chi.URLParam(r, "id"),
		req.Carrier, lines)
	if err != nil {
		writeErr(w, r, err)
		return
	}
	writeJSON(w, http.StatusCreated, map[string]any{"parcel": shipment})
}

// OrderParcels handles GET /seller/orders/{id}/parcels.
//
// Every parcel, including cancelled and voided. The sequence numbers are the audit
// trail, and hiding parcel 2 because it was cancelled leaves a parcel 3 with no
// explanation of where parcel 2 went.
func (h *Seller) OrderParcels(w http.ResponseWriter, r *http.Request) {
	parcels, err := h.svc.OrderParcels(r.Context(), chi.URLParam(r, "id"))
	if err != nil {
		writeErr(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"parcels": parcels})
}

// DispatchParcel handles POST /seller/parcels/{id}/dispatch.
func (h *Seller) DispatchParcel(w http.ResponseWriter, r *http.Request) {
	user := middleware.UserFrom(r.Context())
	var req struct {
		TrackingNumber string `json:"tracking_number,omitempty"`
	}
	if err := decode(r, &req); err != nil {
		writeErr(w, r, domain.E(domain.KindInvalid, "BAD_JSON", err.Error()))
		return
	}
	shipment, err := h.svc.DispatchParcel(r.Context(), user.ID, chi.URLParam(r, "id"),
		req.TrackingNumber)
	if err != nil {
		writeErr(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"parcel": shipment})
}

// BuyParcelLabel handles POST /seller/parcels/{id}/label.
//
// Separate from dispatch on purpose: buying a label SPENDS MONEY and is often
// irreversible at the carrier, while handing the box over is free and happens later.
// Collapsing them means a seller who buys a label and then discovers he cannot get a
// colleague to the depot has spent money he cannot get back.
func (h *Seller) BuyParcelLabel(w http.ResponseWriter, r *http.Request) {
	user := middleware.UserFrom(r.Context())
	var req struct {
		Format string `json:"format,omitempty"`
	}
	if err := decode(r, &req); err != nil {
		writeErr(w, r, domain.E(domain.KindInvalid, "BAD_JSON", err.Error()))
		return
	}
	if req.Format == "" {
		req.Format = "pdf"
	}
	shipment, err := h.svc.BuyLabel(r.Context(), user.ID, chi.URLParam(r, "id"), req.Format)
	if err != nil {
		writeErr(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"parcel": shipment})
}

// addressRequest is a JSONB address as a client sends it.
//
// Typed as a map rather than a struct because the platform has never pinned its keys
// -- `orders.shipping_address` is written by checkout and read by the carrier
// shaper, which tries several spellings. Declaring a struct here would make the
// client believe the shape is enforced when it is not.
type addressRequest map[string]any

// SetReturnAddress handles PUT /seller/return-address.
func (h *Seller) SetReturnAddress(w http.ResponseWriter, r *http.Request) {
	user := middleware.UserFrom(r.Context())
	var req addressRequest
	if err := decode(r, &req); err != nil {
		writeErr(w, r, domain.E(domain.KindInvalid, "BAD_JSON", err.Error()))
		return
	}
	if len(req) == 0 {
		// Empty is a legitimate state -- "I will decide later" -- and it is what
		// makes a return label REFUSED rather than mis-addressed. An empty body is not
		// the same as a missing one, so it is accepted explicitly.
		req = addressRequest{}
	}
	if err := h.svc.SetReturnAddress(r.Context(), user.ID, map[string]any(req)); err != nil {
		writeErr(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]bool{"saved": true})
}

// ReturnAddress handles GET /seller/return-address.
func (h *Seller) ReturnAddress(w http.ResponseWriter, r *http.Request) {
	user := middleware.UserFrom(r.Context())
	addr, err := h.svc.ReturnAddress(r.Context(), user.ID)
	if err != nil {
		writeErr(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"return_address": addr})
}

// BuyReturnLabel handles POST /seller/returns/{id}/return-label.
//
// The seller issues it, because the seller pays for the carriage and states where the
// goods go back to. The BUYER creates the parcel -- see the comment on
// `SellerService.BuyReturnLabel` for why that ownership split is deliberate.
func (h *Seller) BuyReturnLabel(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Format  string `json:"format,omitempty"`
		Carrier string `json:"carrier,omitempty"`
	}
	if err := decode(r, &req); err != nil {
		writeErr(w, r, domain.E(domain.KindInvalid, "BAD_JSON", err.Error()))
		return
	}
	if req.Format == "" {
		req.Format = "pdf"
	}
	parcel, labelURL, err := h.svc.BuyReturnLabel(r.Context(), chi.URLParam(r, "id"), req.Format)
	if err != nil {
		writeErr(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"parcel": parcel, "label_url": labelURL})
}

// ReturnParcels handles GET /seller/returns/{id}/parcel.
func (h *Seller) ReturnParcels(w http.ResponseWriter, r *http.Request) {
	parcels, err := h.svc.ReturnParcelFor(r.Context(), chi.URLParam(r, "id"))
	if err != nil {
		writeErr(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"parcel": parcels})
}

// NoteReturnArrived handles POST /seller/returns/{id}/arrived.
//
// Records that the return parcel has been delivered, which unblocks the refund a human
// still has to perform. It moves `return_requests.status` and NOTHING else: no escrow
// release, no journal, no wallet debit. The refund is `RefundReturn`, an explicit
// seller action.
//
// The alternative -- releasing escrow from here -- would pay a seller on the say-so of
// a carrier webhook.
func (h *Seller) NoteReturnArrived(w http.ResponseWriter, r *http.Request) {
	returnID := chi.URLParam(r, "id")
	arrived, err := h.svc.NoteReturnArrived(r.Context(), returnID)
	if err != nil {
		writeErr(w, r, err)
		return
	}
	// `arrived: false` is a real, common answer: a seller clicking "it arrived" when
	// the parcel has not been delivered yet, or clicking twice. It is reported as
	// 200 with a false, not as a 409, because the seller asking is not an error -- but
	// it is NOT silent either, because a silent false reads as "done".
	writeJSON(w, http.StatusOK, map[string]any{
		"arrived":       arrived,
		"next_step":     "refund the buyer once the return is marked returned",
		"still_blocked": "the refund is not automatic; nothing here moves money",
	})
}

// The parcel is already a `service.ParcelView`, so it is returned directly.
//
// There was a `parcelJSON(*repository.Shipment)` here first, which meant the handler
// imported `internal/repository` -- the exact violation check-import-boundaries
// rejects, and the same one it flagged in seo.go and admin_ops.go. The rule is right:
// a handler that knows the storage shape cannot be changed without changing storage.
// The service owns the wire shape, as it already does for PayoutBatchView.

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

// SetStorePayoutLag handles PUT /admin/stores/{id}/payout-lag.
//
// `lag_days` is a POINTER in the request so that `null` and a missing field mean
// the same thing: clear the override. A plain int cannot express that -- its zero
// value is indistinguishable from "the operator sent 0", and 0 is not a lag, so
// sending it would silently do the wrong thing instead of being rejected.
//
// Admin-only, and inside the admin group, because a payout term is a risk
// parameter rather than a commercial setting -- see SellerService.AdminSetPayoutLag.
func (h *Admin) SetStorePayoutLag(w http.ResponseWriter, r *http.Request) {
	var req struct {
		LagDays *int `json:"lag_days"`
	}
	if err := decode(r, &req); err != nil {
		writeErr(w, r, domain.E(domain.KindInvalid, "BAD_JSON", err.Error()))
		return
	}
	if err := h.svc.AdminSetPayoutLag(r.Context(), chi.URLParam(r, "id"), req.LagDays); err != nil {
		writeErr(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]bool{"updated": true})
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
