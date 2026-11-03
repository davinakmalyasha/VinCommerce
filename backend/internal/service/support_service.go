package service

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/vincommerce/backend/internal/domain"
	"github.com/vincommerce/backend/internal/mail"
	"github.com/vincommerce/backend/internal/repository"
)

// SupportService implements help content and customer service tickets.
type SupportService struct {
	support       *repository.SupportRepository
	orders        *repository.OrderRepository
	notifications *NotificationService
	users         *repository.UserRepository
	mailer        *mail.Client
	webURL        string
	// publicBaseURL is APP_BASE_URL, used to validate that ticket attachments
	// point at a file this service uploaded. Empty is safe: then only the
	// relative `/uploads/...` form is accepted.
	publicBaseURL string
}

// NewSupportService creates a SupportService.
func NewSupportService(support *repository.SupportRepository, orders *repository.OrderRepository) *SupportService {
	return &SupportService{support: support, orders: orders}
}

// SetNotificationService notifies customers when agents reply.
func (s *SupportService) SetNotificationService(n *NotificationService) { s.notifications = n }

// SetUsers enables agent-reply emails.
func (s *SupportService) SetUsers(u *repository.UserRepository) { s.users = u }

// SetMailer enables agent-reply emails.
func (s *SupportService) SetMailer(m *mail.Client, webURL string) { s.mailer = m; s.webURL = webURL }

// SetPublicBaseURL tells the service which host its uploads live on.
func (s *SupportService) SetPublicBaseURL(baseURL string) { s.publicBaseURL = baseURL }

// --- help content ---

// Categories lists help categories.
func (s *SupportService) Categories(ctx context.Context) ([]*domain.HelpCategory, error) {
	return s.support.Categories(ctx)
}

// Articles lists published articles (public).
func (s *SupportService) Articles(ctx context.Context, section, categorySlug, search string) ([]*domain.HelpArticle, error) {
	return s.support.Articles(ctx, section, categorySlug, search)
}

// ArticleBySlug fetches a published article (public).
func (s *SupportService) ArticleBySlug(ctx context.Context, slug string) (*domain.HelpArticle, error) {
	return s.support.ArticleBySlug(ctx, slug)
}

// CreateArticle (admin).
func (s *SupportService) CreateArticle(ctx context.Context, a *domain.HelpArticle) (*domain.HelpArticle, error) {
	if strings.TrimSpace(a.Title) == "" {
		return nil, domain.E(domain.KindInvalid, "TITLE_REQUIRED", "title is required")
	}
	if strings.TrimSpace(a.Content) == "" {
		return nil, domain.E(domain.KindInvalid, "CONTENT_REQUIRED", "content is required")
	}
	if a.Section == "" {
		a.Section = domain.HelpSectionHelp
	}
	a.ID = uuid.NewString()
	a.Slug = slugify(a.Title)
	if err := s.support.CreateArticle(ctx, a); err != nil {
		return nil, err
	}
	return a, nil
}

// UpdateArticle (admin).
func (s *SupportService) UpdateArticle(ctx context.Context, articleID string, a *domain.HelpArticle) (*domain.HelpArticle, error) {
	a.ID = articleID
	if a.Slug == "" {
		a.Slug = slugify(a.Title)
	}
	if err := s.support.UpdateArticle(ctx, a); err != nil {
		return nil, err
	}
	return a, nil
}

// DeleteArticle (admin).
func (s *SupportService) DeleteArticle(ctx context.Context, articleID string) error {
	return s.support.DeleteArticle(ctx, articleID)
}

// AllArticles (admin).
func (s *SupportService) AllArticles(ctx context.Context) ([]*domain.HelpArticle, error) {
	return s.support.AllArticles(ctx)
}

// ToggleArticleHelpful records or retracts a helpful vote (authenticated).
func (s *SupportService) ToggleArticleHelpful(ctx context.Context, articleID, userID string) (bool, int, error) {
	return s.support.ToggleArticleHelpful(ctx, articleID, userID)
}

// --- tickets ---

