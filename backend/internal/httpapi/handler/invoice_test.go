package handler

import (
	"math"
	"net/http/httptest"
	"regexp"
	"strconv"
	"strings"
	"testing"

	"github.com/vincommerce/backend/internal/domain"
)

// The invoice must add up.
//
// This is the whole test. An invoice whose own printed lines do not sum to its
// own printed total is a document that fails an audit on sight, and it is exactly
// what this system was producing: the shipping-insurance fee is added to the
// order total and stored on `Order.InsuranceFee`, but no line was rendered for
// it, so subtotal − discount + shipping came to less than the total.
//
// The arithmetic was never the broken part -- `insuranceFee` rounds correctly and
// `quote.Total` includes it. The PRESENTATION was, and it went unnoticed because
// nothing checked the rendered output against the numbers it was supposed to
// reflect. A README entry claimed the issue was fixed, which is how a
// presentation bug outlived the fix for the arithmetic.

// labelledAmount pulls a labelled money value out of the rendered invoice.
//
// The total is matched on a whole <tr> rather than a substring so that the
// "Subtotal" and "TOTAL" lines cannot be confused, and the label text is
// anchored to the start of a cell so "Ongkir" cannot match a line that happens
// to contain the word.
var (
	subtotalRE   = regexp.MustCompile(`Subtotal</td><td style="text-align:right">Rp ([\d.,]+)</td>`)
	discountRE   = regexp.MustCompile(`Diskon</td><td style="text-align:right">(?:− |-)?Rp ([\d.,]+)</td>`)
	shippingRE   = regexp.MustCompile(`Ongkir \(([^)]*)\)</td><td style="text-align:right">Rp ([\d.,]+)</td>`)
	insuranceRE  = regexp.MustCompile(`Asuransi pengiriman</td><td style="text-align:right">Rp ([\d.,]+)</td>`)
	totalRowRE   = regexp.MustCompile(`(?m)^\s*<tr class="total-row"><td colspan="3"></td><td style="text-align:right">TOTAL</td><td style="text-align:right">Rp ([\d.,]+)</td>`)
	shippingName = "(?:[a-zA-Z0-9 ]+)"
)

func renderOrderInvoice(o *domain.Order) string {
	rec := httptest.NewRecorder()
	renderInvoice(rec, o, invoiceOptions{Number: "INV-202601-000001", PPNRate: 0})
	return rec.Body.String()
}

func parseMoney(s string) float64 {
	// f2 formats with no separators, but the regex tolerates them so a future
	// thousands separator does not silently break this test into always passing.
	s = strings.ReplaceAll(s, ",", "")
	s = strings.ReplaceAll(s, ".", "")
	v, _ := strconv.ParseFloat(s, 64)
	return v
}

// invoiceAmounts extracts every labelled amount the invoice prints.
func invoiceAmounts(html string) (subtotal, discount, shipping, insurance, total float64, found map[string]bool) {
	found = map[string]bool{}
	if m := subtotalRE.FindStringSubmatch(html); m != nil {
		subtotal, found["subtotal"] = parseMoney(m[1]), true
	}
	if m := discountRE.FindStringSubmatch(html); m != nil {
		discount, found["discount"] = parseMoney(m[1]), true
	}
	if m := shippingRE.FindStringSubmatch(html); m != nil {
		shipping, found["shipping"] = parseMoney(m[2]), true
	}
	if m := insuranceRE.FindStringSubmatch(html); m != nil {
		insurance, found["insurance"] = parseMoney(m[1]), true
	}
	if m := totalRowRE.FindStringSubmatch(html); m != nil {
		total, found["total"] = parseMoney(m[1]), true
	}
	return
}

func sampleOrder(subtotal, discount, shipping, insurance float64) *domain.Order {
	return &domain.Order{
		OrderNumber:   "VC-20260101-0001",
		Status:        domain.OrderPaid,
		PaymentStatus: domain.PaymentPaid,
		Subtotal:      subtotal,
		// DiscountAmount is stored as a positive magnitude and printed with a
		// minus sign, matching how the schema and the rest of the system treat it.
		DiscountAmount: discount,
		ShippingFee:    shipping,
		InsuranceFee:   insurance,
		TotalAmount:    math.Round(subtotal - discount + shipping + insurance),
		SellerName:     "Toko uji",
		ShippingMethod: "REG",
		Carrier:        "JNE",
	}
}

