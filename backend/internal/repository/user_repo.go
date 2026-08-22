package repository

import (
	"context"
	"errors"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/vincommerce/backend/internal/db"
	"github.com/vincommerce/backend/internal/domain"
)

// UserRepository persists users.
type UserRepository struct {
	pool *db.Pool
}

// NewUserRepository creates a UserRepository.
func NewUserRepository(pool *db.Pool) *UserRepository {
	return &UserRepository{pool: pool}
}

const userColumns = `id, email, COALESCE(phone,''), full_name, roles, status,
	email_verified_at, two_factor_enabled, last_login_at, created_at, updated_at, COALESCE(avatar_url,'')`

func scanUser(row pgx.Row) (*domain.User, error) {
	var u domain.User
	err := row.Scan(&u.ID, &u.Email, &u.Phone, &u.FullName, &u.Roles, &u.Status,
		&u.EmailVerifiedAt, &u.TwoFactorEnabled, &u.LastLoginAt, &u.CreatedAt, &u.UpdatedAt, &u.AvatarURL)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, domain.ErrNotFound
	}
	if err != nil {
		return nil, err
	}
	return &u, nil
}

// Create inserts a new user. Returns ErrEmailTaken/ErrPhoneTaken on conflict.
func (r *UserRepository) Create(ctx context.Context, u *domain.User, passwordHash string) error {
	_, err := r.pool.Exec(ctx, `
		INSERT INTO users (id, email, phone, full_name, roles, status, password_hash)
		VALUES ($1, $2, NULLIF($3, ''), $4, $5, $6, $7)`,
		u.ID, u.Email, u.Phone, u.FullName, u.Roles, u.Status, passwordHash)
	if err != nil {
		return mapPgConflict(err, domain.ErrEmailTaken, domain.ErrPhoneTaken)
	}
	return nil
}

// ByID fetches a user by primary key.
func (r *UserRepository) ByID(ctx context.Context, id string) (*domain.User, error) {
	return scanUser(r.pool.QueryRow(ctx, `
		SELECT `+userColumns+` FROM users WHERE id = $1`, id))
}

// ByEmail fetches a user by email (case-insensitive).
func (r *UserRepository) ByEmail(ctx context.Context, email string) (*domain.User, error) {
	return scanUser(r.pool.QueryRow(ctx, `
		SELECT `+userColumns+` FROM users WHERE lower(email) = lower($1)`, email))
}

// ListUsers paginates all users (admin).
func (r *UserRepository) ListUsers(ctx context.Context, page, pageSize int) ([]*domain.User, int64, error) {
	if page < 1 {
		page = 1
	}
	if pageSize < 1 || pageSize > 50 {
		pageSize = 20
	}
	var total int64
	if err := r.pool.QueryRow(ctx, `SELECT COUNT(*) FROM users`).Scan(&total); err != nil {
		return nil, 0, err
	}
	rows, err := r.pool.Query(ctx, `
		SELECT `+userColumns+` FROM users ORDER BY created_at DESC LIMIT $1 OFFSET $2`,
		pageSize, (page-1)*pageSize)
	if err != nil {
		return nil, 0, err
	}
	defer rows.Close()

	users := []*domain.User{}
	for rows.Next() {
		u, err := scanUser(rows)
		if err != nil {
			return nil, 0, err
		}
		users = append(users, u)
	}
	return users, total, rows.Err()
}

// UserDetail bundles a user with account stats (admin).
type UserDetail struct {
	*domain.User
	OrderCount    int     `json:"order_count"`
	TotalSpent    float64 `json:"total_spent"`
	TicketCount   int     `json:"ticket_count"`
	ReturnCount   int     `json:"return_count"`
	WalletBalance float64 `json:"wallet_balance"`
}

// DetailWithStats loads a user plus activity stats (admin).
func (r *UserRepository) DetailWithStats(ctx context.Context, userID string) (*UserDetail, error) {
	u, err := r.ByID(ctx, userID)
	if err != nil {
		return nil, err
	}
	d := &UserDetail{User: u}
	err = r.pool.QueryRow(ctx, `
		SELECT COUNT(*)::int, COALESCE(SUM(total_amount), 0)::float8
		FROM orders WHERE buyer_id = $1 AND status NOT IN ('cancelled')`, userID).
		Scan(&d.OrderCount, &d.TotalSpent)
	if err != nil {
		return nil, err
	}
	if err := r.pool.QueryRow(ctx,
		`SELECT COUNT(*)::int FROM support_tickets WHERE user_id = $1`, userID).Scan(&d.TicketCount); err != nil {
		return nil, err
	}
	if err := r.pool.QueryRow(ctx,
		`SELECT COUNT(*)::int FROM return_requests WHERE buyer_id = $1`, userID).Scan(&d.ReturnCount); err != nil {
		return nil, err
	}
	if err := r.pool.QueryRow(ctx,
		`SELECT COALESCE(balance, 0)::float8 FROM wallets WHERE user_id = $1`, userID).Scan(&d.WalletBalance); err != nil {
		return nil, err
	}
	return d, nil
}

