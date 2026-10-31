package service

import (
	"context"
	"testing"
)

// The database refuses fractional rupiah on the transactional money tables
// (migration 00060). This checks the seeded data satisfies the invariant and,
// more importantly, that the paths exercised by the other integration tests
// still write whole values -- a fractional write now fails the INSERT, and this
// test names the table instead of leaving a constraint error in a log.
func TestStoredMoneyIsWholeRupiah(t *testing.T) {
	pool := integrationPool(t)
	ctx := context.Background()

	checks := []struct{ table, expr string }{
		{"orders", "subtotal <> trunc(subtotal) OR discount_amount <> trunc(discount_amount) OR " +
			"shipping_fee <> trunc(shipping_fee) OR insurance_fee <> trunc(insurance_fee) OR " +
			"total_amount <> trunc(total_amount)"},
		{"order_items", "unit_price <> trunc(unit_price) OR total <> trunc(total)"},
		{"payment_intents", "amount <> trunc(amount) OR fee_amount <> trunc(fee_amount) OR seller_amount <> trunc(seller_amount)"},
		{"wallets", "balance <> trunc(balance) OR held_balance <> trunc(held_balance)"},
		{"wallet_transactions", "amount <> trunc(amount) OR balance_after <> trunc(balance_after)"},
		{"wallet_topups", "amount <> trunc(amount)"},
		{"payouts", "amount <> trunc(amount)"},
		{"refunds", "amount <> trunc(amount)"},
		{"seller_reservations", "amount <> trunc(amount)"},
		{"ledger_entries", "amount <> trunc(amount)"},
		{"account_balances", "balance <> trunc(balance)"},
	}

	for _, c := range checks {
		var bad int
		if err := pool.QueryRow(ctx,
			"SELECT count(*) FROM "+c.table+" WHERE "+c.expr).Scan(&bad); err != nil {
			t.Fatalf("%s: %v", c.table, err)
		}
		if bad != 0 {
			t.Errorf("%s has %d row(s) with fractional rupiah", c.table, bad)
		}
	}
}
