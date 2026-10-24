package service

import (
	"context"
	"os"
	"strconv"
	"testing"
	"time"

	"github.com/vincommerce/backend/internal/config"
	"github.com/vincommerce/backend/internal/db"
	"github.com/vincommerce/backend/internal/repository"
)

// Integration test against a REAL database.
//
// Opt-in via INTEGRATION_DB=1, because the suite must still run on a machine with no
// Postgres (this project's development machine cannot start one). CI runs it after
// the migrate+seed step, where the seeded bundle and shipping methods exist.
//
// Why it earns its place: the last three defects that reached production-shaped
// failures were all SQL-level -- a 17-vs-15 column scan that 500'd every store page,
// a PUT that matched zero rows and returned 200, and a seed statement Postgres
// refused. None of them could be seen by a source-inspection test, and the package
// coverage of `repository` is 1.1%.
func integrationPool(t *testing.T) *db.Pool {
	t.Helper()
	if os.Getenv("INTEGRATION_DB") == "" {
		t.Skip("set INTEGRATION_DB=1 with a migrated database to run integration tests")
	}
	port, err := strconv.Atoi(envOr("DB_PORT", "5432"))
	if err != nil {
		t.Fatalf("DB_PORT: %v", err)
	}
	pool, err := db.Connect(context.Background(), config.DatabaseConfig{
		Host:     envOr("DB_HOST", "127.0.0.1"),
		Port:     port,
		User:     envOr("DB_USER", "vincom"),
		Password: envOr("DB_PASSWORD", "vincom_dev"),
		Name:     envOr("DB_NAME", "vincom"),
		SSLMode:  envOr("DB_SSL_MODE", "disable"),
		MaxConns: 4,
		MinConns: 1,
		MaxIdle:  time.Minute,
		MaxLife:  time.Minute,
	})
	if err != nil {
		t.Fatalf("connect: %v", err)
	}
	t.Cleanup(pool.Close)
	return pool
}

func envOr(key, fallback string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return fallback
}

// TestQuoteAppliesSeededBundle exercises the money path end to end against Postgres:
// the cart write, the bundle listing, the strict-match decision and the quote's
// arithmetic. QuoteCheckout is what PlaceOrder calls, so a correct quote here is the
// same code that charges the buyer.
func TestQuoteAppliesSeededBundle(t *testing.T) {
	pool := integrationPool(t)
	ctx := context.Background()

	svc := NewOrderService(
		repository.NewCartRepository(pool),
		repository.NewOrderRepository(pool),
		repository.NewAddressRepository(pool),
	)
	svc.SetStores(repository.NewStoreRepository(pool))
	svc.SetBundles(repository.NewQARepository(pool))

	var userID string
	if err := pool.QueryRow(ctx,
		`SELECT id FROM users WHERE email = 'buyer.sample@vincommerce.com'`).Scan(&userID); err != nil {
		t.Fatalf("seeded buyer missing: %v", err)
	}

	var variantID string
	if err := pool.QueryRow(ctx, `
		SELECT v.id FROM product_variants v
		JOIN products p ON p.id = v.product_id
		WHERE p.slug = 'smartphone-aurora-x5-pro' AND v.name = 'Black'`).Scan(&variantID); err != nil {
		t.Fatalf("the bundle's seeded variant is missing: %v", err)
	}

	var sellerID string
	if err := pool.QueryRow(ctx, `
		SELECT p.seller_id FROM products p
		JOIN product_variants v ON v.product_id = p.id
		WHERE v.id = $1`, variantID).Scan(&sellerID); err != nil {
		t.Fatalf("seller lookup: %v", err)
	}

	cart, err := svc.CartFor(ctx, userID, "")
	if err != nil {
		t.Fatalf("cart: %v", err)
	}
	// Deterministic starting point: this account may carry lines from the seed.
	if _, err := pool.Exec(ctx, `DELETE FROM cart_items WHERE cart_id = $1`, cart.ID); err != nil {
		t.Fatalf("clear cart: %v", err)
	}

	quote := func(t *testing.T) *Quote {
		t.Helper()
		q, err := svc.QuoteCheckout(ctx, userID, cart.ID, "", "regular", false, 0)
		if err != nil {
			t.Fatalf("quote: %v", err)
		}
		return q
	}

	// Exactly the bundle's quantities: the discount must be charged.
	if _, _, err := svc.UpdateCartItem(ctx, cart.ID, variantID, 2); err != nil {
		t.Fatalf("set quantity 2: %v", err)
	}
	exact := quote(t)
	if len(exact.Bundles) != 1 {
		t.Fatalf("the bundle did not apply at 2 units: %+v", exact.Bundles)
	}
	if exact.Bundles[0].Discount <= 0 {
		t.Fatalf("bundle applied with no discount: %+v", exact.Bundles[0])
	}
	if exact.DiscountAmount != exact.Bundles[0].Discount {
		t.Fatalf("quote discount %v does not equal the applied bundle %v -- the buyer "+
			"would be shown one number and charged another",
			exact.DiscountAmount, exact.Bundles[0].Discount)
	}
	// DiscountAmount is what the UI shows; DiscountFor is what PlaceOrder actually
	// subtracts per sub-order. If those two ever disagree, the quote is honest and
	// the charge is not -- or the reverse.
	if got := exact.DiscountFor(sellerID); got != exact.Bundles[0].Discount {
		t.Fatalf("charged discount for the seller = %v, displayed = %v: the quote and "+
			"the charge disagree", got, exact.Bundles[0].Discount)
	}

	// One unit more: strict match fails, so neither the bundle nor its discount
	// may survive.
	if _, _, err := svc.UpdateCartItem(ctx, cart.ID, variantID, 3); err != nil {
		t.Fatalf("set quantity 3: %v", err)
	}
	strict := quote(t)
	if len(strict.Bundles) != 0 {
		t.Fatalf("a bundle applied to a cart it does not describe: %+v", strict.Bundles)
	}
	if strict.DiscountAmount != 0 {
		t.Fatalf("a discount survived the bundle not matching: %v", strict.DiscountAmount)
	}

	// The cart write itself: this is the SQL that silently matched zero rows once.
	var stored int
	if err := pool.QueryRow(ctx,
		`SELECT quantity FROM cart_items WHERE cart_id = $1 AND variant_id = $2`,
		cart.ID, variantID).Scan(&stored); err != nil {
		t.Fatalf("the cart line is not readable: %v", err)
	}
	if stored != 3 {
		t.Fatalf("cart quantity = %d, want 3", stored)
	}
}