func TestInvoiceLinesFootTheTotal(t *testing.T) {
	// The property that was broken. Every combination the checkout can produce
	// must satisfy: subtotal − discount + shipping + insurance == total.
	cases := []struct {
		name                          string
		subtotal, discount, ship, ins float64
	}{
		{"no insurance", 100000, 0, 15000, 0},
		{"insurance only", 100000, 0, 15000, 3000},
		{"with a discount", 250000, 50000, 20000, 7500},
		{"full discount", 100000, 100000, 12000, 0},
		{"free shipping", 50000, 0, 0, 1500},
		{"odd rupiah", 123457, 3333, 9999, 1111},
		{"single cheap item insured", 1500, 0, 9000, 45},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			o := sampleOrder(tc.subtotal, tc.discount, tc.ship, tc.ins)
			html := renderOrderInvoice(o)

			subtotal, discount, shipping, insurance, total, found := invoiceAmounts(html)
			for _, k := range []string{"subtotal", "discount", "shipping", "total"} {
				if !found[k] {
					t.Fatalf("the invoice does not print a %s line at all; the "+
						"test cannot assert it foots", k)
				}
			}
			// An insurance fee on the order MUST appear as a line. This is the
			// specific regression: the fee was charged and stored and invisible.
			if o.InsuranceFee > 0 && !found["insurance"] {
				t.Errorf("the order carries a Rp%.0f insurance fee but the invoice "+
					"prints no line for it, so the printed lines are Rp%.0f short of the total",
					o.InsuranceFee, o.InsuranceFee)
			}
			if o.InsuranceFee == 0 && found["insurance"] {
				t.Error("an insurance line was printed for an order with no insurance fee")
			}

			sum := math.Round(subtotal - discount + shipping + insurance)
			if sum != total {
				t.Errorf("the printed lines sum to Rp%.0f but the invoice prints a total of Rp%.0f\n"+
					"  subtotal %v − discount %v + shipping %v + insurance %v",
					sum, total, subtotal, discount, shipping, insurance)
			}
		})
	}
}

func TestInvoiceOmitsTheInsuranceLineWhenThereIsNoFee(t *testing.T) {
	// A zero line is worse than a missing one: it looks like a charge of Rp0 and
	// invites the reader to wonder what was waived.
	html := renderOrderInvoice(sampleOrder(100000, 0, 15000, 0))
	if insuranceRE.MatchString(html) {
		t.Error("an insurance line was rendered for an order with no insurance")
	}
}

func TestInvoiceOmitsTheInsuranceLineForAFractionalFeeThatRoundsAway(t *testing.T) {
	// A sub-rupiah fee is rounded away by the checkout, so printing "Rp 0" would
	// be a lie about a charge that does not exist.
	html := renderOrderInvoice(sampleOrder(100000, 0, 15000, 0.4))
	if insuranceRE.MatchString(html) {
		t.Error("printed an insurance line for a fee that rounds to zero")
	}
}

// The rendered total must be the ORDER's total, not a number the template
// recomputes. A template that did its own arithmetic would drift from the value
// the buyer was charged the instant the money logic changed.
func TestInvoicePrintsTheStoredTotalRatherThanRecomputingIt(t *testing.T) {
	o := sampleOrder(100000, 0, 15000, 3000)
	// Deliberately inconsistent with the lines: the stored total is authoritative.
	o.TotalAmount = 999999

	_, _, _, _, total, found := invoiceAmounts(renderOrderInvoice(o))
	if !found["total"] {
		t.Fatal("no total printed")
	}
	if total != 999999 {
		t.Errorf("the invoice printed Rp%.0f, want the stored total Rp999999", total)
	}
}

var (
	dppRE = regexp.MustCompile(`DPP \(sebelum PPN\)</td><td style="text-align:right">Rp ([\d.,]+)</td>`)
	ppnRE = regexp.MustCompile(`PPN ([\d.]+)% \(termasuk\)</td><td style="text-align:right">Rp ([\d.,]+)</td>`)
)

