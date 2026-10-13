package repository

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func readFileRel(t *testing.T, path string) string {
	t.Helper()
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read %s: %v", path, err)
	}
	return string(raw)
}

func createIntentBody(t *testing.T) string {
	t.Helper()
	src := orderRepoSource(t) // ensures the package dir is the working directory
	_ = src
	pay := readFileRel(t, "payment_repo.go")
	at := strings.Index(pay, "func (r *PaymentRepository) CreateIntentTx(")
	if at < 0 {
		t.Fatal("CreateIntentTx is gone; update this test")
	}
	rest := pay[at+1:]
	if end := strings.Index(rest, "\nfunc "); end > 0 {
		return pay[at : at+1+end]
	}
	return pay[at:]
}

// THE COLUMN EXISTS BECAUSE `NOT NULL DEFAULT 0` CANNOT DISTINGUISH TWO THINGS.
//
// An intent created before migration 00042 carries (0, 0) because the columns were
// ADDED with that default. An intent whose rate card genuinely was 0% carries
// (0, 0) because that is what the rate was. Treating (0, 0) as "absent" is a guess,
// and it produces a real defect: an order priced while the active rate was 0% is
// charged the NEW rate if an admin raises it before delivery.
//
// `commission_snapshot_present` removes the guess. The writer must set it exactly
// when it actually writes a rate -- and NOT when the rate-card lookup failed,
// because marking present over an unwritten (0, 0) would make the release path
// honour "unknown" as 0% and stop charging commission entirely.
func TestCreateIntentMarksTheSnapshotPresentOnlyWhenARateWasWritten(t *testing.T) {
	body := createIntentBody(t)

	if !strings.Contains(body, "in.CommissionSnapshotPresent = true") {
		t.Errorf("CreateIntentTx never marks the snapshot present.\n%s\n"+
			"Without it every intent reads as snapshot-less and a 0%% rate is "+
			"indistinguishable from a missing one, which is the defect 00052 exists "+
			"to close", body)
	}
	// The assignment must be inside the `err == nil && cfg != nil` success branch.
	cfgAt := strings.Index(body, "err == nil && cfg != nil")
	markAt := strings.Index(body, "in.CommissionSnapshotPresent = true")
	if cfgAt < 0 {
		t.Fatal("the rate-card lookup guard is gone; update this test")
	}
	branchEnd := strings.Index(body[cfgAt:], "\n\t\t}")
	if branchEnd < 0 {
		t.Fatal("could not bound the lookup branch")
	}
	successBranch := body[cfgAt : cfgAt+branchEnd]
	if !strings.Contains(successBranch, "in.CommissionSnapshotPresent = true") {
		t.Errorf("the snapshot is not marked present inside the lookup's success branch "+
			"(mark at %d, guard at %d).\n%s\n"+
			"Marking it outside the branch would mark it even when the lookup failed, "+
			"so an unwritten (0, 0) would be honoured as a real 0%% rate and commission "+
			"would never be charged on those intents", markAt, cfgAt, body)
	}
	// And a caller-supplied rate counts as a snapshot too.
	if !strings.Contains(body, "} else {\n\t\tin.CommissionSnapshotPresent = true") {
		t.Errorf("a caller-supplied rate does not mark the snapshot present.\n%s\n"+
			"The else branch is exactly the 'caller already knows the rate' case", body)
	}
}

// The column has to survive the round trip or the service never sees it.
func TestTheSnapshotFlagIsProjectedAndScanned(t *testing.T) {
	pay := readFileRel(t, "payment_repo.go")

	if !strings.Contains(pay, "commission_snapshot_present,") {
		t.Error("commission_snapshot_present is not in the shared intent projection, " +
			"so every reader sees the zero value")
	}
	if !strings.Contains(pay, "&in.CommissionSnapshotPresent") {
		t.Error("scanIntent does not read commission_snapshot_present into the domain field")
	}
	if !strings.Contains(pay, "in.CommissionSnapshotPresent)") {
		t.Error("CreateIntentTx does not bind commission_snapshot_present")
	}
}

// The migration must not backfill the ambiguous rows.
//
// A non-zero rate could only have been written on purpose, so those rows are safe
// to mark. A (0, 0) row is either a genuine 0% rate or an intent predating 00042,
// and telling them apart needs information that does not exist -- so they keep the
// active-rate fallback rather than getting a guess baked into the schema.
func TestTheMigrationBackfillsOnlyUnambiguousRows(t *testing.T) {
	sql := readFileRel(t, filepath.Join("..", "db", "migrations", "00052_commission_snapshot_present.sql"))

	if !strings.Contains(sql, "ADD COLUMN IF NOT EXISTS commission_snapshot_present boolean NOT NULL DEFAULT false") {
		t.Errorf("the migration does not add the column as NOT NULL DEFAULT false.\n" +
			"A default of true would mark every pre-existing row as having a snapshot, " +
			"which is the guess this column exists to avoid")
	}
	stripped := stripSQLComments(sql)
	if !strings.Contains(stripped, "UPDATE payment_intents") {
		t.Fatal("the migration has no backfill; update this test")
	}
	updateAt := strings.Index(stripped, "UPDATE payment_intents")
	updateEnd := strings.Index(stripped[updateAt:], ";")
	update := stripped[updateAt : updateAt+updateEnd]

	if !strings.Contains(update, "commission_rate_pct <> 0 OR commission_rate_fixed <> 0") {
		t.Errorf("the backfill marks rows without requiring a non-zero rate.\n%s\n"+
			"That would flip the ambiguous (0, 0) rows to 'present' and stop charging "+
			"commission on every intent that predates 00042", update)
	}
	if !strings.Contains(update, "WHERE") {
		t.Errorf("the backfill has no WHERE clause, so it marks every row.\n%s", update)
	}
}

// stripSQLComments removes `--` line comments so an assertion cannot be satisfied
// by prose. This is the mistake mutation checking caught earlier in the series: a
// check that matched a function's own documentation.
func stripSQLComments(sql string) string {
	var out []string
	for _, line := range strings.Split(sql, "\n") {
		if i := strings.Index(line, "--"); i >= 0 {
			line = line[:i]
		}
		out = append(out, line)
	}
	return strings.Join(out, "\n")
}