// ByRole lists users holding a role (limit 100).
func (r *UserRepository) ByRole(ctx context.Context, role string) ([]*domain.User, error) {
	rows, err := r.pool.Query(ctx, `
		SELECT `+userColumns+` FROM users WHERE roles @> ARRAY[$1] ORDER BY created_at LIMIT 100`, role)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	users := []*domain.User{}
	for rows.Next() {
		u, err := scanUser(rows)
		if err != nil {
			return nil, err
		}
		users = append(users, u)
	}
	return users, rows.Err()
}

// PasswordHash fetches the password hash for a user.
func (r *UserRepository) PasswordHash(ctx context.Context, userID string) (string, error) {
	var hash string
	err := r.pool.QueryRow(ctx,
		`SELECT password_hash FROM users WHERE id = $1`, userID).Scan(&hash)
	if errors.Is(err, pgx.ErrNoRows) {
		return "", domain.ErrNotFound
	}
	return hash, err
}

// SetPassword replaces the stored password hash.
func (r *UserRepository) SetPassword(ctx context.Context, userID, passwordHash string) error {
	_, err := r.pool.Exec(ctx,
		`UPDATE users SET password_hash = $2, updated_at = now() WHERE id = $1`,
		userID, passwordHash)
	return err
}

// UpdateProfile edits name, phone and avatar.
func (r *UserRepository) UpdateProfile(ctx context.Context, userID, fullName, phone, avatarURL string) error {
	tag, err := r.pool.Exec(ctx, `
		UPDATE users SET full_name = $2, phone = NULLIF($3, ''), avatar_url = NULLIF($4, ''), updated_at = now()
		WHERE id = $1`,
		userID, fullName, phone, avatarURL)
	if err != nil {
		return mapPgConflict(err, nil, domain.E(domain.KindConflict, "PHONE_TAKEN", "phone number is already registered"))
	}
	if tag.RowsAffected() == 0 {
		return domain.ErrNotFound
	}
	return nil
}

// VerifyEmail marks an email as verified.
func (r *UserRepository) VerifyEmail(ctx context.Context, userID string) error {
	_, err := r.pool.Exec(ctx,
		`UPDATE users SET email_verified_at = COALESCE(email_verified_at, now()), updated_at = now() WHERE id = $1`,
		userID)
	return err
}

// SetLastLogin updates the last login timestamp.
func (r *UserRepository) SetLastLogin(ctx context.Context, userID string, at time.Time) error {
	_, err := r.pool.Exec(ctx,
		`UPDATE users SET last_login_at = $2 WHERE id = $1`, userID, at)
	return err
}

// UpdateStatus changes account status (active/disabled/suspended).
func (r *UserRepository) UpdateStatus(ctx context.Context, userID, status string) error {
	tag, err := r.pool.Exec(ctx,
		`UPDATE users SET status = $2, updated_at = now() WHERE id = $1`, userID, status)
	if err != nil {
		return err
	}
	if tag.RowsAffected() == 0 {
		return domain.ErrNotFound
	}
	return nil
}

// AddRoles grants roles to a user.
func (r *UserRepository) AddRoles(ctx context.Context, userID string, roles []string) error {
	_, err := r.pool.Exec(ctx,
		`UPDATE users SET roles = array_append(roles, $2), updated_at = now()
		 WHERE id = $1 AND NOT roles @> ARRAY[$2]`, userID, roles)
	return err
}

// TOTPSecret loads the TOTP secret row for a user.
func (r *UserRepository) TOTPSecret(ctx context.Context, userID string) (secret string, confirmed bool, err error) {
	err = r.pool.QueryRow(ctx,
		`SELECT secret, confirmed_at IS NOT NULL FROM totp_secrets WHERE user_id = $1`,
		userID).Scan(&secret, &confirmed)
	if errors.Is(err, pgx.ErrNoRows) {
		return "", false, nil
	}
	return secret, confirmed, err
}

