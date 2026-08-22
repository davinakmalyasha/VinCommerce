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

// Notify stores a notification for a user.
func (s *NotificationService) Notify(ctx context.Context, userID, ntype, title, body string, data map[string]any) error {
	if data == nil {
		data = map[string]any{}
	}
	return s.repo.Create(ctx, &domain.Notification{
		UserID: userID, Type: ntype, Title: title, Body: body, Data: data,
	})
}

// List returns recent notifications.
func (s *NotificationService) List(ctx context.Context, userID string, limit int) ([]*domain.Notification, error) {
	return s.repo.List(ctx, userID, limit)
}

// UnreadCount returns the unread badge number.
func (s *NotificationService) UnreadCount(ctx context.Context, userID string) (int, error) {
	return s.repo.UnreadCount(ctx, userID)
}

// MarkRead marks notifications read.
func (s *NotificationService) MarkRead(ctx context.Context, userID string, id int64) error {
	return s.repo.MarkRead(ctx, userID, id)
}
