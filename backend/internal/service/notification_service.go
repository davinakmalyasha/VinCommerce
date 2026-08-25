package service

import (
	"context"

	"github.com/vincommerce/backend/internal/domain"
	"github.com/vincommerce/backend/internal/repository"
)

// NotificationService persists and serves in-app notifications.
type NotificationService struct {
	repo *repository.NotificationRepository
}

// NewNotificationService creates a NotificationService.
func NewNotificationService(repo *repository.NotificationRepository) *NotificationService {
	return &NotificationService{repo: repo}
}

// Notify stores a notification for a user, honoring in-app preferences.
func (s *NotificationService) Notify(ctx context.Context, userID, ntype, title, body string, data map[string]any) error {
	if !s.repo.InAppEnabled(ctx, userID, ntype) {
		return nil // user muted this category in-app
	}
	if data == nil {
		data = map[string]any{}
	}
	return s.repo.Create(ctx, &domain.Notification{
		UserID: userID, Type: ntype, Title: title, Body: body, Data: data,
	})
}

// NotifyMany fans out one notification to many users in TWO queries total:
// a single pref-exclusion lookup and a single INSERT..SELECT FROM unnest.
// Replaces per-recipient loops (follower fan-out, alert sweeps) that cost
// 2 round trips per user.
func (s *NotificationService) NotifyMany(ctx context.Context, userIDs []string, ntype, title, body string, data map[string]any, excluded map[string]bool) (int, error) {
	if len(userIDs) == 0 {
		return 0, nil
	}
	eligible := make([]string, 0, len(userIDs))
	for _, id := range userIDs {
		if excluded[id] {
			continue
		}
		if !s.repo.InAppEnabled(ctx, id, ntype) {
			continue
		}
		eligible = append(eligible, id)
	}
	if len(eligible) == 0 {
		return 0, nil
	}
	if err := s.repo.CreateMany(ctx, eligible, ntype, title, body, data); err != nil {
		return 0, err
	}
	return len(eligible), nil
}

// List returns recent notifications, optionally filtered by type.
func (s *NotificationService) List(ctx context.Context, userID string, limit int, ntype string) ([]*domain.Notification, error) {
	return s.repo.List(ctx, userID, limit, ntype)
}

// Preferences lists the user's delivery preferences (with defaults).
func (s *NotificationService) Preferences(ctx context.Context, userID string) ([]*repository.NotificationPref, error) {
	return s.repo.Preferences(ctx, userID)
}

// SetPreference updates one category preference.
func (s *NotificationService) SetPreference(ctx context.Context, userID, category string, inApp, email bool) error {
	return s.repo.SetPreference(ctx, userID, category, inApp, email)
}

// UnreadCount returns the unread badge number.
func (s *NotificationService) UnreadCount(ctx context.Context, userID string) (int, error) {
	return s.repo.UnreadCount(ctx, userID)
}

// MarkRead marks notifications read.
func (s *NotificationService) MarkRead(ctx context.Context, userID string, id int64) error {
	return s.repo.MarkRead(ctx, userID, id)
}