// CreateTicketInput for opening a support ticket.
type CreateTicketInput struct {
	UserID      string
	OrderID     string
	Subject     string
	Category    string
	Priority    string
	Message     string
	Attachments []string
}

// OpenTicket creates a ticket with the first message.
func (s *SupportService) OpenTicket(ctx context.Context, in CreateTicketInput) (*domain.SupportTicket, error) {
	if strings.TrimSpace(in.Subject) == "" {
		return nil, domain.E(domain.KindInvalid, "SUBJECT_REQUIRED", "subject is required")
	}
	if strings.TrimSpace(in.Message) == "" {
		return nil, domain.E(domain.KindInvalid, "MESSAGE_REQUIRED", "message is required")
	}
	if in.OrderID != "" {
		order, err := s.orders.ByID(ctx, in.OrderID)
		if err != nil {
			return nil, err
		}
		if order.BuyerID != in.UserID {
			return nil, domain.E(domain.KindForbidden, "NOT_OWNED", "order does not belong to user")
		}
	}
	if in.Category == "" {
		in.Category = "general"
	}
	if in.Priority == "" {
		in.Priority = "normal"
	}
	switch in.Priority {
	case "low", "normal", "high", "urgent":
	default:
		return nil, domain.E(domain.KindInvalid, "BAD_PRIORITY", "invalid priority")
	}

	seq, err := s.support.NextTicketNumber(ctx)
	if err != nil {
		return nil, err
	}
	attachments, err := domain.NormalizeEvidenceURLs(in.Attachments, s.publicBaseURL)
	if err != nil {
		return nil, err
	}
	ticket := &domain.SupportTicket{
		ID:           uuid.NewString(),
		TicketNumber: fmt.Sprintf("TCK-%d", seq),
		UserID:       in.UserID,
		OrderID:      strOrNil(in.OrderID),
		Subject:      strings.TrimSpace(in.Subject),
		Category:     in.Category,
		Priority:     in.Priority,
		Status:       domain.TicketOpen,
		SlaDueAt:     slaDueFor(in.Priority, time.Now().UTC()),
	}
	if err := s.support.CreateTicket(ctx, ticket); err != nil {
		return nil, err
	}
	msg := &domain.TicketMessage{
		TicketID: ticket.ID, AuthorID: in.UserID, AuthorRole: "customer",
		Body: strings.TrimSpace(in.Message), Attachments: attachments, IsInternal: false,
	}
	if err := s.support.AddMessage(ctx, msg); err != nil {
		return nil, err
	}
	return ticket, nil
}

// slaDueFor is the first-response SLA policy: how long staff has to answer a
// new ticket, by priority. Kept as a pure function so the policy is tested by
// value; the Contact page's "1x24 jam" copy describes the normal tier.
func slaDueFor(priority string, now time.Time) *time.Time {
	var d time.Duration
	switch priority {
	case "urgent":
		d = 4 * time.Hour
	case "high":
		d = 24 * time.Hour
	case "low":
		d = 7 * 24 * time.Hour
	default:
		d = 24 * time.Hour
	}
	due := now.Add(d)
	return &due
}

// MyTickets lists the customer's tickets.
func (s *SupportService) MyTickets(ctx context.Context, userID string) ([]*domain.SupportTicket, error) {
	return s.support.TicketsByUser(ctx, userID)
}

// TicketFor loads a ticket if the user owns it (or is staff).
func (s *SupportService) TicketFor(ctx context.Context, ticketID, requesterID string, staff bool) (*domain.SupportTicket, error) {
	t, err := s.support.TicketByID(ctx, ticketID)
	if err != nil {
		return nil, err
	}
	if !staff && t.UserID != requesterID {
		return nil, domain.E(domain.KindForbidden, "NOT_OWNED", "ticket does not belong to user")
	}
	return t, nil
}

// TicketMessages returns the thread (staff sees internal notes).
func (s *SupportService) TicketMessages(ctx context.Context, ticketID string, staff bool) ([]*domain.TicketMessage, error) {
	return s.support.Messages(ctx, ticketID, staff)
}

