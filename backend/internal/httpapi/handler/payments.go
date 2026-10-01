package handler

import (
	"fmt"
	"io"
	"net/http"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/vincommerce/backend/internal/domain"
	"github.com/vincommerce/backend/internal/httpapi/middleware"
	"github.com/vincommerce/backend/internal/service"
)

// Payments exposes the escrow payment API.
type Payments struct {
	svc *service.PaymentService
}

// NewPayments creates a Payments handler.
func NewPayments(svc *service.PaymentService) *Payments {
	return &Payments{svc: svc}
}

type initiateRequest struct {
	Method         string `json:"method"`
	IdempotencyKey string `json:"idempotency_key,omitempty"`
}

// Initiate handles POST /payments/orders/{orderId}/intent.
func (h *Payments) Initiate(w http.ResponseWriter, r *http.Request) {
	user := middleware.UserFrom(r.Context())
	var req initiateRequest
	if err := decode(r, &req); err != nil {
		writeErr(w, r, domain.E(domain.KindInvalid, "BAD_JSON", err.Error()))
		return
	}
	intent, gw, err := h.svc.InitiatePayment(r.Context(), service.CreateIntentInput{
		OrderID:        chi.URLParam(r, "orderId"),
		BuyerID:        user.ID,
		Method:         req.Method,
		IdempotencyKey: req.IdempotencyKey,
	})
	if err != nil {
		writeErr(w, r, err)
		return
	}
	writeJSON(w, http.StatusCreated, map[string]any{
		"intent": intent, "payment_url": gw.RedirectURL, "gateway_ref": gw.Reference,
		"snap_token": gw.Token, "gateway": intent.Gateway,
	})
}

// Webhook handles POST /payments/webhook/{gateway}.
func (h *Payments) Webhook(w http.ResponseWriter, r *http.Request) {
	payload, err := io.ReadAll(io.LimitReader(r.Body, 1<<20))
	if err != nil {
		writeErr(w, r, domain.E(domain.KindInvalid, "BAD_WEBHOOK", "unable to read payload"))
		return
	}
	signature := r.Header.Get("X-Webhook-Signature")
	if err := h.svc.HandleWebhook(r.Context(), chi.URLParam(r, "gateway"), payload, signature); err != nil {
		writeErr(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]bool{"received": true})
}

// Release handles POST /payments/orders/{orderId}/release (admin/delivery confirmation).
func (h *Payments) Release(w http.ResponseWriter, r *http.Request) {
	if err := h.svc.ReleaseEscrow(r.Context(), chi.URLParam(r, "orderId")); err != nil {
		writeErr(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]bool{"released": true})
}

type refundRequest struct {
	Reason string `json:"reason"`
}

// Refund handles POST /payments/orders/{orderId}/refund.
func (h *Payments) Refund(w http.ResponseWriter, r *http.Request) {
	var req refundRequest
	if err := decode(r, &req); err != nil {
		writeErr(w, r, domain.E(domain.KindInvalid, "BAD_JSON", err.Error()))
		return
	}
	if err := h.svc.RefundOrder(r.Context(), chi.URLParam(r, "orderId"), req.Reason, true, 0); err != nil {
		writeErr(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]bool{"refunded": true})
}

// Intent handles GET /payments/orders/{orderId}/intent.
// Ownership-gated: payment intents carry Snap tokens and buyer metadata, so
// only the buyer (or staff) may read them.
func (h *Payments) Intent(w http.ResponseWriter, r *http.Request) {
	user := middleware.UserFrom(r.Context())
	intent, err := h.svc.IntentForOrder(r.Context(), chi.URLParam(r, "orderId"))
	if err != nil {
		writeErr(w, r, err)
		return
	}
	if intent.BuyerID != user.ID && !user.HasRole(domain.RoleAdmin) && !user.HasRole(domain.RoleSupport) {
		writeErr(w, r, domain.E(domain.KindForbidden, "NOT_OWNED", "intent does not belong to user"))
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"intent": intent})
}

