package domain

import "time"

// Help article sections.
const (
	HelpSectionHelp  = "help"
	HelpSectionDocs  = "docs"
	HelpSectionLegal = "legal"
)

// Ticket statuses.
const (
	TicketOpen       = "open"
	TicketInProgress = "in_progress"
	TicketResolved   = "resolved"
	TicketClosed     = "closed"
)

// HelpCategory groups help articles.
type HelpCategory struct {
	ID       string `json:"id"`
	Name     string `json:"name"`
	Slug     string `json:"slug"`
	Position int    `json:"position"`
	IsActive bool   `json:"is_active"`
}

// HelpArticle is a knowledge-base entry (help / docs / legal).
type HelpArticle struct {
	ID          string     `json:"id"`
	CategoryID  *string    `json:"category_id,omitempty"`
	Title       string     `json:"title"`
	Slug        string     `json:"slug"`
	Excerpt     string     `json:"excerpt"`
	Content     string     `json:"content"`
	Section     string     `json:"section"`
	IsPublished bool       `json:"is_published"`
	ViewCount   int        `json:"view_count"`
	PublishedAt *time.Time `json:"published_at,omitempty"`
	CreatedAt   time.Time  `json:"created_at"`
	UpdatedAt   time.Time  `json:"updated_at"`

	Category *HelpCategory `json:"category,omitempty"`
}

// SupportTicket is a customer service request.
type SupportTicket struct {
	ID           string     `json:"id"`
	TicketNumber string     `json:"ticket_number"`
	UserID       string     `json:"user_id"`
	OrderID      *string    `json:"order_id,omitempty"`
	Subject      string     `json:"subject"`
	Category     string     `json:"category"`
	Priority     string     `json:"priority"`
	Status       string     `json:"status"`
	AssignedTo   *string    `json:"assigned_to,omitempty"`
	CreatedAt    time.Time  `json:"created_at"`
	UpdatedAt    time.Time  `json:"updated_at"`
	ResolvedAt   *time.Time `json:"resolved_at,omitempty"`

	LastMessage string `json:"last_message,omitempty"`
	UnreadStaff bool   `json:"unread_staff,omitempty"`
}

// TicketMessage is one reply in a ticket thread.
type TicketMessage struct {
	ID         int64     `json:"id"`
	TicketID   string    `json:"ticket_id"`
	AuthorID   string    `json:"author_id"`
	AuthorRole string    `json:"author_role"`
	Body       string    `json:"body"`
	IsInternal bool      `json:"is_internal"`
	CreatedAt  time.Time `json:"created_at"`

	AuthorName string `json:"author_name,omitempty"`
}

// Notification is a persisted in-app alert.
type Notification struct {
	ID        int64          `json:"id"`
	UserID    string         `json:"user_id"`
	Type      string         `json:"type"`
	Title     string         `json:"title"`
	Body      string         `json:"body"`
	Data      map[string]any `json:"data,omitempty"`
	ReadAt    *time.Time     `json:"read_at,omitempty"`
	CreatedAt time.Time      `json:"created_at"`
}

// LoyaltyEntry is one points ledger line.
type LoyaltyEntry struct {
	ID        int64     `json:"id"`
	UserID    string    `json:"user_id"`
	Change    int       `json:"change"`
	Reason    string    `json:"reason"`
	RefID     string    `json:"ref_id,omitempty"`
	CreatedAt time.Time `json:"created_at"`
}

// Dispute is an escalated conflict between buyer and seller.
type Dispute struct {
	ID          string     `json:"id"`
	OrderID     string     `json:"order_id"`
	ReturnID    *string    `json:"return_id,omitempty"`
	UserID      string     `json:"user_id"`
	SellerID    string     `json:"seller_id"`
	Subject     string     `json:"subject"`
	Description string     `json:"description"`
	Status      string     `json:"status"`
	Decision    string     `json:"decision,omitempty"`
	AdminNote   string     `json:"admin_note,omitempty"`
	CreatedAt   time.Time  `json:"created_at"`
	ResolvedAt  *time.Time `json:"resolved_at,omitempty"`

	BuyerName  string `json:"buyer_name,omitempty"`
	SellerName string `json:"seller_name,omitempty"`
}
