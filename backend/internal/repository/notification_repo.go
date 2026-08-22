package repository

import (
	"context"
	"encoding/json"

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

// Create stores a notification for a user.
func (r *NotificationRepository) Create(ctx context.Context, n *domain.Notification) error {
	_, err := r.pool.Exec(ctx, `
		INSERT INTO notifications (user_id, type, title, body, data)
		VALUES ($1, $2, $3, $4, $5)`,
		n.UserID, n.Type, n.Title, n.Body, n.Data)
	return err
}

// List returns the user's recent notifications.
func (r *NotificationRepository) List(ctx context.Context, userID string, limit int) ([]*domain.Notification, error) {
	if limit < 1 || limit > 50 {
		limit = 20
	}
	rows, err := r.pool.Query(ctx, `
		SELECT id, user_id, type, title, body, data, read_at, created_at
		FROM notifications WHERE user_id = $1 ORDER BY created_at DESC LIMIT $2`,
		userID, limit)
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