// SandboxApprove simulates the buyer completing payment at the fake gateway.
// Development only — emits an authenticated webhook into the real pipeline.
func (h *Payments) SandboxApprove(w http.ResponseWriter, r *http.Request) {
	user := middleware.UserFrom(r.Context())
	intent, err := h.svc.IntentForOrder(r.Context(), chi.URLParam(r, "orderId"))
	if err != nil {
		writeErr(w, r, err)
		return
	}
	if intent.BuyerID != user.ID && !user.HasRole(domain.RoleAdmin) {
		writeErr(w, r, domain.E(domain.KindForbidden, "NOT_OWNED", "intent does not belong to user"))
		return
	}
	if intent.Status != domain.IntentInitiated {
		writeErr(w, r, domain.E(domain.KindConflict, "INTENT_STATE", "intent is not awaiting payment"))
		return
	}
	payload := []byte(fmt.Sprintf(
		`{"type":"payment.paid","reference":%q,"amount":%v,"currency":%q}`,
		intent.GatewayRef, intent.Amount, intent.Currency))
	signature := h.svc.SignWebhook(payload)
	if err := h.svc.HandleWebhook(r.Context(), "sandbox", payload, signature); err != nil {
		writeErr(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"paid": true, "order_id": intent.OrderID})
}

// SandboxFail simulates a failed payment at the fake gateway.
func (h *Payments) SandboxFail(w http.ResponseWriter, r *http.Request) {
	user := middleware.UserFrom(r.Context())
	intent, err := h.svc.IntentForOrder(r.Context(), chi.URLParam(r, "orderId"))
	if err != nil {
		writeErr(w, r, err)
		return
	}
	if intent.BuyerID != user.ID && !user.HasRole(domain.RoleAdmin) {
		writeErr(w, r, domain.E(domain.KindForbidden, "NOT_OWNED", "intent does not belong to user"))
		return
	}
	payload := []byte(fmt.Sprintf(
		`{"type":"payment.failed","reference":%q,"amount":%v,"currency":%q}`,
		intent.GatewayRef, intent.Amount, intent.Currency))
	signature := h.svc.SignWebhook(payload)
	if err := h.svc.HandleWebhook(r.Context(), "sandbox", payload, signature); err != nil {
		writeErr(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"failed": true})
}

// Wallet exposes seller wallet endpoints.
type Wallet struct {
	svc *service.PaymentService
}

// NewWallet creates a Wallet handler.
func NewWallet(svc *service.PaymentService) *Wallet {
	return &Wallet{svc: svc}
}

// Get handles GET /wallet.
func (h *Wallet) Get(w http.ResponseWriter, r *http.Request) {
	user := middleware.UserFrom(r.Context())
	wallet, err := h.svc.Wallet(r.Context(), user.ID)
	if err != nil {
		writeErr(w, r, err)
		return
	}
	txs, err := h.svc.WalletTransactions(r.Context(), user.ID, 20)
	if err != nil {
		writeErr(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"wallet": wallet, "transactions": txs})
}

// Payouts handles GET /wallet/payouts.
func (h *Wallet) Payouts(w http.ResponseWriter, r *http.Request) {
	user := middleware.UserFrom(r.Context())
	payouts, err := h.svc.Payouts(r.Context(), user.ID)
	if err != nil {
		writeErr(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"payouts": payouts})
}

type payoutRequest struct {
	Amount      float64 `json:"amount"`
	BankName    string  `json:"bank_name"`
	BankAccount string  `json:"bank_account"`
}

// RequestPayout handles POST /wallet/payouts.
func (h *Wallet) RequestPayout(w http.ResponseWriter, r *http.Request) {
	user := middleware.UserFrom(r.Context())
	var req payoutRequest
	if err := decode(r, &req); err != nil {
		writeErr(w, r, domain.E(domain.KindInvalid, "BAD_JSON", err.Error()))
		return
	}
	payout, err := h.svc.RequestPayout(r.Context(), user.ID, req.Amount, req.BankName, req.BankAccount)
	if err != nil {
		writeErr(w, r, err)
		return
	}
	writeJSON(w, http.StatusCreated, map[string]any{"payout": payout})
}

// AdminPayouts handles GET /admin/payouts?status=pending (ops queue).
func (h *Wallet) AdminPayouts(w http.ResponseWriter, r *http.Request) {
	items, err := h.svc.AdminPayouts(r.Context(), r.URL.Query().Get("status"))
	if err != nil {
		writeErr(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"payouts": items})
}

type processPayoutRequest struct {
	Action    string `json:"action"` // sent | failed
	Reference string `json:"reference,omitempty"`
}

