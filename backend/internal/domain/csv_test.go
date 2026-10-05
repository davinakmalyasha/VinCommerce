package domain

import "testing"

// The escaper is the only thing standing between a seller-supplied string and code
// execution on the machine of whoever opens the export. It is worth pinning precisely.
func TestCSVCellNeutralizesEveryFormulaPrefix(t *testing.T) {
	// Every prefix the escaper claims to handle, asserted against the exported list so
	// the switch and the list cannot drift apart.
	for _, p := range CSVInjectionPrefixes {
		in := p + `HYPERLINK("http://evil.example","click")`
		if !HasCSVInjection(in) {
			t.Errorf("fixture %q is not detected as an injection; the test is vacuous", in)
		}
		got := CSVCell(in)
		// The escaped value must be INERT. `HasCSVInjection` looks at the first byte,
		// and the apostrophe now occupies it, so this must come back false.
		if HasCSVInjection(got) {
			t.Errorf("CSVCell(%q) = %q, which is still executable", p, got)
		}
		if got != "'"+in {
			t.Errorf("CSVCell(%q) = %q, want an apostrophe prefix", p, got)
		}
	}
}

// A value that does not start with a formula character must be untouched. Escaping
// indiscriminately would corrupt legitimate data -- a seller called "-Bakso-" or a
// tracking ref with a leading tab would come out visibly wrong to the reader.
func TestCSVCellLeavesOrdinaryValuesAlone(t *testing.T) {
	for _, s := range []string{
		"", "BCA", "1234567890", "PT Bukalapak", "a=b", "x+y", "name@domain.com",
		"  leading space", "Sari", "Rp 1.000",
	} {
		if got := CSVCell(s); got != s {
			t.Errorf("CSVCell(%q) = %q, want it unchanged", s, got)
		}
	}
}

// A leading space defeats execution in the spreadsheets checked, so it is not treated
// as an injection. Pinning the decision so someone "fixing" it knows it was considered:
// prefixing every value with a space would misalign every column.
func TestCSVCellDoesNotTreatLeadingSpaceAsInjection(t *testing.T) {
	s := " =cmd|'/c calc'!A0"
	if HasCSVInjection(s) {
		t.Error("a leading space was reported as an injection; it is not, and treating " +
			"it as one would corrupt every value that legitimately starts with a space")
	}
	if got := CSVCell(s); got != s {
		t.Errorf("CSVCell(%q) = %q, want unchanged", s, got)
	}
}

func TestHasCSVInjectionOnlyLooksAtTheFirstByte(t *testing.T) {
	// "a=b" contains '=' but does not begin with it, so it is inert.
	if HasCSVInjection("a=b") {
		t.Error("HasCSVInjection(\"a=b\") = true; only the first byte can start a formula")
	}
	if !HasCSVInjection("=b") {
		t.Error("HasCSVInjection(\"=b\") = false")
	}
}

func TestCSVRowEscapesEveryField(t *testing.T) {
	got := CSVRow("plain", "=evil()", "-dash", "@at")
	for i, want := range []string{"plain", "'=evil()", "'-dash", "'@at"} {
		if got[i] != want {
			t.Errorf("CSVRow field %d = %q, want %q", i, got[i], want)
		}
	}
}

// CSVRowMixed exists so numerics and timestamps are NOT passed through a text escaper.
// Escaping a formatted amount would be a category error: it cannot be dangerous, and
// doing it would hide a future formatting bug behind a stray apostrophe.
func TestCSVRowMixedLeavesNumericsAndTimestampsAlone(t *testing.T) {
	fields := []string{"RF-1", "p-1", "s-1", "BCA", "12345", "15000", "2026-01-03T10:00:00Z"}
	got, err := CSVRowMixed(fields, 0, 1, 2, 3, 4)
	if err != nil {
		t.Fatalf("CSVRowMixed: %v", err)
	}
	if got[5] != "15000" {
		t.Errorf("the amount was escaped: %q", got[5])
	}
	if got[6] != "2026-01-03T10:00:00Z" {
		t.Errorf("the timestamp was escaped: %q", got[6])
	}
	for i := 0; i <= 4; i++ {
		if got[i] != fields[i] {
			t.Errorf("field %d = %q, want %q", i, got[i], fields[i])
		}
	}
}

// The dangerous case: a seller names their bank so the formula lands in a finance
// operator's spreadsheet.
func TestCSVRowMixedEscapesSellerControlledBankFields(t *testing.T) {
	attack := `=HYPERLINK("http://evil.example","Approve batch RF-9")`
	got, err := CSVRowMixed([]string{
		"RF-9", "payout-1", "seller-1", attack, "9999999999", "15000000",
		"2026-01-03T10:00:00Z",
	}, 0, 1, 2, 3, 4)
	if err != nil {
		t.Fatalf("CSVRowMixed: %v", err)
	}
	if got[3] == attack {
		t.Fatal("the bank name was exported unescaped; it would execute on open")
	}
	if !HasCSVInjection(attack) {
		t.Fatal("the fixture no longer looks like an injection; the test is vacuous")
	}
}

// An index outside the row must be an ERROR, not a silent skip. Appending a column to a
// row and forgetting to extend the index list is the realistic mistake, and ignoring an
// out-of-range index would export that new field unescaped with no signal whatsoever.
func TestCSVRowMixedRefusesAnIndexOutsideTheRow(t *testing.T) {
	for _, idx := range []int{7, 99, -1} {
		_, err := CSVRowMixed([]string{"a", "b"}, 0, idx)
		if err == nil {
			t.Errorf("CSVRowMixed accepted index %d on a row of width 2", idx)
			continue
		}
		var ce *CSVRowError
		if !asCSVRowError(err, &ce) {
			t.Errorf("index %d: error is %T, want *CSVRowError", idx, err)
		}
		if ce.Width != 2 {
			t.Errorf("index %d: reported width %d, want 2", idx, ce.Width)
		}
	}
}

func asCSVRowError(err error, out **CSVRowError) bool {
	e, ok := err.(*CSVRowError)
	if ok {
		*out = e
	}
	return ok
}
