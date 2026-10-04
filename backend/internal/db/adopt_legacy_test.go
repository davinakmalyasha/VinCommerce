package db

import "testing"

// The legacy tracker stored whole filenames. Confirm the shapes that occur in the
// real table parse to the version goose needs, and that the ones that cannot be
// interpreted are refused rather than silently treated as version 0.
func TestLegacyVersionPrefixParsesWhatTheLegacyTrackerActuallyStored(t *testing.T) {
	cases := []struct {
		stored string
		want   int
		wantOK bool
	}{
		// Real rows from the legacy `schema_migrations` table.
		{"0031_index_hot.sql", 31, true},
		{"0001_init.sql", 1, true},
		{"0020_backinstock.sql", 20, true},
		// Already bare, which some rows were.
		{"31", 31, true},
		// Must not silently become 0.
		{"", 0, false},
		{"_no_prefix.sql", 0, false},
	}

	for _, c := range cases {
		m := legacyVersionPrefix.FindStringSubmatch(c.stored)
		if !c.wantOK {
			if m != nil {
				t.Errorf("stored version %q parsed to %q; it should be refused, because "+
					"treating it as a version makes goose skip migrations that were never applied",
					c.stored, m[1])
			}
			continue
		}
		if m == nil {
			t.Errorf("stored version %q did not parse; the legacy tracker wrote filenames like this", c.stored)
			continue
		}
		if got := mustAtoi(t, m[1]); got != c.want {
			t.Errorf("stored version %q parsed to %d, want %d", c.stored, got, c.want)
		}
	}
}

func mustAtoi(t *testing.T, s string) int {
	t.Helper()
	n := 0
	for _, r := range s {
		if r < '0' || r > '9' {
			t.Fatalf("non-digit %q in version %q", r, s)
		}
		n = n*10 + int(r-'0')
	}
	return n
}

// The regex must be anchored, because it is applied to a database string and a
// prefix match would turn `x0031.sql` into version 31.
func TestLegacyVersionPrefixIsAnchoredAtTheStart(t *testing.T) {
	if legacyVersionPrefix.MatchString("x0031.sql") {
		t.Error("pattern matched mid-string; an unparsable version must be refused, " +
			"not silently accepted after discarding leading junk")
	}
}