// ProcessPayout handles POST /admin/payouts/{id}/process.
func (h *Wallet) ProcessPayout(w http.ResponseWriter, r *http.Request) {
	var req processPayoutRequest
	if err := decode(r, &req); err != nil {
		writeErr(w, r, domain.E(domain.KindInvalid, "BAD_JSON", err.Error()))
		return
	}
	if err := h.svc.ProcessPayout(r.Context(), chi.URLParam(r, "id"), req.Action, req.Reference); err != nil {
		writeErr(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]bool{"processed": true})
}

// PayoutBatches handles GET /admin/payout-batches?status=draft.
//
// Rejects an unknown status rather than returning an empty list, for the reason
// `Refunds` does: an empty list and a misspelled filter look identical, and an
// operator's conclusion from "no batches" would be wrong.
func (h *Wallet) PayoutBatches(w http.ResponseWriter, r *http.Request) {
	status := r.URL.Query().Get("status")
	if status != "" && !validPayoutBatchStatus(status) {
		writeErr(w, r, domain.E(domain.KindInvalid, "BAD_STATUS",
			"status must be one of draft, approved, submitted, paid, failed, cancelled"))
		return
	}
	batches, err := h.svc.PayoutBatches(r.Context(), status, intQuery(r.URL.Query().Get("limit"), 100))
	if err != nil {
		writeErr(w, r, err)
		return
	}
	out := make([]service.PayoutBatchView, 0, len(batches))
	out = append(out, batches...)
	writeJSON(w, http.StatusOK, map[string]any{"batches": out})
}

// BuildPayoutBatch handles POST /admin/payout-batches/build.
//
// Exposed as an endpoint as well as a scheduled job, because an operator building a
// batch on demand before a bank cut-off is the normal case and waiting for the daily
// run is not something anyone should have to do.
//
// `lag_days` defaults to the seller's platform lag rather than to 7: an operator
// pressing this button means "batch what is due", and the due date is whatever the
// configured lag says.
func (h *Wallet) BuildPayoutBatch(w http.ResponseWriter, r *http.Request) {
	var req struct {
		LagDays int `json:"lag_days,omitempty"`
		Limit   int `json:"limit,omitempty"`
	}
	if err := decode(r, &req); err != nil {
		writeErr(w, r, domain.E(domain.KindInvalid, "BAD_JSON", err.Error()))
		return
	}
	if req.LagDays == 0 {
		req.LagDays = h.svc.PayoutLagDays()
	}
	batch, added, created, err := h.svc.BuildPayoutBatch(r.Context(), req.LagDays,
		time.Time{}, req.Limit)
	if err != nil {
		writeErr(w, r, err)
		return
	}
	// `created` is reported rather than hidden: "I built a batch" and "one was
	// already there for this day" are different things for an operator to know.
	writeJSON(w, http.StatusOK, map[string]any{
		"batch":   batch,
		"added":   added,
		"created": created,
	})
}

// ApprovePayoutBatch handles POST /admin/payout-batches/{id}/approve.
//
// The approver is the ADMIN who clicked, not a body field. An approval whose
// subject can be chosen by the client is not an audit trail -- and the admin guard
// has already established who is calling.
func (h *Wallet) ApprovePayoutBatch(w http.ResponseWriter, r *http.Request) {
	user := middleware.UserFrom(r.Context())
	items, err := h.svc.ApprovePayoutBatch(r.Context(), chi.URLParam(r, "id"), user.ID)
	if err != nil {
		writeErr(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"approved": true, "items": items})
}

// PayoutRemittance handles GET /admin/payout-batches/{id}/remittance.csv.
//
// Served as a download rather than as JSON, because its consumer is a person
// uploading it to a bank portal -- and it is refused for an unapproved batch, so an
// unattended job cannot produce a payment instruction.
func (h *Wallet) PayoutRemittance(w http.ResponseWriter, r *http.Request) {
	csvBytes, err := h.svc.PayoutRemittanceCSV(r.Context(), chi.URLParam(r, "id"))
	if err != nil {
		writeErr(w, r, err)
		return
	}
	w.Header().Set("Content-Type", "text/csv; charset=utf-8")
	w.Header().Set("Content-Disposition",
		fmt.Sprintf("attachment; filename=%q", "payout-remittance-"+chi.URLParam(r, "id")+".csv"))
	w.WriteHeader(http.StatusOK)
	_, _ = w.Write(csvBytes)
}

func validPayoutBatchStatus(s string) bool {
	switch s {
	case "draft", "approved", "submitted", "paid", "failed", "cancelled":
		return true
	}
	return false
}
