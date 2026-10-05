package service

import (
	"strings"
	"testing"
)

// A CSV writer that forgets to escape is a silent, high-value defect, so this asserts
// on the WRITER rather than only on the escaper's unit behaviour.
func TestTheRemittanceWriterEscapesEverySellerControlledColumn(t *testing.T) {
	body := funcBody(t, readSource(t, "payment_service.go"),
		"func (s *PaymentService) PayoutRemittanceCSV(")

	// Find the row construction.
	rowAt := strings.Index(body, "CSVRowMixed([]string{")
	if rowAt < 0 {
		t.Fatal("the remittance row is not built with CSVRowMixed.\n" +
			"This file goes to a finance operator or a bank. Seller-controlled bank_name " +
			"and bank_account must be escaped, and the raw-form Write([]string{...}) form " +
			"they replaced is what left them unescaped for so long")
	}

	row := body[rowAt:]
	if end := strings.Index(row, "}, "); end > 0 {
		row = row[:end]
	}

	// bank_name and bank_account are the seller-controlled ones. Indexes follow the
	// header: batch_ref, payout_id, seller_id, bank_name, bank_account, amount,
	// requested_at.
	for _, field := range []string{"p.BankName", "p.BankAccount"} {
		if !strings.Contains(row, field) {
			t.Errorf("the row no longer exports %s.\n%s\n"+
				"If it was removed the column is gone, which is a different decision and "+
				"needs its own change", field, row)
		}
	}

	// The escape index list must cover the text columns.
	idxAt := strings.Index(body[rowAt:], "}, ")
	if idxAt < 0 {
		t.Fatalf("could not find the escape index list:\n%s", row)
	}
	idxList := body[rowAt+idxAt:]
	if end := strings.Index(idxList, ")\n"); end > 0 {
		idxList = idxList[:end]
	}
	for _, want := range []string{"0", "1", "2", "3", "4"} {
		if !strings.Contains(idxList, want) {
			t.Errorf("escape index %s missing from %q.\n"+
				"Indexes 0-4 are the text columns; 5 and 6 are the formatted amount and "+
				"the RFC3339 timestamp, which cannot begin with a formula character",
				want, strings.TrimSpace(idxList))
		}
	}

	// The amount and timestamp must NOT be escaped: escaping a formatted number is a
	// category error that would hide a formatting bug behind a stray apostrophe.
	if strings.Contains(idxList, "5") || strings.Contains(idxList, "6") {
		t.Errorf("a numeric or timestamp column is being run through the text escaper: %q",
			strings.TrimSpace(idxList))
	}
}

// The escaper must be the SHARED one. It used to exist only in the handler package,
// which is precisely why the remittance writer -- the one with the most dangerous
// reader -- had none.
func TestEveryCsvWriterInTheServiceLayerUsesTheSharedEscaper(t *testing.T) {
	for _, f := range []string{"payment_service.go"} {
		src := readSource(t, f)
		if strings.Contains(src, "csv.NewWriter") &&
			!strings.Contains(src, "domain.CSVRow") &&
			!strings.Contains(src, "domain.CSVCell") {
			t.Errorf("%s writes CSV but uses neither domain.CSVCell nor domain.CSVRow.\n"+
				"The escaper must be shared, not reimplemented per writer -- a copy is one "+
				"that eventually diverges and silently stops protecting its export", f)
		}
	}
}

// And the handler must delegate rather than keep its own copy, which is the same
// divergence risk from the other direction.
func TestTheHandlerEscaperDelegatesToTheSharedOne(t *testing.T) {
	src := readSource(t, "../httpapi/handler/analytics.go")
	i := strings.Index(src, "func escapeCSVCell(")
	if i < 0 {
		t.Fatal("analytics.go no longer has escapeCSVCell")
	}
	body := src[i:]
	if end := strings.Index(body[1:], "\n}"); end > 0 {
		body = body[:end+1]
	}
	if !strings.Contains(body, "domain.CSVCell(") {
		t.Errorf("escapeCSVCell does not delegate to domain.CSVCell:\n%s\n"+
			"A private copy in the handler is what let the remittance writer go unprotected",
			body)
	}
}
