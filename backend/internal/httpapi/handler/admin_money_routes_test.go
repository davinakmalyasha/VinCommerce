package handler

import (
	"os"
	"strings"
	"testing"
)

// The admin money routes must exist and must sit behind the admin guard.
//
// A handler that is written but never routed is the same failure as a refund
// state that is stored but never surfaced: the capability is present, everything
// looks fine, and the operator has no way to reach it. That is exactly what
// happened to the `manual` refund state -- it was designed to prompt a human, and
// there was no endpoint that listed it.
func TestAdminMoneyRoutesAreRegistered(t *testing.T) {
	src, err := os.ReadFile("../router.go")
	if err != nil {
		t.Fatalf("read router.go: %v", err)
	}
	body := string(src)

	for _, want := range []struct {
		route string
		why   string
	}{
		{`r.Get("/refunds", adminOps.Refunds)`,
			"without it a manual refund cannot be listed, and a state nobody can " +
				"list is a state nobody acts on"},
		{`r.Get("/refunds/summary", adminOps.RefundSummary)`,
			"the dashboard needs the count without fetching and counting the list"},
		{`r.Get("/ledger/trial-balance", adminOps.TrialBalance)`,
			"an operator asking how much we hold and for whom needs an answer"},
		{`r.Get("/ledger/reconciliation", adminOps.LedgerReconciliation)`,
			"the daily job's finding exists only as a log line otherwise"},
		{`r.Get("/ledger/held", adminOps.HeldBalances)`,
			"a hold is money a SELLER has earned and may not spend, which is a " +
				"different number from escrow and does not move when one is taken; " +
				"without this a hold is enforced and invisible at the same time"},
	} {
		if !strings.Contains(body, want.route) {
			t.Errorf("router.go does not register %s: %s", want.route, want.why)
		}
	}
}

// The money routes must be inside the /admin group, which carries
// RequireRoles(domain.RoleAdmin). A trial balance exposed outside that guard is
// a list of how much money the platform holds and who it is owed to.
func TestMoneyRoutesAreInsideTheAdminGuard(t *testing.T) {
	src, err := os.ReadFile("../router.go")
	if err != nil {
		t.Fatalf("read router.go: %v", err)
	}
	body := string(src)

	start := strings.Index(body, `r.Route("/admin"`)
	if start < 0 {
		t.Fatal("no /admin route group found")
	}
	// The group is closed by the first `})` at the same indentation after its
	// guard line. Crude, but the guard line and the routes are what matter.
	end := strings.Index(body[start:], "r.Get(\"/users\"")
	if end < 0 {
		t.Fatal("could not find the end of the /admin group")
	}
	group := body[start : start+end]

	if !strings.Contains(group, "mw.RequireRoles(domain.RoleAdmin)") {
		t.Fatal("the /admin group does not require the admin role")
	}
	for _, route := range []string{
		`r.Get("/refunds"`, `r.Get("/ledger/trial-balance"`, `r.Get("/ledger/reconciliation"`,
	} {
		if !strings.Contains(group, route) {
			t.Errorf("%s is not inside the guarded /admin group", route)
		}
	}
}

// The payment service must be attached, or every money endpoint answers 409 and
// the operator sees a permanently broken dashboard.
func TestAdminOpsIsWiredToThePaymentService(t *testing.T) {
	src, err := os.ReadFile("../router.go")
	if err != nil {
		t.Fatalf("read router.go: %v", err)
	}
	if !strings.Contains(string(src), "adminOps.SetPayment(paymentSvc)") {
		t.Error("router.go never calls adminOps.SetPayment; the money endpoints " +
			"would answer 409 for every request")
	}
}
