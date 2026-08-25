package repository

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/vincommerce/backend/internal/db"
	"github.com/vincommerce/backend/internal/domain"
)

// NotificationRepository persists in-app notifications.
type NotificationRepository struct {
	pool *db.Pool
}

// NewNotificationRepository creates a NotificationRepository.
func NewNotificationRepository(pool *db.Pool) *NotificationRepository {
	return &NotificationRepository{pool: pool}
}

// NotificationPref is a per-category delivery preference.
type NotificationPref struct {
	Category string `json:"category"`
	InApp    bool   `json:"in_app"`
	Email    bool   `json:"email"`
}

var prefCategories = []string{
	"order", "payment", "price_alert", "back_in_stock", "store_new_product", "low_stock", "marketing",
	// Drift fix: these types were emitted by Notify() but missing from the
	// category list, making them impossible to mute or see in the prefs UI.
	"cart_recovery", "moderation", "ticket",
}

// Preferences lists the user's prefs, materializing defaults for unset categories.
func (r *NotificationRepository) Preferences(ctx context.Context, userID string) ([]*NotificationPref, error) {
	rows, err := r.pool.Query(ctx,
		`SELECT category, in_app, email FROM notification_prefs WHERE user_id = $1`, userID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	set := map[string]*NotificationPref{}
	for rows.Next() {
		var p NotificationPref
		if err := rows.Scan(&p.Category, &p.InApp, &p.Email); err != nil {
			return nil, err
		}
		set[p.Category] = &p
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	out := make([]*NotificationPref, 0, len(prefCategories))
	for _, cat := range prefCategories {
		if p, ok := set[cat]; ok {
			out = append(out, p)
		} else {
			out = append(out, &NotificationPref{Category: cat, InApp: true, Email: false})
		}
	}
	return out, nil
}

// SetPreference upserts one category preference.
func (r *NotificationRepository) SetPreference(ctx context.Context, userID, category string, inApp, email bool) error {
	valid := false
	for _, c := range prefCategories {
		if c == category {
			valid = true
			break
		}
	}
	if !valid {
		return domain.E(domain.KindInvalid, "BAD_CATEGORY", "unknown notification category")
	}
	_, err := r.pool.Exec(ctx, `
		INSERT INTO notification_prefs (user_id, category, in_app, email) VALUES ($1, $2, $3, $4)
		ON CONFLICT (user_id, category) DO UPDATE SET in_app = EXCLUDED.in_app, email = EXCLUDED.email`,
		userID, category, inApp, email)
	return err
}

// InAppEnabled reports whether a category is allowed in-app (default true).
func (r *NotificationRepository) InAppEnabled(ctx context.Context, userID, category string) bool {
	var on bool
	err := r.pool.QueryRow(ctx,
		`SELECT in_app FROM notification_prefs WHERE user_id = $1 AND category = $2`,
		userID, category).Scan(&on)
	if err != nil {
		return true // default allow when unset or on error
	}
	return on
}

// EmailEnabled reports whether promo/optional email for a category is
// allowed. NOTE: defaults to FALSE (matching the prefs UI default) — only
// genuinely transactional mail (password reset, verification, order/payment
// status) should bypass this check entirely.
func (r *NotificationRepository) EmailEnabled(ctx context.Context, userID, category string) bool {
	var on bool
	err := r.pool.QueryRow(ctx,
		`SELECT email FROM notification_prefs WHERE user_id = $1 AND category = $2`,
		userID, category).Scan(&on)
	if err != nil {
		return false // opt-out by default for optional mail
	}
	return on
}

// Create stores a notification for a user.
func (r *NotificationRepository) Create(ctx context.Context, n *domain.Notification) error {
	_, err := r.pool.Exec(ctx, `
		INSERT INTO notifications (user_id, type, title, body, data)
		VALUES ($1, $2, $3, $4, $5)`,
		n.UserID, n.Type, n.Title, n.Body, n.Data)
	return err
}

// CreateMany inserts one notification per user in a single statement.
func (r *NotificationRepository) CreateMany(ctx context.Context, userIDs []string, ntype, title, body string, data map[string]any) error {
	if len(userIDs) == 0 {
		return nil
	}
	if data == nil {
		data = map[string]any{}
	}
	payload, err := json.Marshal(data)
	if err != nil {
		return err
	}
	_, err = r.pool.Exec(ctx, `
		INSERT INTO notifications (user_id, type, title, body, data)
		SELECT uid, $2, $3, $4, $5::jsonb FROM unnest($1::uuid[]) AS uid`,
		userIDs, ntype, title, body, string(payload))
	return err
}

// List returns the user's recent notifications.
func (r *NotificationRepository) List(ctx context.Context, userID string, limit int, ntype string) ([]*domain.Notification, error) {
	if limit < 1 || limit > 50 {
		limit = 20
	}
	where := `WHERE user_id = $1`
	args := []any{userID}
	if ntype != "" {
		args = append(args, ntype)
		where += fmt.Sprintf(` AND type = $%d`, len(args))
	}
	args = append(args, limit)
	rows, err := r.pool.Query(ctx, `
		SELECT id, user_id, type, title, body, data, read_at, created_at
		FROM notifications `+where+` ORDER BY created_at DESC LIMIT $`+itoa(len(args)),
		args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	items := []*domain.Notification{}
	for rows.Next() {
		var n domain.Notification
		var data []byte
		if err := rows.Scan(&n.ID, &n.UserID, &n.Type, &n.Title, &n.Body, &data, &n.ReadAt, &n.CreatedAt); err != nil {
			return nil, err
		}
		if len(data) > 0 {
			_ = json.Unmarshal(data, &n.Data)
		}
		items = append(items, &n)
	}
	return items, rows.Err()
}

// UnreadCount counts unread notifications.
func (r *NotificationRepository) UnreadCount(ctx context.Context, userID string) (int, error) {
	var n int
	err := r.pool.QueryRow(ctx,
		`SELECT COUNT(*) FROM notifications WHERE user_id = $1 AND read_at IS NULL`, userID).Scan(&n)
	return n, err
}

// MarkRead marks notifications read (optionally a single id).
func (r *NotificationRepository) MarkRead(ctx context.Context, userID string, id int64) error {
	if id > 0 {
		_, err := r.pool.Exec(ctx, `
			UPDATE notifications SET read_at = now()
			WHERE user_id = $1 AND id = $2 AND read_at IS NULL`, userID, id)
		return err
	}
	_, err := r.pool.Exec(ctx, `
		UPDATE notifications SET read_at = now()
		WHERE user_id = $1 AND read_at IS NULL`, userID)
	return err
}
