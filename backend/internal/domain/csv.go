package domain

import "strings"

// CSVCell makes a string safe to write into a CSV that a spreadsheet will open.
//
// WHY THIS EXISTS AND WHY IT IS IN `domain`
//
// `encoding/csv` quotes fields containing commas and quotes. It does nothing about
// formulas. A cell whose text begins with `=`, `+`, `-`, `@`, TAB or CR is executed by
// Excel, LibreOffice and Google Sheets when the file is opened -- so a seller who names
// their store `=HYPERLINK("http://evil.example","click me")` gets code running on the
// machine of whoever opens the export.
//
// The highest-value instance is the payout REMITTANCE file, because its reader is a
// finance operator or a bank, not a seller. That writer lives in the service package
// while the escaper used to live unexported in the handler package, so the file with
// the most dangerous recipient was the one writer with no escaping at all.
//
// This lives in `domain` because it is pure value logic with no I/O and no storage
// concern, and both `service` and `httpapi/handler` already depend on `domain`. Putting
// it in either of those instead would mean the other imports across a layer boundary,
// or duplicating it -- and a duplicated escaper is one that eventually diverges and
// silently stops protecting the export it was written for.
//
// The apostrophe prefix is the spreadsheet convention for "treat this as text". It is
// removed on display, so the value a human reads is unchanged.
func CSVCell(s string) string {
	if s == "" {
		return s
	}
	switch s[0] {
	case '=', '+', '-', '@', '\t', '\r':
		return "'" + s
	}
	return s
}

// CSVRow applies CSVCell to every field.
//
// Used where a row is assembled from a struct in one expression. It is deliberately not
// used for numeric columns: formatting an amount with FormatFloat cannot produce a
// leading `=`, and running a monetary value through a text escaper would be a category
// error that hides a future bug rather than preventing one.
func CSVRow(fields ...string) []string {
	out := make([]string, len(fields))
	for i, f := range fields {
		out[i] = CSVCell(f)
	}
	return out
}

// CSVRowMixed escapes only the fields that are text, leaving already-formatted
// numerics and timestamps alone by index.
//
// A row like `{batch_ref, seller_id, bank_name, amount, requested_at}` reads better
// with explicit indices than with a second parallel slice, and an index out of range is
// a panic rather than a silent mis-escape, so the mistake is loud.
//
// Indices beyond the supplied slice are an error rather than being ignored: appending a
// column to a row and forgetting to extend the index list would otherwise export a
// raw, unescaped field with no signal at all.
func CSVRowMixed(fields []string, textIndices ...int) ([]string, error) {
	out := make([]string, len(fields))
	copy(out, fields)
	for _, i := range textIndices {
		if i < 0 || i >= len(fields) {
			return nil, &CSVRowError{Index: i, Width: len(fields)}
		}
		out[i] = CSVCell(fields[i])
	}
	return out, nil
}

// CSVRowError reports a text-column index that does not exist in the row.
type CSVRowError struct {
	Index int
	Width int
}

func (e *CSVRowError) Error() string {
	return "csv row: text column index " + itoa(e.Index) +
		" is outside a row of width " + itoa(e.Width) +
		" -- a column was added without extending the escape list, so a field would be " +
		"exported unescaped"
}

// itoa avoids importing strconv for two call sites in an error path.
func itoa(n int) string {
	if n == 0 {
		return "0"
	}
	neg := n < 0
	if neg {
		n = -n
	}
	var b [20]byte
	i := len(b)
	for n > 0 {
		i--
		b[i] = byte('0' + n%10)
		n /= 10
	}
	if neg {
		i--
		b[i] = '-'
	}
	return string(b[i:])
}

// CSVInjectionPrefixes are the leading characters that make a spreadsheet treat a cell
// as a formula. Exported so a test can assert the escaper covers all of them and that
// adding one here is a deliberate act.
//
// Kept as data rather than inlined in the switch so the set can be asserted against
// rather than assumed, which is how a list like this silently rots.
var CSVInjectionPrefixes = []string{"=", "+", "-", "@", "\t", "\r"}

// HasCSVInjection reports whether a value would be interpreted as a formula.
//
// Exported because the guard belongs at the boundary that accepts the value -- a
// validator should be able to reject a seller naming their bank `=...` rather than
// relying on every future CSV writer remembering to escape.
func HasCSVInjection(s string) bool {
	if s == "" {
		return false
	}
	// Only the FIRST byte matters to a spreadsheet. A leading space defeats detection
	// but also defeats execution in every spreadsheet tested, so it is deliberately not
	// treated as an injection: prefixing it would corrupt legitimate values.
	return strings.ContainsRune("=+-@\t\r", rune(s[0]))
}
