package repository

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// Every SELECT's column count must equal the number of destinations in the matching
// rows.Scan.
//
// This is not hypothetical: ListActiveBySeller selected 17 columns and scanned 15, so
// pgx failed the Scan at execution time and ONLY when a row existed. A missing slug
// returned a clean 404 while a real store returned 500, and every public store page
// was broken for as long as the query existed. Nothing in the compiler, and no test
// without a database, could see it.
//
// The guard is deliberately conservative. It checks only what can be parsed with
// confidence:
//   - functions containing exactly ONE `pool.Query(` and exactly ONE `rows.Scan(`
//     (otherwise pairing them is guesswork),
//   - a SQL string that is a single literal (no concatenation, which some queries
//     legitimately use),
//   - a literal column list (not `SELECT *`).
//
// This repository has already deleted one checker that cried wolf; a guard that
// reports false positives is worse than none, so anything ambiguous is skipped
// rather than guessed at.

func funcBodies(t *testing.T) map[string]string {
	t.Helper()
	files, err := filepath.Glob("*.go")
	if err != nil {
		t.Fatal(err)
	}
	out := map[string]string{}
	for _, f := range files {
		if strings.HasSuffix(f, "_test.go") {
			continue
		}
		raw, err := os.ReadFile(f)
		if err != nil {
			t.Fatalf("read %s: %v", f, err)
		}
		src := string(raw)
		idx := 0
		for {
			i := strings.Index(src[idx:], "\nfunc ")
			if i < 0 {
				break
			}
			start := idx + i + 1
			body := src[start:]
			end := len(body)
			if j := strings.Index(body[1:], "\nfunc "); j >= 0 {
				end = j + 1
			}
			// Unique key: method names repeat within a file's receivers.
			key := fmt.Sprintf("%s:%s#%d", f, functionName(body), len(out))
			out[key] = body[:end]
			idx = start + end
		}
	}
	return out
}

func functionName(body string) string {
	rest := strings.TrimPrefix(body, "func ")
	// A method has a receiver: skip it so the name is the method, not "(r".
	if strings.HasPrefix(rest, "(") {
		if j := strings.Index(rest, ")"); j >= 0 {
			rest = rest[j+1:]
		}
	}
	rest = strings.TrimSpace(rest)
	if i := strings.IndexAny(rest, "( {"); i >= 0 {
		rest = rest[:i]
	}
	return rest
}

// sqlLiteralAfter returns the backtick-quoted SQL that starts after marker, and
// whether it is a single literal (no concatenation before the closing backtick).
func sqlLiteralAfter(body, marker string) (string, bool) {
	i := strings.Index(body, marker)
	if i < 0 {
		return "", false
	}
	rest := body[i+len(marker):]
	open := strings.Index(rest, "`")
	if open < 0 {
		return "", false
	}
	// Concatenation before the literal means the query is built dynamically.
	if strings.Contains(rest[:open], "+") {
		return "", false
	}
	close := strings.Index(rest[open+1:], "`")
	if close < 0 {
		return "", false
	}
	return rest[open+1 : open+1+close], true
}

// selectColumns returns the number of top-level expressions in the SELECT list, and
// false when the list cannot be trusted.
func selectColumns(sql string) (int, bool) {
	up := strings.ToUpper(sql)
	sel := strings.Index(up, "SELECT ")
	if sel < 0 {
		return 0, false
	}
	rest := sql[sel+len("SELECT "):]
	upRest := strings.ToUpper(rest)
	depth := 0
	inQuote := false
	topStar := false
	for i := 0; i < len(rest); i++ {
		c := rest[i]
		// A star at depth 0 means SELECT *, which has no fixed column count. One
		// inside COUNT(*) or a similar call is a normal expression.
		if c == '*' && depth == 0 && !inQuote {
			topStar = true
		}
		if c == '\'' {
			inQuote = !inQuote
			continue
		}
		if inQuote {
			continue
		}
		switch c {
		case '(':
			depth++
		case ')':
			depth--
		}
		// The FIRST " FROM " at depth 0 ends the column list. A subquery in the list
		// has its own FROM, and matching that one truncated the list -- which is how
		// this guard reported ListBySeller as 16 columns when it selects 18.
		// FROM may be preceded by a newline or a tab, not just a space: matching
		// " FROM " skipped every multi-line query and reported 0 columns.
		if depth == 0 && strings.HasPrefix(upRest[i:], "FROM") &&
			(i == 0 || upRest[i-1] == ' ' || upRest[i-1] == '\n' || upRest[i-1] == '\t') &&
			(i+4 == len(upRest) || upRest[i+4] == ' ' || upRest[i+4] == '\n' || upRest[i+4] == '\t') {
			if topStar {
				return 0, false
			}
			return len(splitTopLevelSQL(rest[:i])), true
		}
	}
	return 0, false
}

// splitTopLevelSQL splits on commas outside parentheses and outside string literals.
func splitTopLevelSQL(s string) []string {
	var out []string
	depth := 0
	inQuote := false
	start := 0
	for i := 0; i < len(s); i++ {
		c := s[i]
		if c == '\'' {
			inQuote = !inQuote
			continue
		}
		if inQuote {
			continue
		}
		switch c {
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

func scanDestinationCount(body string) (int, bool) {
	i := strings.Index(body, "rows.Scan(")
	if i < 0 {
		return 0, false
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
				return strings.Count(body[open+1:j], "&"), true
			}
		}
	}
	return 0, false
}

func TestEverySingleQuerySelectMatchesItsScan(t *testing.T) {
	checked := 0
	for name, body := range funcBodies(t) {
		if strings.Count(body, "pool.Query(") != 1 || strings.Count(body, "rows.Scan(") != 1 {
			continue
		}
		sql, ok := sqlLiteralAfter(body, "pool.Query(ctx, ")
		if !ok {
			continue
		}
		cols, ok := selectColumns(sql)
		if !ok {
			continue
		}
		dest, ok := scanDestinationCount(body)
		if !ok {
			continue
		}
		checked++
		if cols != dest {
			t.Errorf("%s: SELECT lists %d columns but rows.Scan takes %d destinations; "+
				"pgx fails that Scan at execution time and only when a row exists, so "+
				"the endpoint returns 500 instead of data", name, cols, dest)
		}
	}
	if checked == 0 {
		t.Fatal("no queries were checked: the parser has stopped matching the source")
	}
	t.Logf("checked %d single-query functions", checked)
}
