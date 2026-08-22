package handler

import (
	"fmt"
	"io"
	"net/http"

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
	if err := h.svc.RefundOrder(r.Context(), chi.URLParam(r, "orderId"), req.Reason, true); err != nil {
		writeErr(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]bool{"refunded": true})
}

// Intent handles GET /payments/orders/{orderId}/intent.
func (h *Payments) Intent(w http.ResponseWriter, r *http.Request) {
	intent, err := h.svc.IntentForOrder(r.Context(), chi.URLParam(r, "orderId"))
	if err != nil {
		writeErr(w, r, err)
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
