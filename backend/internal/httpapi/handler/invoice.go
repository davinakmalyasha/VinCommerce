package handler

import (
	"fmt"
	"math"
	"net/http"

	"github.com/vincommerce/backend/internal/domain"
)

// renderInvoice serves a printable HTML invoice for an order.
func renderInvoice(w http.ResponseWriter, o *domain.Order) {
	var rows string
	for _, it := range o.Items {
		rows += fmt.Sprintf(`<tr><td>%s</td><td>%s</td><td>%d</td><td style="text-align:right">%s</td><td style="text-align:right">%s</td></tr>`,
			htmlEsc(it.ProductName), htmlEsc(it.VariantName), it.Quantity,
			f2(it.UnitPrice), f2(it.Total))
	}

	// The insurance line, printed only when there is a charge worth showing.
	//
	// It was missing, and the printed lines therefore did not foot: subtotal minus
	// discount plus shipping is LESS than the printed total whenever insurance was
	// selected, because the fee is added to `quote.Total` and stored on
	// `Order.InsuranceFee` but was never rendered. An invoice whose own lines do
	// not sum to its own total is not a rounding artefact an Indonesian seller
	// can expense; it is a document that fails an audit on sight.
	//
	// The README claimed this was fixed. The ARITHMETIC was -- the fee is
	// correctly rounded and correctly added -- but the presentation was not, and
	// the claim outlived the code.
	//
	// Gated on the ROUNDED amount, not the raw one: `f2` prints whole rupiah, so
	// a stored 0.4 would render as a line reading "Rp 0". A zero line is worse
	// than no line -- it looks like a charge and invites the reader to wonder what
	// was waived.
	insuranceRow := ""
	if rounded := math.Round(o.InsuranceFee); rounded > 0 {
		insuranceRow = fmt.Sprintf(
			`  <tr><td colspan="3"></td><td style="text-align:right">Asuransi pengiriman</td><td style="text-align:right">Rp %s</td></tr>`+"\n",
			f2(rounded))
	}

	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	fmt.Fprintf(w, invoiceHTML,
		o.OrderNumber, // title
		o.OrderNumber, // header
		o.PlacedAt.Format("02 Jan 2006 15:04"),
		htmlEsc(fmt.Sprintf("%v", o.ShippingAddress["recipient"])),
		htmlEsc(fmt.Sprintf("%v", o.ShippingAddress["address_line1"])),
		htmlEsc(fmt.Sprintf("%v", o.ShippingAddress["city"])),
		htmlEsc(fmt.Sprintf("%v", o.ShippingAddress["province"])),
		htmlEsc(fmt.Sprintf("%v", o.ShippingAddress["postal_code"])),
		htmlEsc(o.SellerName),
		o.PaymentStatus,
		rows,
		f2(o.Subtotal), f2(o.DiscountAmount),
		htmlEsc(o.ShippingMethod), f2(o.ShippingFee),
		insuranceRow,
		f2(o.TotalAmount),
		htmlEsc(o.ShippingMethod), htmlEsc(o.TrackingNumber), htmlEsc(o.Carrier),
	)
}

func htmlEsc(s string) string {
	if s == "" {
		return "&nbsp;"
	}
	out := make([]rune, 0, len(s))
	for _, r := range s {
		switch r {
		case '<':
			out = append(out, '&', 'l', 't', ';')
		case '>':
			out = append(out, '&', 'g', 't', ';')
		case '&':
			out = append(out, '&', 'a', 'm', 'p', ';')
		default:
			out = append(out, r)
		}
	}
	return string(out)
}

func f2(v float64) string {
	return fmt.Sprintf("%.0f", v)
}

// renderPackingSlip serves a dense, price-free picking/packing sheet.
func renderPackingSlip(w http.ResponseWriter, o *domain.Order) {
	var rows string
	for _, it := range o.Items {
		rows += fmt.Sprintf(`<tr>
			<td style="font-family:monospace;font-size:15px">%s</td>
			<td>%s<br><span style="color:#6b7280;font-size:12px">%s</span></td>
			<td style="font-size:20px;font-weight:800;text-align:center">%d</td>
			<td></td>
		</tr>`,
			htmlEsc(it.SKU), htmlEsc(it.ProductName), htmlEsc(it.VariantName), it.Quantity)
	}

	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	fmt.Fprintf(w, packingSlipHTML,
		o.OrderNumber, // title
		o.OrderNumber, // header
		o.PlacedAt.Format("02 Jan 2006 15:04"),
		htmlEsc(fmt.Sprintf("%v", o.ShippingAddress["recipient"])),
		htmlEsc(fmt.Sprintf("%v", o.ShippingAddress["phone"])),
		htmlEsc(fmt.Sprintf("%v", o.ShippingAddress["address_line1"])),
		htmlEsc(fmt.Sprintf("%v", o.ShippingAddress["city"])),
		htmlEsc(fmt.Sprintf("%v", o.ShippingAddress["province"])),
		htmlEsc(fmt.Sprintf("%v", o.ShippingAddress["postal_code"])),
		htmlEsc(o.Notes),
		rows,
		htmlEsc(o.ShippingMethod), htmlEsc(o.Carrier),
	)
}

