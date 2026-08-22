package repository

import (
	"context"
	"errors"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/vincommerce/backend/internal/db"
)

// FeatureFlag is a toggleable capability.
type FeatureFlag struct {
	ID          string    `json:"id"`
	Key         string    `json:"key"`
	Description string    `json:"description"`
	Enabled     bool      `json:"enabled"`
	UpdatedAt   time.Time `json:"updated_at"`
}

// FeatureFlagRepository persists feature flags.
type FeatureFlagRepository struct {
	pool *db.Pool
}

// NewFeatureFlagRepository creates a FeatureFlagRepository.
func NewFeatureFlagRepository(pool *db.Pool) *FeatureFlagRepository {
	return &FeatureFlagRepository{pool: pool}
}

// List returns all flags.
func (r *FeatureFlagRepository) List(ctx context.Context) ([]*FeatureFlag, error) {
	rows, err := r.pool.Query(ctx, `
		SELECT id, key, description, enabled, updated_at FROM feature_flags ORDER BY key`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	items := []*FeatureFlag{}
	for rows.Next() {
		var f FeatureFlag
		if err := rows.Scan(&f.ID, &f.Key, &f.Description, &f.Enabled, &f.UpdatedAt); err != nil {
			return nil, err
		}
		items = append(items, &f)
	}
	return items, rows.Err()
}

// IsEnabled checks a flag, treating missing flags as disabled.
func (r *FeatureFlagRepository) IsEnabled(ctx context.Context, key string) (bool, error) {
	var enabled bool
	err := r.pool.QueryRow(ctx,
		`SELECT enabled FROM feature_flags WHERE key = $1`, key).Scan(&enabled)
	if errors.Is(err, pgx.ErrNoRows) {
		return false, nil
	}
	return enabled, err
}

// SetEnabled flips a flag (upsert).
func (r *FeatureFlagRepository) SetEnabled(ctx context.Context, key string, enabled bool) error {
	_, err := r.pool.Exec(ctx, `
		INSERT INTO feature_flags (id, key, enabled) VALUES (gen_random_uuid(), $1, $2)
		ON CONFLICT (key) DO UPDATE SET enabled = EXCLUDED.enabled, updated_at = now()`,
		key, enabled)
	return err
}
