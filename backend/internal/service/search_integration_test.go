package service

import (
	"context"
	"strings"
	"testing"

	"github.com/vincommerce/backend/internal/domain"
	"github.com/vincommerce/backend/internal/repository"
)

// Search stemmed Indonesian text with the ENGLISH Snowball configuration.
// English stemming does not strip Indonesian affixes, so "makanan" and
// "makan" are unrelated lexemes: a shopper typing the root word got nothing.
//
// These assertions need a real PostgreSQL because the configuration lives in
// the database (a GENERATED column and an expression index), not in Go. The
// unit suite could not see this defect at all.
func TestSearchStemsIndonesianText(t *testing.T) {
	pool := integrationPool(t)
	ctx := context.Background()

	var indonesianMatches bool
	if err := pool.QueryRow(ctx,
		`SELECT to_tsvector('indonesian', 'makanan') @@ websearch_to_tsquery('indonesian', 'makan')`,
	).Scan(&indonesianMatches); err != nil {
		t.Fatalf("indonesian text search config is unavailable: %v", err)
	}
	if !indonesianMatches {
		t.Fatal("indonesian stemming does not reduce makanan -> makan; the config this migration relies on is missing")
	}

	// This is the before-state, kept deliberately. If PostgreSQL ever changes
	// english stemming to also strip Indonesian affixes, the migration becomes
	// unnecessary and this assertion tells us rather than silently passing.
	var englishMatches bool
	if err := pool.QueryRow(ctx,
		`SELECT to_tsvector('english', 'makanan') @@ websearch_to_tsquery('english', 'makan')`,
	).Scan(&englishMatches); err != nil {
		t.Fatalf("english text search config: %v", err)
	}
	if englishMatches {
		t.Fatal("english stemming now matches makanan/makan -- revisit migration 00054")
	}

	// The stored column must carry the configuration, not just the queries:
	// a query-level 'indonesian' cast against an 'english' tsvector cannot use
	// the index and silently mismatches.
	var columnExpr string
	if err := pool.QueryRow(ctx, `
		SELECT pg_get_expr(d.adbin, d.adrelid)
		FROM pg_attribute a
		JOIN pg_attrdef d ON d.adrelid = a.attrelid AND d.adnum = a.attnum
		WHERE a.attrelid = 'products'::regclass AND a.attname = 'search_vector'`,
	).Scan(&columnExpr); err != nil {
		t.Fatalf("read products.search_vector definition: %v", err)
	}
	if !strings.Contains(columnExpr, "'indonesian'") {
		t.Fatalf("products.search_vector is not indonesian: %s", columnExpr)
	}

	var helpIndex string
	if err := pool.QueryRow(ctx,
		`SELECT pg_get_indexdef('idx_help_articles_search'::regclass)`).Scan(&helpIndex); err != nil {
		t.Fatalf("read help search index: %v", err)
	}
	if !strings.Contains(helpIndex, "'indonesian'") {
		t.Fatalf("help article index is not indonesian: %s", helpIndex)
	}

	// And the ordinary English-named catalogue still searches normally. A
	// migration that broke search entirely would satisfy the checks above.
	products := repository.NewProductRepository(pool)
	res, err := products.Search(ctx, &domain.ProductFilter{Query: "laptop", Page: 1, PageSize: 10})
	if err != nil {
		t.Fatalf("Search(laptop): %v", err)
	}
	if res.Total == 0 {
		t.Fatal("Search(laptop) returned nothing after the config change")
	}
}