// PPN is printed as a BREAKDOWN of the inclusive total, so the two lines must
// sum to the same total the buyer was charged -- not to a larger number.
func TestInvoiceIncludedTaxFootsTheTotal(t *testing.T) {
	for _, total := range []float64{111000, 100000, 250750, 999999} {
		o := sampleOrder(total, 0, 0, 0)
		o.TotalAmount = total

		rec := httptest.NewRecorder()
		renderInvoice(rec, o, invoiceOptions{Number: "INV-202601-000001", PPNRate: 0.11})
		html := rec.Body.String()

		dppMatch := dppRE.FindStringSubmatch(html)
		ppnMatch := ppnRE.FindStringSubmatch(html)
		if dppMatch == nil || ppnMatch == nil {
			t.Fatalf("total %.0f: invoice printed no tax breakdown", total)
		}
		if ppnMatch[1] != "11" {
			t.Errorf("tax rate printed as %s%%, want 11%%", ppnMatch[1])
		}
		dpp, ppn := parseMoney(dppMatch[1]), parseMoney(ppnMatch[2])
		if sum := dpp + ppn; sum != total {
			t.Errorf("total %.0f: DPP %.0f + PPN %.0f = %.0f, want the charged total",
				total, dpp, ppn, sum)
		}
		_, _, _, _, printedTotal, _ := invoiceAmounts(html)
		if printedTotal != total {
			t.Errorf("total %.0f: invoice printed TOTAL %.0f", total, printedTotal)
		}
	}
}

func TestInvoiceOmitsTaxWhenTheRoundedTaxIsZero(t *testing.T) {
	// Rp1 at 11%% rounds to Rp0 of PPN. Printing "Rp 0" looks like a waived
	// charge; the same rule the insurance line follows.
	o := sampleOrder(1, 0, 0, 0)
	o.TotalAmount = 1
	rec := httptest.NewRecorder()
	renderInvoice(rec, o, invoiceOptions{Number: "INV-202601-000001", PPNRate: 0.11})
	if dppRE.MatchString(rec.Body.String()) || ppnRE.MatchString(rec.Body.String()) {
		t.Error("printed a zero-rupiah tax line")
	}
}

func TestInvoiceOmitsTaxWhenNoRateIsConfigured(t *testing.T) {
	html := renderOrderInvoice(sampleOrder(100000, 0, 15000, 0))
	if dppRE.MatchString(html) || ppnRE.MatchString(html) {
		t.Error("tax lines were printed with INVOICE_PPN_RATE unset")
	}
}

func TestInvoicePrintsTheFiscalNumberAndTheOrderReference(t *testing.T) {
	rec := httptest.NewRecorder()
	renderInvoice(rec, sampleOrder(100000, 0, 0, 0), invoiceOptions{Number: "INV-202601-000042"})
	html := rec.Body.String()
	if !strings.Contains(html, "INV-202601-000042") {
		t.Error("the invoice number is missing")
	}
	if !strings.Contains(html, "VC-20260101-0001") {
		t.Error("the order number is missing; support needs both references")
	}
}

// A faktur carries the seller's tax identity. When none was submitted the line
// must be ABSENT, not "NPWP: -": a placeholder on a tax document reads as a
// number that exists.
func TestInvoicePrintsSellerNPWPOnlyWhenPresent(t *testing.T) {
	o := sampleOrder(100000, 0, 0, 0)

	rec := httptest.NewRecorder()
	renderInvoice(rec, o, invoiceOptions{Number: "INV-202601-000001", SellerNPWP: "123456789012000"})
	if html := rec.Body.String(); !strings.Contains(html, "NPWP: 123456789012000") {
		t.Error("the invoice did not print the seller NPWP")
	}

	rec = httptest.NewRecorder()
	renderInvoice(rec, o, invoiceOptions{Number: "INV-202601-000001"})
	if html := rec.Body.String(); strings.Contains(html, "NPWP") {
		t.Error("the invoice printed an NPWP line without a number")
	}
}