// SaveTOTPSecret upserts an (unconfirmed) TOTP secret.
func (r *UserRepository) SaveTOTPSecret(ctx context.Context, userID, secret string) error {
	_, err := r.pool.Exec(ctx, `
		INSERT INTO totp_secrets (user_id, secret, confirmed_at)
		VALUES ($1, $2, NULL)
		ON CONFLICT (user_id) DO UPDATE SET secret = EXCLUDED.secret, confirmed_at = NULL`,
		userID, secret)
	return err
}

// ConfirmTOTP marks the TOTP secret confirmed and enables 2FA.
func (r *UserRepository) ConfirmTOTP(ctx context.Context, userID string) error {
	tx, err := r.pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer tx.Rollback(ctx)

	if _, err := tx.Exec(ctx,
		`UPDATE totp_secrets SET confirmed_at = now() WHERE user_id = $1`, userID); err != nil {
		return err
	}
	if _, err := tx.Exec(ctx,
		`UPDATE users SET two_factor_enabled = TRUE, updated_at = now() WHERE id = $1`, userID); err != nil {
		return err
	}
	return tx.Commit(ctx)
}

// DisableTOTP removes the secret and disables 2FA.
func (r *UserRepository) DisableTOTP(ctx context.Context, userID string) error {
	tx, err := r.pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer tx.Rollback(ctx)

	if _, err := tx.Exec(ctx,
		`DELETE FROM totp_secrets WHERE user_id = $1`, userID); err != nil {
		return err
	}
	if _, err := tx.Exec(ctx,
		`UPDATE users SET two_factor_enabled = FALSE, updated_at = now() WHERE id = $1`, userID); err != nil {
		return err
	}
	return tx.Commit(ctx)
}

// BackupCodeSummary is one stored 2FA backup code (hash not exposed).
type BackupCodeSummary struct {
	CreatedAt time.Time  `json:"created_at"`
	UsedAt    *time.Time `json:"used_at,omitempty"`
}

// SaveBackupCodes replaces the user's backup codes (regenerate).
func (r *UserRepository) SaveBackupCodes(ctx context.Context, userID string, hashes []string) error {
	tx, err := r.pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer tx.Rollback(ctx)

	if _, err := tx.Exec(ctx,
		`DELETE FROM two_factor_backup_codes WHERE user_id = $1`, userID); err != nil {
		return err
	}
	for _, h := range hashes {
		if _, err := tx.Exec(ctx,
			`INSERT INTO two_factor_backup_codes (user_id, code_hash) VALUES ($1, $2)`,
			userID, h); err != nil {
			return err
		}
	}
	return tx.Commit(ctx)
}

// BackupCodes lists the user's backup codes.
func (r *UserRepository) BackupCodes(ctx context.Context, userID string) ([]*BackupCodeSummary, error) {
	rows, err := r.pool.Query(ctx, `
		SELECT created_at, used_at FROM two_factor_backup_codes
		WHERE user_id = $1 ORDER BY created_at`, userID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	items := []*BackupCodeSummary{}
	for rows.Next() {
		var b BackupCodeSummary
		if err := rows.Scan(&b.CreatedAt, &b.UsedAt); err != nil {
			return nil, err
		}
		items = append(items, &b)
	}
	return items, rows.Err()
}

// ConsumeBackupCode marks a matching unused code as used; reports whether it matched.
func (r *UserRepository) ConsumeBackupCode(ctx context.Context, userID, hash string) (bool, error) {
	tag, err := r.pool.Exec(ctx, `
		UPDATE two_factor_backup_codes SET used_at = now()
		WHERE user_id = $1 AND code_hash = $2 AND used_at IS NULL`,
		userID, hash)
	if err != nil {
		return false, err
	}
	return tag.RowsAffected() > 0, nil
}

// mapPgConflict translates unique-violation errors into domain conflicts.
func mapPgConflict(err error, emailErr, phoneErr *domain.Error) error {
	var pgErr *pgconn.PgError
	if errors.As(err, &pgErr) && pgErr.Code == "23505" {
		switch {
		case strings.Contains(pgErr.ConstraintName, "phone"):
			if phoneErr != nil {
				return phoneErr
			}
			return emailErr
		default:
			if emailErr != nil {
				return emailErr
			}
			return domain.E(domain.KindConflict, "CONFLICT", "resource already exists")
		}
	}
	return err
}