var _ = domain.OrderCompleted

const invoiceHTML = `<!DOCTYPE html>
<html>
<head>
<meta charset="UTF-8"><title>Invoice %s</title>
<style>
  body { font-family: system-ui, sans-serif; max-width: 720px; margin: 32px auto; padding: 0 24px; color: #111; }
  .head { display: flex; justify-content: space-between; align-items: center; border-bottom: 3px solid #d97706; padding-bottom: 16px; }
  .brand { font-size: 22px; font-weight: 800; color: #d97706; }
  .meta { color: #6b7280; font-size: 13px; }
  table { width: 100%%; border-collapse: collapse; margin: 24px 0; font-size: 14px; }
  th { text-align: left; padding: 8px; border-bottom: 2px solid #e5e7eb; color: #6b7280; font-size: 12px; text-transform: uppercase; }
  td { padding: 8px; border-bottom: 1px solid #f3f4f6; }
  .total-row td { font-weight: 700; border-top: 2px solid #111; border-bottom: none; font-size: 15px; }
  .addr { font-size: 13px; color: #374151; line-height: 1.6; }
  .foot { margin-top: 32px; border-top: 1px solid #e5e7eb; padding-top: 12px; font-size: 12px; color: #9ca3af; }
  .print-btn { position: fixed; top: 16px; right: 16px; padding: 10px 20px; background: #111; color: #fff; border: 0; border-radius: 8px; cursor: pointer; font-size: 13px; }
  @media print { .print-btn { display: none; } }
</style>
</head>
<body>
<button class="print-btn" onclick="window.print()">Cetak / Simpan PDF</button>
<div class="head">
  <div>
    <div class="brand">VinCommerce</div>
    <div class="meta">Invoice %s · Marketplace escrow</div>
  </div>
  <div class="meta" style="text-align:right">Tanggal: %s</div>
</div>

<div style="display:flex; gap:48px; margin-top:24px;">
  <div class="addr">
    <b>Dikirim ke</b><br>%s<br>%s<br>%s, %s %s
  </div>
  <div class="addr">
    <b>Penjual</b><br>%s<br>Pembayaran: %s
  </div>
</div>

<table>
  <tr><th>Produk</th><th>Varian</th><th>Qty</th><th style="text-align:right">Harga</th><th style="text-align:right">Total</th></tr>
  %s
  <tr class="total-row"><td colspan="3"></td><td style="text-align:right">Subtotal</td><td style="text-align:right">Rp %s</td></tr>
  <tr><td colspan="3"></td><td style="text-align:right">Diskon</td><td style="text-align:right">− Rp %s</td></tr>
  <tr><td colspan="3"></td><td style="text-align:right">Ongkir (%s)</td><td style="text-align:right">Rp %s</td></tr>
%s  <tr class="total-row"><td colspan="3"></td><td style="text-align:right">TOTAL</td><td style="text-align:right">Rp %s</td></tr>
</table>

<div class="meta">Kurir: %s · Nomor resi: %s (%s)</div>
<div class="foot">VinCommerce — platform marketplace dengan pembayaran escrow. Invoice ini sah tanpa tanda tangan.</div>
</body>
</html>`

const packingSlipHTML = `<!DOCTYPE html>
<html>
<head>
<meta charset="UTF-8"><title>Packing Slip %s</title>
<style>
  body { font-family: system-ui, sans-serif; max-width: 640px; margin: 24px auto; padding: 0 20px; color: #111; }
  .head { border-bottom: 3px solid #4f46e5; padding-bottom: 10px; }
  .brand { font-size: 20px; font-weight: 800; color: #4f46e5; }
  .meta { color: #6b7280; font-size: 13px; }
  table { width: 100%%; border-collapse: collapse; margin: 18px 0; }
  th { text-align: left; padding: 8px; border-bottom: 2px solid #e5e7eb; color: #6b7280; font-size: 12px; text-transform: uppercase; }
  td { padding: 10px 8px; border-bottom: 1px dashed #d1d5db; vertical-align: top; }
  .addr { font-size: 15px; line-height: 1.55; background: #f9fafb; padding: 14px; border-radius: 8px; }
  .notes { margin-top: 12px; font-size: 13px; color: #b45309; }
  .print-btn { position: fixed; top: 16px; right: 16px; padding: 10px 20px; background: #111; color: #fff; border: 0; border-radius: 8px; cursor: pointer; font-size: 13px; }
  @media print { .print-btn { display: none; } }
</style>
</head>
<body>
<button class="print-btn" onclick="window.print()">Cetak</button>
<div class="head">
  <div class="brand">PACKING SLIP</div>
  <div class="meta">%s · %s</div>
</div>

<h3 style="margin-bottom:8px">Kirim ke:</h3>
<div class="addr">
  <b style="font-size:17px">%s</b> · %s<br>
  %s<br>%s, %s %s
</div>
%s

<table>
  <tr><th>SKU</th><th>Produk / Varian</th><th style="text-align:center">Qty</th><th style="text-align:center">✓</th></tr>
  %s
</table>

<div class="meta">Metode: %s · Kurir: %s</div>
</body>
</html>`
