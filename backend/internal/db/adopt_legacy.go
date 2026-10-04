package db

import (
	"context"
	"database/sql"
	"fmt"
	"log/slog"
	"regexp"
	"strconv"
)

// legacyVersionPrefix extracts the numeric prefix from a legacy migration filename.
//
// The legacy tracker stored whole filenames, e.g. `0031_index_hot.sql`. That file is
// not in this repository -- the tree here has `00031_insurance.sql` -- so the legacy
// history and the file set are not the same list. The numeric prefix is the only part
// both agree on, and it is the part goose needs.
var legacyVersionPrefix = regexp.MustCompile(`^0*(\d+)`)

// adoptLegacyMigrationHistory seeds goose's version table from this project's earlier,
// hand-rolled tracker.
//
// # THE PROBLEM
//
// `schema_migrations` recorded versions as filenames up to `0031_index_hot.sql`. goose
// keeps its own `goose_db_version`, and a fresh goose table starts at version 0. So
// `Migrate()` against an already-migrated database replays 00001 and dies with
//
//	ERROR: relation "users" already exists (42P07)
//
// and the application never boots. Re-running changes nothing, because the failing
// migration's transaction rolls back and writes no version row.
//
// # THE FIX, AND WHY IT IS GUARDED RATHER THAN TRUSTED
//
// Seed the highest legacy version as applied, then let goose continue from there.
//
// This is the dangerous kind of magic -- seeding a version table from a different
// source can silently skip migrations that were never applied. So it only runs when:
//
//   - `schema_migrations` exists (there IS a legacy history to adopt), and
//   - goose's table is absent or empty (goose is not already authoritative).
//
// If both are populated the legacy tracker is ignored, because once goose has run the
// two can legitimately disagree and goose is the one that will be maintained.
//
// It also REQUIRES that the schema actually looks like version N was applied, and
// refuses to seed on a mismatch. A version number in a table is a claim; the objects
// on disk are the evidence. Seeding 31 onto an empty database would make goose skip 31
// migrations and then fail much later with a confusing "relation does not exist".
//
// Legacy `0031_index_hot.sql` is not present in this tree, so the objects that prove
// version 31 are the ones 0031 is expected to have added -- `orders.insurance_fee`.
// If a database genuinely lacks it, seeding is refused and the operator is told what
// to do rather than being handed a database in an undefined state.
func adoptLegacyMigrationHistory(ctx context.Context, sqlDB *sql.DB, logger *slog.Logger) error {
	var legacyExists bool
	if err := sqlDB.QueryRowContext(ctx,
		`SELECT EXISTS (SELECT 1 FROM information_schema.tables
		                WHERE table_schema = current_schema() AND table_name = 'schema_migrations')`,
	).Scan(&legacyExists); err != nil {
		return fmt.Errorf("check for legacy schema_migrations: %w", err)
	}
	if !legacyExists {
		return nil
	}

	var gooseCount int
	if err := sqlDB.QueryRowContext(ctx,
		`SELECT count(*) FROM information_schema.tables
		  WHERE table_schema = current_schema() AND table_name = 'goose_db_version'`,
	).Scan(&gooseCount); err != nil {
		return fmt.Errorf("check for goose_db_version: %w", err)
	}
	if gooseCount == 0 {
		// goose creates its own table on the first run. If it does not exist, there is
		// nothing to seed INTO and no legacy state to protect -- this is either a fresh
		// database or one whose legacy table was never populated. Either way goose
		// applies everything, which is correct.
		return nil
	}

	var appliedRows int
	if err := sqlDB.QueryRowContext(ctx, `SELECT count(*) FROM goose_db_version`).Scan(&appliedRows); err != nil {
		// The table exists but is not queryable in the expected shape. Let goose fail
		// loudly on its own rather than guessing.
		return fmt.Errorf("read goose_db_version: %w", err)
	}
	if appliedRows > 0 {
		logger.Info("legacy schema_migrations present but goose is already authoritative; not adopting")
		return nil
	}

	var legacyMax string
	if err := sqlDB.QueryRowContext(ctx, `SELECT max(version) FROM schema_migrations`).Scan(&legacyMax); err != nil {
		return fmt.Errorf("read legacy schema_migrations: %w", err)
	}
	if legacyMax == "" {
		return nil
	}
	m := legacyVersionPrefix.FindStringSubmatch(legacyMax)
	if m == nil {
		return fmt.Errorf(
			"legacy schema_migrations holds %q, which has no numeric prefix, so the "+
				"applied version cannot be determined; refusing to guess. Migrate this "+
				"database manually, or rename the row to a zero-padded numeric prefix",
			legacyMax)
	}
	version, err := strconv.Atoi(m[1])
	if err != nil {
		return fmt.Errorf("parse legacy version %q: %w", legacyMax, err)
	}

	// The evidence check. A version row is a claim; these objects are the proof.
	var insuranceCol int
	if err := sqlDB.QueryRowContext(ctx,
		`SELECT count(*) FROM information_schema.columns
		  WHERE table_schema = current_schema() AND table_name = 'orders'
		    AND column_name = 'insurance_fee'`,
	).Scan(&insuranceCol); err != nil {
		return fmt.Errorf("verify orders.insurance_fee: %w", err)
	}
	if insuranceCol == 0 {
		return fmt.Errorf(
			"legacy schema_migrations claims version %d is applied, but orders.insurance_fee "+
				"does not exist -- so the schema does not match its own history. Seeding "+
				"goose to %d would skip every migration and fail later with a misleading "+
				"'relation does not exist'. Resolve the history first", version, version)
	}

	if _, err := sqlDB.ExecContext(ctx,
		`INSERT INTO goose_db_version (id, version_id, is_applied)
		 VALUES (1, $1, true) ON CONFLICT DO NOTHING`, version,
	); err != nil {
		return fmt.Errorf("seed goose_db_version: %w", err)
	}

	logger.Warn("adopted legacy schema_migrations into goose; VERIFY THE RESULT",
		"legacy_table", "schema_migrations",
		"legacy_version", legacyMax,
		"seeded_goose_version", version,
		"note", "the two histories are independent; goose now starts from "+
			strconv.Itoa(version+1)+". The legacy file 0031_index_hot.sql is not in this "+
			"tree, so the numeric prefix is the only part both histories agree on.")
	return nil
}
