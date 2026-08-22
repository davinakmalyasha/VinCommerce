package domain

import "time"

// Chat sender roles.
const (
	ChatRoleCustomer = "customer"
	ChatRoleAgent    = "agent"
	ChatRoleSystem   = "system"
)

// ChatSession is a real-time support conversation.
type ChatSession struct {
	ID        string     `json:"id"`
	UserID    string     `json:"user_id"`
	AgentID   *string    `json:"agent_id,omitempty"`
	Status    string     `json:"status"`
	Source    string     `json:"source"`
	OrderID   *string    `json:"order_id,omitempty"`
	Type      string     `json:"type"`
	CreatedAt time.Time  `json:"created_at"`
	ClosedAt  *time.Time `json:"closed_at,omitempty"`

	UserName string `json:"user_name,omitempty"`
}

// ChatMessage is one chat line.
type ChatMessage struct {
	ID         int64     `json:"id"`
	SessionID  string    `json:"session_id"`
	SenderRole string    `json:"sender_role"`
	SenderID   *string   `json:"sender_id,omitempty"`
	Body       string    `json:"body"`
	CreatedAt  time.Time `json:"created_at"`

	SenderName string `json:"sender_name,omitempty"`
}
