package service

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/google/uuid"
	"github.com/vincommerce/backend/internal/domain"
	"github.com/vincommerce/backend/internal/repository"
	"github.com/vincommerce/backend/internal/stream"
)

// ChatService implements real-time support chat.
type ChatService struct {
	chat   *repository.ChatRepository
	orders *repository.OrderRepository
	broker *stream.Broker
}

// NewChatService creates a ChatService.
func NewChatService(chat *repository.ChatRepository, orders *repository.OrderRepository, broker *stream.Broker) *ChatService {
	return &ChatService{chat: chat, broker: broker}
}

// OpenSellerChat starts an order-scoped buyer-seller conversation.
func (s *ChatService) OpenSellerChat(ctx context.Context, orderID, userID string) (*domain.ChatSession, error) {
	order, err := s.orders.ByID(ctx, orderID)
	if err != nil {
		return nil, err
	}
	if order.BuyerID != userID && order.SellerID != userID {
		return nil, domain.E(domain.KindForbidden, "NOT_OWNED", "order does not belong to user")
	}
	sess := &domain.ChatSession{
		ID: uuid.NewString(), UserID: order.BuyerID, Status: "open",
		Source: "widget", OrderID: &order.ID, Type: "seller", AgentID: &order.SellerID,
	}
	if err := s.chat.CreateSession(ctx, sess); err != nil {
		return nil, err
	}
	return sess, nil
}

// SessionForOrder returns the active seller chat for an order, creating none.
func (s *ChatService) SessionForOrder(ctx context.Context, orderID, userID string) (*domain.ChatSession, error) {
	return s.chat.SessionByOrder(ctx, orderID, userID)
}

func channelFor(sessionID string) string { return "chat:" + sessionID }

// OpenSession starts a chat for a customer.
func (s *ChatService) OpenSession(ctx context.Context, userID string, source string) (*domain.ChatSession, error) {
	if source == "" {
		source = "widget"
	}
	sess := &domain.ChatSession{ID: uuid.NewString(), UserID: userID, Status: "open", Source: source}
	if err := s.chat.CreateSession(ctx, sess); err != nil {
		return nil, err
	}
	sys := &domain.ChatMessage{
		SessionID: sess.ID, SenderRole: domain.ChatRoleSystem, Body: "Selamat datang di dukungan VinCommerce! Seorang agen akan segera bergabung.",
	}
	_ = s.chat.AddMessage(ctx, sys)
	return sess, nil
}

// MySessions lists the customer's sessions.
func (s *ChatService) MySessions(ctx context.Context, userID string) ([]*domain.ChatSession, error) {
	return s.chat.SessionsForUser(ctx, userID)
}

// Queue lists open sessions (staff).
func (s *ChatService) Queue(ctx context.Context) ([]*domain.ChatSession, error) {
	return s.chat.OpenSessions(ctx)
}

// Session loads a session if the user participates in it (customer or
// assigned agent) or is staff.
func (s *ChatService) Session(ctx context.Context, sessionID, requesterID string, staff bool) (*domain.ChatSession, error) {
	sess, err := s.chat.SessionByID(ctx, sessionID)
	if err != nil {
		return nil, err
	}
	isParticipant := sess.UserID == requesterID ||
		(sess.AgentID != nil && *sess.AgentID == requesterID)
	if !staff && !isParticipant {
		return nil, domain.E(domain.KindForbidden, "NOT_OWNED", "session does not belong to user")
	}
	return sess, nil
}

// Messages returns the session history.
func (s *ChatService) Messages(ctx context.Context, sessionID string) ([]*domain.ChatMessage, error) {
	return s.chat.Messages(ctx, sessionID)
}

// SendMessage persists a message and publishes it in realtime.
func (s *ChatService) SendMessage(ctx context.Context, sessionID, senderID, role, body string) (*domain.ChatMessage, error) {
	if len([]rune(body)) == 0 {
		return nil, domain.E(domain.KindInvalid, "MESSAGE_REQUIRED", "message is required")
	}
	const maxLen = 2000
	if len([]rune(body)) > maxLen {
		return nil, domain.E(domain.KindInvalid, "MESSAGE_TOO_LONG",
			"message must be at most "+fmt.Sprintf("%d", maxLen)+" characters")
	}
	msg := &domain.ChatMessage{
		SessionID: sessionID, SenderRole: role, SenderID: &senderID, Body: body,
	}
	if err := s.chat.AddMessage(ctx, msg); err != nil {
		return nil, err
	}
	payload, _ := json.Marshal(msg)
	_ = s.broker.PublishChannel(ctx, channelFor(sessionID), payload)
	return msg, nil
}

// Claim assigns an agent (staff).
func (s *ChatService) Claim(ctx context.Context, sessionID, agentID string) error {
	return s.chat.Claim(ctx, sessionID, agentID)
}

// Close ends a session.
func (s *ChatService) Close(ctx context.Context, sessionID string) error {
	return s.chat.Close(ctx, sessionID)
}
