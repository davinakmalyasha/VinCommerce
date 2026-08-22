package repository

import (
	"context"
	"errors"

	"github.com/jackc/pgx/v5"
	"github.com/vincommerce/backend/internal/db"
	"github.com/vincommerce/backend/internal/domain"
)

// AddressRepository persists shipping addresses.
type AddressRepository struct {
	pool *db.Pool
}

// NewAddressRepository creates an AddressRepository.
func NewAddressRepository(pool *db.Pool) *AddressRepository {
	return &AddressRepository{pool: pool}
}

// Create adds an address, optionally promoting it to default.
func (r *AddressRepository) Create(ctx context.Context, a *domain.Address) error {
	tx, err := r.pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer tx.Rollback(ctx)

	if a.IsDefault {
		if _, err := tx.Exec(ctx,
			`UPDATE addresses SET is_default = FALSE WHERE user_id = $1`, a.UserID); err != nil {
			return err
		}
	} else {
		var count int
		if err := tx.QueryRow(ctx,
			`SELECT COUNT(*) FROM addresses WHERE user_id = $1`, a.UserID).Scan(&count); err != nil {
			return err
		}
		if count == 0 {
			a.IsDefault = true
		}
	}
	if err := tx.QueryRow(ctx, `
		INSERT INTO addresses (id, user_id, label, recipient, phone, address_line1, address_line2,
		                       city, province, postal_code, country, is_default)
		VALUES ($1, $2, $3, $4, $5, $6, NULLIF($7, ''), $8, $9, $10, $11, $12)
		RETURNING created_at`,
		a.ID, a.UserID, a.Label, a.Recipient, a.Phone, a.AddressLine1, a.AddressLine2,
		a.City, a.Province, a.PostalCode, a.Country, a.IsDefault).Scan(&a.CreatedAt); err != nil {
		return err
	}
	return tx.Commit(ctx)
}

// List returns the user's addresses.
func (r *AddressRepository) List(ctx context.Context, userID string) ([]*domain.Address, error) {
	rows, err := r.pool.Query(ctx, `
		SELECT id, user_id, label, recipient, phone, address_line1, COALESCE(address_line2,''),
		       city, province, postal_code, country, is_default, created_at
		FROM addresses WHERE user_id = $1 ORDER BY is_default DESC, created_at`, userID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	addresses := []*domain.Address{}
	for rows.Next() {
		a, err := scanAddress(rows)
		if err != nil {
			return nil, err
		}
		addresses = append(addresses, a)
	}
	return addresses, rows.Err()
}

// Default returns the user's default address.
func (r *AddressRepository) Default(ctx context.Context, userID string) (*domain.Address, error) {
	var a domain.Address
	err := r.pool.QueryRow(ctx, `
		SELECT id, user_id, label, recipient, phone, address_line1, COALESCE(address_line2,''),
		       city, province, postal_code, country, is_default, created_at
		FROM addresses WHERE user_id = $1 ORDER BY is_default DESC, created_at LIMIT 1`, userID).
		Scan(&a.ID, &a.UserID, &a.Label, &a.Recipient, &a.Phone, &a.AddressLine1, &a.AddressLine2,
			&a.City, &a.Province, &a.PostalCode, &a.Country, &a.IsDefault, &a.CreatedAt)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, domain.E(domain.KindNotFound, "NO_ADDRESS", "no shipping address on file")
	}
	return &a, err
}

// ByID fetches an address by id.
func (r *AddressRepository) ByID(ctx context.Context, addressID string) (*domain.Address, error) {
	var a domain.Address
	err := r.pool.QueryRow(ctx, `
		SELECT id, user_id, label, recipient, phone, address_line1, COALESCE(address_line2,''),
		       city, province, postal_code, country, is_default, created_at
		FROM addresses WHERE id = $1`, addressID).
		Scan(&a.ID, &a.UserID, &a.Label, &a.Recipient, &a.Phone, &a.AddressLine1, &a.AddressLine2,
			&a.City, &a.Province, &a.PostalCode, &a.Country, &a.IsDefault, &a.CreatedAt)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, domain.ErrNotFound
	}
	return &a, err
}

// Update edits an owned address.
func (r *AddressRepository) Update(ctx context.Context, userID string, a *domain.Address) error {
	tx, err := r.pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer tx.Rollback(ctx)

	if a.IsDefault {
		if _, err := tx.Exec(ctx,
			`UPDATE addresses SET is_default = FALSE WHERE user_id = $1`, userID); err != nil {
			return err
		}
	}
	tag, err := tx.Exec(ctx, `
		UPDATE addresses SET label = $3, recipient = $4, phone = $5, address_line1 = $6,
			address_line2 = NULLIF($7, ''), city = $8, province = $9, postal_code = $10,
			country = $11, is_default = $12, updated_at = now()
		WHERE id = $1 AND user_id = $2`,
		a.ID, userID, a.Label, a.Recipient, a.Phone, a.AddressLine1, a.AddressLine2,
		a.City, a.Province, a.PostalCode, a.Country, a.IsDefault)
	if err != nil {
		return err
	}
	if tag.RowsAffected() == 0 {
		return domain.E(domain.KindNotFound, "ADDRESS_NOT_FOUND", "address not found")
	}
	return tx.Commit(ctx)
}

// Delete removes an address if owned by the user.
func (r *AddressRepository) Delete(ctx context.Context, userID, addressID string) error {
	tag, err := r.pool.Exec(ctx,
		`DELETE FROM addresses WHERE id = $1 AND user_id = $2`, addressID, userID)
	if err != nil {
		return err
	}
	if tag.RowsAffected() == 0 {
		return domain.E(domain.KindNotFound, "ADDRESS_NOT_FOUND", "address not found")
	}
	return nil
}

type addressRow interface {
	Scan(dest ...any) error
}

func scanAddress(row addressRow) (*domain.Address, error) {
	var a domain.Address
	err := row.Scan(&a.ID, &a.UserID, &a.Label, &a.Recipient, &a.Phone, &a.AddressLine1, &a.AddressLine2,
		&a.City, &a.Province, &a.PostalCode, &a.Country, &a.IsDefault, &a.CreatedAt)
	return &a, err
}
