package handler

import (
	"net/http"

	"github.com/vincommerce/backend/internal/domain"
	"github.com/vincommerce/backend/internal/service"
)

// The operator-facing money surfaces: the refund queue, the trial balance, and
// the reconciliation report.
//
// These exist because the states they expose were otherwise unobservable. A
// refund in `manual` means the platform owes a buyer money the gateway will not
// move and a human has to -- and a state nobody can see is a state nobody acts
// on. The reconciliation report is the same argument: a daily job whose only
// output is a log line is a finding nobody triages.

// requirePayment returns the payment service or a clear error.
//
// A 409 with an explanation rather than a nil dereference. The two look very
// different to whoever is looking: a crash reads as a broken feature, an
// explained 409 reads as a deployment that is missing something.
func (h *AdminOps) requirePayment(w http.ResponseWriter, r *http.Request) bool {
	if h.pay == nil {
		writeErr(w, r, domain.E(domain.KindConflict, "MONEY_SURFACES_UNAVAILABLE",
			"the money surfaces are not wired on this server"))
		return false
	}
	return true
}

// Refunds handles GET /admin/refunds.
//
// `?status=` filters to one state; absent means all of them, which is what an
// operator wants by default because the queue is a triage list and filtering to
// one state hides the refunds that fell out of every pipeline.
func (h *AdminOps) Refunds(w http.ResponseWriter, r *http.Request) {
	if !h.requirePayment(w, r) {
		return
	}
	status := r.URL.Query().Get("status")
	if status != "" && !validRefundStatus(status) {
		// Reject an unknown state rather than returning an empty list. An empty
		// list and a misspelled filter look identical, and the operator's
		// conclusion from "no refunds" would be wrong.
		writeErr(w, r, domain.E(domain.KindInvalid, "BAD_STATUS",
			"status must be one of pending, submitted, succeeded, failed, manual"))
		return
	}
	refunds, err := h.pay.AdminRefunds(r.Context(), status, intQuery(r.URL.Query().Get("limit"), 100))
	if err != nil {
		writeErr(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"refunds": refunds,
		"summary": mustSummary(h.pay, r),
	})
}

// RefundSummary handles GET /admin/refunds/summary.
//
// Separate from the list because it is what a dashboard polls and the list is
// what a human opens. A dashboard that has to fetch and count the list itself
// will eventually cache the count, and a cached "0 need action" is the most
// dangerous number in the system.
func (h *AdminOps) RefundSummary(w http.ResponseWriter, r *http.Request) {
	if !h.requirePayment(w, r) {
		return
	}
	summary, err := h.pay.RefundQueueSummary(r.Context())
	if err != nil {
		writeErr(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, summary)
}

// mustSummary computes the summary, degrading to nil rather than failing the list.
//
// The list is the answer the operator asked for; the summary is a convenience
// alongside it. Losing the convenience must not turn a readable queue into a
// 500.
func mustSummary(pay *service.PaymentService, r *http.Request) any {
	s, err := pay.RefundQueueSummary(r.Context())
	if err != nil {
		return nil
	}
	return s
}

// TrialBalance handles GET /admin/ledger/trial-balance.
//
// Every account and its balance, which is the answer to "how much money are we
// holding and for whom". Liabilities carry a credit normal side, so a positive
// number on `seller_available` means "we owe this much", which is the direction an
// operator reads it in.
func (h *AdminOps) TrialBalance(w http.ResponseWriter, r *http.Request) {
	if !h.requirePayment(w, r) {
		return
	}
	rows, err := h.pay.TrialBalance(r.Context())
	if err != nil {
		writeErr(w, r, err)
		return
	}
	escrow, err := h.pay.EscrowOutstanding(r.Context())
	if err != nil {
		writeErr(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"accounts": rows,
		// The headline, separated from the rows so a dashboard does not have to
		// know which account is the one being asked about.
		"escrow_outstanding": escrow,
	})
}

// HeldBalances handles GET /admin/ledger/held.
//
// A hold is money a SELLER has earned and may not yet spend; escrow is money held
// for ORDERS. They move independently, so an operator watching only
// `escrow_outstanding` sees a number that does not change when a return hold is
// taken -- which is what a hold looks like when it is working.
//
// `?live=false` includes released holds, for the same question escrow cannot
// answer either: what did we hold last month, and did it come back?
func (h *AdminOps) HeldBalances(w http.ResponseWriter, r *http.Request) {
	if !h.requirePayment(w, r) {
		return
	}
	liveOnly := r.URL.Query().Get("live") != "false"
	total, rows, err := h.pay.HeldOutstanding(r.Context(), liveOnly,
		intQuery(r.URL.Query().Get("limit"), 200))
	if err != nil {
		writeErr(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"held_outstanding": total,
		"live_only":        liveOnly,
		"holds":            rows,
	})
}

// LedgerReconciliation handles GET /admin/ledger/reconciliation.
//
// The same report the daily job runs, on demand. The job's finding otherwise
// exists only as a log line, and a log line nobody is watching is not a check.
//
// `clean: false` with the offending accounts named is the response that matters.
// The counts alone tell an operator something is wrong; the account codes tell
// them where.
func (h *AdminOps) LedgerReconciliation(w http.ResponseWriter, r *http.Request) {
	if !h.requirePayment(w, r) {
		return
	}
	report, err := h.pay.LedgerReconciliation(r.Context())
	if err != nil {
		writeErr(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, report)
}

func validRefundStatus(s string) bool {
	switch s {
	case domain.RefundPending, domain.RefundSubmitted, domain.RefundSucceeded,
		domain.RefundFailed, domain.RefundManual:
		return true
	}
	return false
}
