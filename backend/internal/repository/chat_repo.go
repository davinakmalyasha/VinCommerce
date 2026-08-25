package repository

import (
	"context"
	"errors"

	"github.com/jackc/pgx/v5"
	"github.com/vincommerce/backend/internal/db"
	"github.com/vincommerce/backend/internal/domain"
)

// ChatRepository persists chat sessions and messages.
type ChatRepository struct {
	pool *db.Pool
}

// NewChatRepository creates a ChatRepository.
func NewChatRepository(pool *db.Pool) *ChatRepository {
	return &ChatRepository{pool: pool}
}

// CreateSession opens a chat session.
func (r *ChatRepository) CreateSession(ctx context.Context, s *domain.ChatSession) error {
	err := r.pool.QueryRow(ctx, `
		INSERT INTO chat_sessions (id, user_id, status, source, order_id, type, agent_id)
		VALUES ($1, $2, 'open', $3, NULLIF($4, '')::uuid, $5, NULLIF($6, '')::uuid)
		RETURNING created_at`,
		s.ID, s.UserID, s.Source, s.OrderID, s.Type, s.AgentID).Scan(&s.CreatedAt)
	return err
}

// SessionByOrder finds the open seller chat for an order, scoped to a
// participant (the buyer who owns the session or the agent assigned to it).
func (r *ChatRepository) SessionByOrder(ctx context.Context, orderID, userID string) (*domain.ChatSession, error) {
	var s domain.ChatSession
	err := r.pool.QueryRow(ctx, `
		SELECT s.id, s.user_id, s.agent_id, s.status, s.source, s.created_at, s.closed_at, u.full_name
		FROM chat_sessions s
		JOIN users u ON u.id = s.user_id
		WHERE s.order_id = $1 AND s.type = 'seller' AND s.status = 'open'
		  AND ($2::uuid IS NULL OR s.user_id = $2::uuid OR s.agent_id = $2::uuid)
		ORDER BY s.created_at DESC LIMIT 1`, orderID, userID).
		Scan(&s.ID, &s.UserID, &s.AgentID, &s.Status, &s.Source, &s.CreatedAt, &s.ClosedAt, &s.UserName)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, domain.ErrNotFound
	}
	return &s, err
}

// AddMessage appends a chat message.
func (r *ChatRepository) AddMessage(ctx context.Context, m *domain.ChatMessage) error {
	return r.pool.QueryRow(ctx, `
		INSERT INTO chat_messages (session_id, sender_role, sender_id, body)
		VALUES ($1, $2, NULLIF($3, '')::uuid, $4)
		RETURNING created_at`,
		m.SessionID, m.SenderRole, m.SenderID, m.Body).Scan(&m.CreatedAt)
}

// SessionByID loads a session with the user name.
func (r *ChatRepository) SessionByID(ctx context.Context, sessionID string) (*domain.ChatSession, error) {
	var s domain.ChatSession
	err := r.pool.QueryRow(ctx, `
		SELECT s.id, s.user_id, s.agent_id, s.status, s.source, s.created_at, s.closed_at, u.full_name
		FROM chat_sessions s
		JOIN users u ON u.id = s.user_id
		WHERE s.id = $1`, sessionID).
		Scan(&s.ID, &s.UserID, &s.AgentID, &s.Status, &s.Source, &s.CreatedAt, &s.ClosedAt, &s.UserName)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, domain.ErrNotFound
	}
	return &s, err
}

// SessionsForUser lists a customer's sessions.
func (r *ChatRepository) SessionsForUser(ctx context.Context, userID string) ([]*domain.ChatSession, error) {
	rows, err := r.pool.Query(ctx, `
		SELECT s.id, s.user_id, s.agent_id, s.status, s.source, s.created_at, s.closed_at, u.full_name
		FROM chat_sessions s
		JOIN users u ON u.id = s.user_id
		WHERE s.user_id = $1 ORDER BY s.created_at DESC`, userID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	return scanSessions(rows)
}

// OpenSessions lists the unclaimed/claimed open queue (staff).
func (r *ChatRepository) OpenSessions(ctx context.Context) ([]*domain.ChatSession, error) {
	rows, err := r.pool.Query(ctx, `
		SELECT s.id, s.user_id, s.agent_id, s.status, s.source, s.created_at, s.closed_at, u.full_name
		FROM chat_sessions s
		JOIN users u ON u.id = s.user_id
		WHERE s.status = 'open' ORDER BY s.created_at`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	return scanSessions(rows)
}

// Messages lists a session's history.
func (r *ChatRepository) Messages(ctx context.Context, sessionID string) ([]*domain.ChatMessage, error) {
	rows, err := r.pool.Query(ctx, `
		SELECT m.id, m.session_id, m.sender_role, m.sender_id, m.body, m.created_at, COALESCE(u.full_name, '')
		FROM chat_messages m
		LEFT JOIN users u ON u.id = m.sender_id
		WHERE m.session_id = $1 ORDER BY m.created_at`, sessionID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	items := []*domain.ChatMessage{}
	for rows.Next() {
		var m domain.ChatMessage
		if err := rows.Scan(&m.ID, &m.SessionID, &m.SenderRole, &m.SenderID, &m.Body, &m.CreatedAt, &m.SenderName); err != nil {
			return nil, err
		}
		items = append(items, &m)
	}
	return items, rows.Err()
}

// Claim assigns an agent to a session (idempotent).
func (r *ChatRepository) Claim(ctx context.Context, sessionID, agentID string) error {
	_, err := r.pool.Exec(ctx, `
		UPDATE chat_sessions SET agent_id = $2 WHERE id = $1 AND status = 'open'`,
		sessionID, agentID)
	return err
}

// Close closes a session.
func (r *ChatRepository) Close(ctx context.Context, sessionID string) error {
	tag, err := r.pool.Exec(ctx, `
		UPDATE chat_sessions SET status = 'closed', closed_at = now() WHERE id = $1 AND status = 'open'`,
		sessionID)
	if err != nil {
		return err
	}
	if tag.RowsAffected() == 0 {
		return domain.E(domain.KindConflict, "ALREADY_CLOSED", "session is already closed")
	}
	return nil
}

type sessionRow interface {
	Scan(dest ...any) error
}

func scanSessions(rows pgx.Rows) ([]*domain.ChatSession, error) {
	items := []*domain.ChatSession{}
	for rows.Next() {
		var s domain.ChatSession
		if err := rows.Scan(&s.ID, &s.UserID, &s.AgentID, &s.Status, &s.Source, &s.CreatedAt, &s.ClosedAt, &s.UserName); err != nil {
			return nil, err
		}
		items = append(items, &s)
	}
	return items, rows.Err()
}
