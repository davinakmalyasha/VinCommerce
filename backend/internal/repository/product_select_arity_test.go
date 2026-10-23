package repository

import (
	"os"
	"strings"
	"testing"
)

// THE DEFECT.
//
// ListActiveBySeller selected 17 columns and called rows.Scan with 15 destinations:
//
//	number of field descriptions must equal number of destinations, got 17 and 15
//
// The two extras were COALESCE(SUM(stock)) and COUNT(variants) subqueries with no
// corresponding field on domain.Product and no consumer. pgx fails the Scan at
// EXECUTION time, and only when a row exists -- so the public store page returned 500
// for every store, while a missing slug correctly returned 404. Nothing caught it
// because no store row existed until the seed started creating them.
//
// This pins the arity, which is a static property of one query. It is deliberately
// narrow: a general SQL/Scan comparator is the kind of checker this repository has
// already deleted once for crying wolf.

func repoFuncBody(t *testing.T, fileName, signature string) string {
	t.Helper()
	raw, err := os.ReadFile(fileName)
	if err != nil {
		t.Fatalf("read %s: %v", fileName, err)
	}
	src := string(raw)
	i := strings.Index(src, signature)
	if i < 0 {
		t.Fatalf("no %s", signature)
	}
	body := src[i:]
	if end := strings.Index(body[1:], "\nfunc "); end > 0 {
		return body[:end]
	}
	return body
}

// splitTopLevel splits on commas that are not inside parentheses.
func splitTopLevel(s string) []string {
	var out []string
	depth := 0
	start := 0
	for i, r := range s {
		switch r {
		case '(':
			depth++
		case ')':
			depth--
		case ',':
			if depth == 0 {
				out = append(out, s[start:i])
				start = i + 1
			}
		}
	}
	out = append(out, s[start:])
	return out
}

// scanArgs returns the text between the parentheses of the first rows.Scan call,
// balancing nested parentheses so a subquery or a cast cannot truncate it.
func scanArgs(t *testing.T, body string) string {
	t.Helper()
	i := strings.Index(body, "rows.Scan(")
	if i < 0 {
		t.Fatal("no rows.Scan")
	}
	open := i + len("rows.Scan")
	depth := 0
	for j := open; j < len(body); j++ {
		switch body[j] {
		case '(':
			depth++
		case ')':
			depth--
			if depth == 0 {
				return body[open+1 : j]
			}
		}
	}
	t.Fatal("unbalanced rows.Scan")
	return ""
}

func assertSelectMatchesScan(t *testing.T, signature string) {
	t.Helper()
	body := repoFuncBody(t, "product_repo.go", signature)
	// Anchor on the rows query, not the COUNT(*) query above it, which also starts
	// with SELECT and would be mistaken for the column list.
	q := strings.Index(body, "r.pool.Query(")
	if q < 0 {
		t.Fatal("no r.pool.Query")
	}
	rest := body[q:]
	selStart := strings.Index(rest, "SELECT ")
	selEnd := strings.Index(rest, "FROM products")
	if selStart < 0 || selEnd < selStart {
		t.Fatal("cannot locate the SELECT list")
	}
	cols := splitTopLevel(rest[selStart+len("SELECT ") : selEnd])
	destinations := strings.Count(scanArgs(t, body), "&")
	if len(cols) != destinations {
		t.Fatalf("%s lists %d columns but Scan takes %d destinations; pgx fails the "+
			"Scan at execution time, and only when a row exists, so the endpoint "+
			"returns 500 instead of data", signature, len(cols), destinations)
	}
}

func TestProductSelectsMatchTheirScans(t *testing.T) {
	assertSelectMatchesScan(t, "func (r *ProductRepository) ListActiveBySeller(")
	// ListBySeller had the same two orphaned columns and the same 17-vs-15 mismatch;
	// it backs the seller's own product list, so that page 500'd too.
	assertSelectMatchesScan(t, "func (r *ProductRepository) ListBySeller(")
}
