package repository

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/vincommerce/backend/internal/db"
	"github.com/vincommerce/backend/internal/domain"
)

// SessionRepository persists refresh token sessions.
type SessionRepository struct {
	pool *db.Pool
}

// NewSessionRepository creates a SessionRepository.
func NewSessionRepository(pool *db.Pool) *SessionRepository {
	return &SessionRepository{pool: pool}
}

// Create stores a new session.
func (r *SessionRepository) Create(ctx context.Context, s *domain.RefreshSession) error {
	_, err := r.pool.Exec(ctx, `
		INSERT INTO refresh_sessions (id, user_id, refresh_hash, family_id, device_name, ip_address, user_agent, expires_at)
		VALUES ($1, $2, $3, $4, NULLIF($5, ''), NULLIF($6, '')::inet, $7, $8)`,
		s.ID, s.UserID, s.RefreshHash, s.FamilyID, s.DeviceName, s.IPAddress, s.UserAgent, s.ExpiresAt)
	return err
}

// ByHash finds an active session by its token hash.
func (r *SessionRepository) ByHash(ctx context.Context, hash string) (*domain.RefreshSession, error) {
	var s domain.RefreshSession
	var ip *string
	err := r.pool.QueryRow(ctx, `
		SELECT id, user_id, family_id, COALESCE(device_name,''), ip_address::text, COALESCE(user_agent,''),
		       expires_at, revoked_at, last_used_at, created_at
		FROM refresh_sessions
		WHERE refresh_hash = $1 AND revoked_at IS NULL AND expires_at > now()`,
		hash).Scan(&s.ID, &s.UserID, &s.FamilyID, &s.DeviceName, &ip, &s.UserAgent,
		&s.ExpiresAt, &s.RevokedAt, &s.LastUsedAt, &s.CreatedAt)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, domain.ErrSessionRevoked
	}
	if ip != nil {
		s.IPAddress = *ip
	}
	if err != nil {
		return nil, err
	}
	return &s, nil
}

// ByID finds a session row regardless of state.
func (r *SessionRepository) ByID(ctx context.Context, sessionID string) (*domain.RefreshSession, error) {
	var s domain.RefreshSession
	var ip *string
	err := r.pool.QueryRow(ctx, `
		SELECT id, user_id, family_id, COALESCE(device_name,''), ip_address::text, COALESCE(user_agent,''),
		       expires_at, revoked_at, last_used_at, created_at
		FROM refresh_sessions
		WHERE id = $1`,
		sessionID).Scan(&s.ID, &s.UserID, &s.FamilyID, &s.DeviceName, &ip, &s.UserAgent,
		&s.ExpiresAt, &s.RevokedAt, &s.LastUsedAt, &s.CreatedAt)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, domain.ErrNotFound
	}
	if ip != nil {
		s.IPAddress = *ip
	}
	if err != nil {
		return nil, err
	}
	return &s, nil
}

// Rotate replaces a session's refresh hash with a new one (rotation).
// The superseded hash is retained for reuse detection.
func (r *SessionRepository) Rotate(ctx context.Context, sessionID, oldHash, newHash string) error {
	_, err := r.pool.Exec(ctx, `
		UPDATE refresh_sessions
		SET refresh_hash = $2, prev_hash = $3, last_used_at = now()
		WHERE id = $1 AND refresh_hash = $3`,
		sessionID, newHash, oldHash)
	return err
}

// ActiveFamilyForPrevHash finds the live session whose previous token hash
// matches — i.e. the caller replayed a token that was already rotated away,
// which indicates theft or duplication.
func (r *SessionRepository) ActiveFamilyForPrevHash(ctx context.Context, hash string) (*domain.RefreshSession, error) {
	var s domain.RefreshSession
	var ip *string
	err := r.pool.QueryRow(ctx, `
		SELECT id, user_id, family_id, COALESCE(device_name,''), ip_address::text, COALESCE(user_agent,''),
		       expires_at, revoked_at, last_used_at, created_at
		FROM refresh_sessions
		WHERE prev_hash = $1 AND revoked_at IS NULL AND expires_at > now()`,
		hash).Scan(&s.ID, &s.UserID, &s.FamilyID, &s.DeviceName, &ip, &s.UserAgent,
		&s.ExpiresAt, &s.RevokedAt, &s.LastUsedAt, &s.CreatedAt)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, domain.ErrNotFound
	}
	if ip != nil {
		s.IPAddress = *ip
	}
	if err != nil {
		return nil, err
	}
	return &s, nil
}

// Revoke marks a session revoked.
func (r *SessionRepository) Revoke(ctx context.Context, sessionID string) error {
	_, err := r.pool.Exec(ctx,
		`UPDATE refresh_sessions SET revoked_at = now() WHERE id = $1`, sessionID)
	return err
}

// RevokeAllForUser revokes every session for a user.
func (r *SessionRepository) RevokeAllForUser(ctx context.Context, userID string) error {
	_, err := r.pool.Exec(ctx,
		`UPDATE refresh_sessions SET revoked_at = now() WHERE user_id = $1 AND revoked_at IS NULL`,
		userID)
	return err
}

// RevokeFamily revokes all sessions sharing a family (reuse detection).
func (r *SessionRepository) RevokeFamily(ctx context.Context, familyID string) error {
	_, err := r.pool.Exec(ctx,
		`UPDATE refresh_sessions SET revoked_at = now() WHERE family_id = $1 AND revoked_at IS NULL`,
		familyID)
	return err
}

