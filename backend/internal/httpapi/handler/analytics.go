package handler

import (
	"encoding/csv"
	"fmt"
	"net/http"
	"strconv"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/vincommerce/backend/internal/httpapi/middleware"
	"github.com/vincommerce/backend/internal/service"
)

// Analytics exposes reports (admin + seller scoped).
type Analytics struct {
	svc *service.AnalyticsService
}

// NewAnalytics creates an Analytics handler.
func NewAnalytics(svc *service.AnalyticsService) *Analytics {
	return &Analytics{svc: svc}
}

// View handles POST /products/{id}/view — analytics beacon (optional auth).
func (h *Analytics) View(w http.ResponseWriter, r *http.Request) {
	user := middleware.UserFrom(r.Context())
	var userID *string
	if user != nil {
		uid := user.ID
		userID = &uid
	}
	if err := h.svc.RecordView(r.Context(), chi.URLParam(r, "id"), userID); err != nil {
		writeErr(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]bool{"ok": true})
}

func rangeFromQuery(r *http.Request) (time.Time, time.Time) {
	q := r.URL.Query()
	layout := "2006-01-02"
	from, _ := time.Parse(layout, q.Get("from"))
	to, _ := time.Parse(layout, q.Get("to"))
	if !to.IsZero() {
		to = to.Add(24 * time.Hour)
	}
	return from, to
}

// Platform handles GET /admin/analytics.
func (h *Analytics) Platform(w http.ResponseWriter, r *http.Request) {
	from, to := rangeFromQuery(r)
	report, err := h.svc.PlatformReport(r.Context(), from, to)
	if err != nil {
		writeErr(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, report)
}

// Seller handles GET /seller/analytics.
func (h *Analytics) Seller(w http.ResponseWriter, r *http.Request) {
	user := middleware.UserFrom(r.Context())
	from, to := rangeFromQuery(r)
	report, err := h.svc.SellerReport(r.Context(), user.ID, from, to)
	if err != nil {
		writeErr(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, report)
}

// PlatformCSV exports the sales series as CSV (admin).
func (h *Analytics) PlatformCSV(w http.ResponseWriter, r *http.Request) {
	from, to := rangeFromQuery(r)
	report, err := h.svc.PlatformReport(r.Context(), from, to)
	if err != nil {
		writeErr(w, r, err)
		return
	}
	h.writeCSV(w, report)
}

// SellerCSV exports the seller sales series as CSV.
func (h *Analytics) SellerCSV(w http.ResponseWriter, r *http.Request) {
	user := middleware.UserFrom(r.Context())
	from, to := rangeFromQuery(r)
	report, err := h.svc.SellerReport(r.Context(), user.ID, from, to)
	if err != nil {
		writeErr(w, r, err)
		return
	}
	h.writeCSV(w, report)
}

// SellerOrdersCSV exports the seller's orders as CSV.
func (h *Analytics) SellerOrdersCSV(w http.ResponseWriter, r *http.Request) {
	user := middleware.UserFrom(r.Context())
	orders, err := h.svc.SellerOrders(r.Context(), user.ID, 2000)
	if err != nil {
		writeErr(w, r, err)
		return
	}
	w.Header().Set("Content-Type", "text/csv; charset=utf-8")
	w.Header().Set("Content-Disposition", fmt.Sprintf("attachment; filename=vincommerce-orders-%s.csv", time.Now().Format("20060102")))

	cw := csv.NewWriter(w)
	defer cw.Flush()

	_ = cw.Write([]string{"order_number", "status", "payment_status", "buyer", "total", "external_payment_ref", "placed_at"})
	for _, o := range orders {
		_ = cw.Write([]string{
			o.OrderNumber, o.Status, o.PaymentStatus, o.SellerName, strconv.FormatFloat(o.TotalAmount, 'f', 2, 64),
			o.ExternalPaymentRef, o.PlacedAt.Format("2006-01-02 15:04"),
		})
	}
}

func (h *Analytics) writeCSV(w http.ResponseWriter, report *service.Report) {
	w.Header().Set("Content-Type", "text/csv; charset=utf-8")
	w.Header().Set("Content-Disposition", fmt.Sprintf("attachment; filename=vincommerce-sales-%s.csv", time.Now().Format("20060102")))

	cw := csv.NewWriter(w)
	defer cw.Flush()

	_ = cw.Write([]string{"day", "gmv", "orders", "items"})
	for _, p := range report.SalesSeries {
		_ = cw.Write([]string{
			p.Day.Format("2006-01-02"),
			strconv.FormatFloat(p.GMV, 'f', 2, 64),
			strconv.Itoa(p.OrderCount),
			strconv.Itoa(p.ItemCount),
		})
	}
	_ = cw.Write([]string{"total_gmv", strconv.FormatFloat(report.Summary.GMV, 'f', 2, 64), "orders", strconv.Itoa(report.Summary.OrderCount)})
}
