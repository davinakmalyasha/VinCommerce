package repository

import (
	"context"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
)

// Querier is the minimal surface needed to run a statement on either the
// connection pool or an open transaction. It exists so a repository method
// can be written once and then called from inside a caller's transaction (or
// standalone), which is what makes "claim the row AND move the money in one
// atomic unit" expressible without duplicating every method.
//
// Both *db.Pool (via the embedded *pgxpool.Pool) and pgx.Tx satisfy it.
type Querier interface {
	Exec(ctx context.Context, sql string, args ...any) (pgconn.CommandTag, error)
	Query(ctx context.Context, sql string, args ...any) (pgx.Rows, error)
	QueryRow(ctx context.Context, sql string, args ...any) pgx.Row
}

var (
	_ Querier = (pgx.Tx)(nil)
)