// ReplyMessage posts a message on a ticket.
func (s *SupportService) ReplyMessage(ctx context.Context, ticketID, authorID, authorRole, body string, internal bool, attachments []string) (*domain.TicketMessage, error) {
	if strings.TrimSpace(body) == "" {
		return nil, domain.E(domain.KindInvalid, "MESSAGE_REQUIRED", "message is required")
	}
	files, err := domain.NormalizeEvidenceURLs(attachments, s.publicBaseURL)
	if err != nil {
		return nil, err
	}
	msg := &domain.TicketMessage{
		TicketID: ticketID, AuthorID: authorID, AuthorRole: authorRole,
		Body: strings.TrimSpace(body), Attachments: files, IsInternal: internal,
	}
	if err := s.support.AddMessage(ctx, msg); err != nil {
		return nil, err
	}
	if !internal {
		// customer reply reopens the ticket
		if authorRole == "customer" {
			_ = s.support.UpdateTicketStatus(ctx, ticketID, domain.TicketOpen)
		}
	}
	// Customer-facing notifications fire ONLY for visible replies — internal
	// staff notes must never notify or email the customer.
	if !internal && s.notifications != nil && authorRole == "agent" {
		t, err := s.support.TicketByID(ctx, ticketID)
		if err == nil {
			_ = s.notifications.Notify(ctx, t.UserID, "ticket", "Balasan baru di tiket "+t.TicketNumber,
				"Staf telah membalas tiket: "+t.Subject,
				map[string]any{"ticket_id": ticketID})
		}
	}
	if !internal && s.mailer != nil && s.users != nil && authorRole == "agent" {
		t, err := s.support.TicketByID(ctx, ticketID)
		if err == nil {
			if u, err := s.users.ByID(ctx, t.UserID); err == nil {
				_ = s.mailer.Send(ctx, u.Email, "Balasan baru di tiket "+t.TicketNumber, "ticket_reply", map[string]any{
					"Name":      u.FullName,
					"TicketURL": s.webURL + "/account/tickets/" + ticketID,
				})
			}
		}
	}
	return msg, nil
}

// SetTicketStatus transitions a ticket (staff).
func (s *SupportService) SetTicketStatus(ctx context.Context, ticketID, status string) error {
	switch status {
	case domain.TicketOpen, domain.TicketInProgress, domain.TicketResolved, domain.TicketClosed:
	default:
		return domain.E(domain.KindInvalid, "BAD_STATUS", "invalid ticket status")
	}
	return s.support.UpdateTicketStatus(ctx, ticketID, status)
}

// AssignTicket sets the agent (staff).
func (s *SupportService) AssignTicket(ctx context.Context, ticketID, agentID string) error {
	return s.support.AssignTicket(ctx, ticketID, agentID)
}

// RateTicket records the customer's satisfaction score.
//
// Only the ticket owner may rate, and only once staff had a chance to
// respond (resolved/closed). Rating an open ticket would score the queue
// time, not the resolution.
func (s *SupportService) RateTicket(ctx context.Context, ticketID, requesterID string, rating int) error {
	if rating < 1 || rating > 5 {
		return domain.E(domain.KindInvalid, "BAD_RATING", "rating must be 1-5")
	}
	t, err := s.support.TicketByID(ctx, ticketID)
	if err != nil {
		return err
	}
	if t.UserID != requesterID {
		return domain.E(domain.KindForbidden, "NOT_OWNED", "ticket does not belong to user")
	}
	if t.Status != domain.TicketResolved && t.Status != domain.TicketClosed {
		return domain.E(domain.KindConflict, "NOT_RESOLVED",
			"you can rate this ticket once it is resolved")
	}
	return s.support.RateTicket(ctx, ticketID, rating)
}

// TicketQueue lists open tickets (staff).
func (s *SupportService) TicketQueue(ctx context.Context, status string) ([]*domain.SupportTicket, error) {
	return s.support.TicketsByStatus(ctx, status)
}