// ListForUser returns active sessions for the sessions management UI.
func (r *SessionRepository) ListForUser(ctx context.Context, userID string) ([]*domain.RefreshSession, error) {
	rows, err := r.pool.Query(ctx, `
		SELECT id, user_id, family_id, COALESCE(device_name,''), COALESCE(ip_address::text,''), COALESCE(user_agent,''),
		       expires_at, revoked_at, last_used_at, created_at
		FROM refresh_sessions
		WHERE user_id = $1 AND revoked_at IS NULL
		ORDER BY last_used_at DESC`, userID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	sessions := []*domain.RefreshSession{}
	for rows.Next() {
		var s domain.RefreshSession
		var ip *string
		if err := rows.Scan(&s.ID, &s.UserID, &s.FamilyID, &s.DeviceName, &ip, &s.UserAgent,
			&s.ExpiresAt, &s.RevokedAt, &s.LastUsedAt, &s.CreatedAt); err != nil {
			return nil, err
		}
		if ip != nil {
			s.IPAddress = *ip
		}
		sessions = append(sessions, &s)
	}
	return sessions, rows.Err()
}

// PurgeExpired deletes long-expired sessions (housekeeping).
func (r *SessionRepository) PurgeExpired(ctx context.Context, before time.Time) (int64, error) {
	tag, err := r.pool.Exec(ctx,
		`DELETE FROM refresh_sessions WHERE expires_at < $1`, before)
	if err != nil {
		return 0, err
	}
	return tag.RowsAffected(), nil
}

// EmailToken stores a verification/reset token.
func (r *SessionRepository) SaveEmailToken(ctx context.Context, tokenHash, userID, purpose string, expiresAt time.Time) error {
	_, err := r.pool.Exec(ctx, `
		INSERT INTO email_tokens (token_hash, user_id, purpose, expires_at)
		VALUES ($1, $2, $3, $4)`, tokenHash, userID, purpose, expiresAt)
	return err
}

// ConsumeEmailToken validates and consumes a token atomically.
func (r *SessionRepository) ConsumeEmailToken(ctx context.Context, tokenHash, purpose string) (string, error) {
	var userID string
	err := r.pool.QueryRow(ctx, `
		UPDATE email_tokens
		SET used_at = now()
		WHERE token_hash = $1 AND purpose = $2 AND used_at IS NULL AND expires_at > now()
		RETURNING user_id`,
		tokenHash, purpose).Scan(&userID)
	if errors.Is(err, pgx.ErrNoRows) {
		return "", domain.ErrTokenExpired
	}
	return userID, err
}

// Audit inserts an audit entry.
func (r *SessionRepository) Audit(ctx context.Context, e *domain.AuditEntry) error {
	if e.Metadata == nil {
		e.Metadata = map[string]any{}
	}
	_, err := r.pool.Exec(ctx, `
		INSERT INTO audit_log (actor_id, action, entity_type, entity_id, ip_address, user_agent, metadata)
		VALUES ($1, $2, NULLIF($3, ''), NULLIF($4, ''), NULLIF($5, '')::inet, $6, $7)`,
		e.ActorID, e.Action, e.EntityType, e.EntityID, e.IPAddress, e.UserAgent, e.Metadata)
	return err
}

// AuditEntryRow is one audit log record.
type AuditEntryRow struct {
	ID         int64          `json:"id"`
	ActorID    *string        `json:"actor_id,omitempty"`
	ActorName  string         `json:"actor_name,omitempty"`
	Action     string         `json:"action"`
	EntityType string         `json:"entity_type,omitempty"`
	EntityID   string         `json:"entity_id,omitempty"`
	IPAddress  string         `json:"ip_address,omitempty"`
	Metadata   map[string]any `json:"metadata,omitempty"`
	CreatedAt  time.Time      `json:"created_at"`
}

// AuditLog lists audit entries (admin).
func (r *SessionRepository) AuditLog(ctx context.Context, actorID, action string, limit int) ([]*AuditEntryRow, error) {
	if limit < 1 || limit > 100 {
		limit = 50
	}
	where := `1=1`
	args := []any{}
	if actorID != "" {
		args = append(args, actorID)
		where += fmt.Sprintf(` AND a.actor_id = $%d`, len(args))
	}
	if action != "" {
		args = append(args, "%"+action+"%")
		where += fmt.Sprintf(` AND a.action ILIKE $%d`, len(args))
	}
	args = append(args, limit)
	rows, err := r.pool.Query(ctx, `
		SELECT a.id, a.actor_id, COALESCE(u.full_name, ''), a.action, COALESCE(a.entity_type,''),
		       COALESCE(a.entity_id,''), COALESCE(a.ip_address::text,''), a.metadata, a.created_at
		FROM audit_log a
		LEFT JOIN users u ON u.id = a.actor_id
		WHERE `+where+`
		ORDER BY a.created_at DESC
		LIMIT $`+itoa(len(args)), args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	items := []*AuditEntryRow{}
	for rows.Next() {
		var e AuditEntryRow
		var meta []byte
		if err := rows.Scan(&e.ID, &e.ActorID, &e.ActorName, &e.Action, &e.EntityType, &e.EntityID,
			&e.IPAddress, &meta, &e.CreatedAt); err != nil {
			return nil, err
		}
		if len(meta) > 0 {
			_ = json.Unmarshal(meta, &e.Metadata)
		}
		items = append(items, &e)
	}
	return items, rows.Err()
}
