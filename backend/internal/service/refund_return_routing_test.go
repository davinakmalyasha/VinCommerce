package service

import (
	"strings"
	"testing"

	"github.com/vincommerce/backend/internal/domain"
)

// The routing decision is the whole point of P0-1: a buyer who paid QRIS must
// not be refunded into a wallet they cannot withdraw from. These are the
// methods that cannot be reversed at a provider, and everything else must be.
func TestRefundRoutingChoosesGatewayOnlyForReversibleMethods(t *testing.T) {
	cases := []struct {
		name   string
		intent *domain.PaymentIntent
		want   bool
	}{
		{"midtrans e-wallet", &domain.PaymentIntent{Gateway: "midtrans", Method: domain.MethodEWallet}, true},
		{"midtrans qris", &domain.PaymentIntent{Gateway: "midtrans", Method: domain.MethodQRIS}, true},
		{"midtrans bank transfer", &domain.PaymentIntent{Gateway: "midtrans", Method: domain.MethodBankTransfer}, true},
		{"sandbox card", &domain.PaymentIntent{Gateway: "sandbox", Method: domain.MethodCreditCard}, true},
		{"wallet balance", &domain.PaymentIntent{Gateway: "internal", Method: domain.MethodWalletBalance}, false},
		{"wallet method alias", &domain.PaymentIntent{Gateway: "internal", Method: "wallet"}, false},
		{"cod", &domain.PaymentIntent{Gateway: "cod", Method: domain.MethodCOD}, false},
		{"cod via gateway field", &domain.PaymentIntent{Gateway: "midtrans", Method: domain.MethodCOD}, false},
		{"manual transfer", &domain.PaymentIntent{Gateway: "external", Method: domain.MethodManualTransfer}, false},
		{"empty gateway", &domain.PaymentIntent{Gateway: "", Method: domain.MethodBankTransfer}, false},
		{"nil", nil, false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := refundRoutedThroughGateway(tc.intent); got != tc.want {
				t.Fatalf("refundRoutedThroughGateway(%+v) = %v, want %v", tc.intent, got, tc.want)
			}
		})
	}
}

// Pinned at the source for the same reason the refund-row test is: the branch
// needs a live database and a provider to execute, so the wiring is asserted
// where it lives. The routing test above proves the decision; this proves the
// decision is used and that the gateway call is not inside the transaction.
func TestReturnRefundUsesTheGatewayOutsideATransaction(t *testing.T) {
	body := functionSource(t, "seller_service.go", "refundReturnThroughGateway")

	if !strings.Contains(body, `s.paymentSvc.RefundOrder(ctx, req.OrderID`) {
		t.Fatalf("the gateway return-refund path does not call RefundOrder:\n%s", body)
	}
	if !strings.Contains(body, `status = 'refunding'`) {
		t.Fatalf("the return is not claimed as `refunding` before the provider call:\n%s", body)
	}
	if !strings.Contains(body, "s.finalizeReturnRefund") {
		t.Fatalf("a successful provider refund never resolves the return claim:\n%s", body)
	}
	// The claim commit must happen BEFORE RefundOrder. Holding a row lock across
	// the provider call is the defect this two-step flow removes.
	commit := strings.Index(body, "tx.Commit(ctx)")
	call := strings.Index(body, "s.paymentSvc.RefundOrder(")
	if commit < 0 || call < 0 || commit > call {
		t.Fatalf("RefundOrder is not called after the claim commit (commit=%d call=%d):\n%s",
			commit, call, body)
	}

	refundReturn := functionSource(t, "seller_service.go", "RefundReturn")
	if !strings.Contains(refundReturn, "refundRoutedThroughGateway(intent)") {
		t.Fatalf("RefundReturn no longer routes by payment method:\n%s", refundReturn)
	}
	if !strings.Contains(refundReturn, "s.paymentSvc.RefundOrderInTx") {
		t.Fatalf("the non-reversible (wallet/COD/manual) path was removed:\n%s", refundReturn)
	}
}
