package handler

import (
	"net/http"
	"strconv"

	"github.com/go-chi/chi/v5"
	"github.com/vincommerce/backend/internal/domain"
	"github.com/vincommerce/backend/internal/httpapi/middleware"
	"github.com/vincommerce/backend/internal/service"
)

// Support exposes help content (public) and ticket endpoints (authenticated).
type Support struct {
	svc *service.SupportService
}

// NewSupport creates a Support handler.
func NewSupport(svc *service.SupportService) *Support {
	return &Support{svc: svc}
}

// Categories handles GET /help/categories.
func (h *Support) Categories(w http.ResponseWriter, r *http.Request) {
	items, err := h.svc.Categories(r.Context())
	if err != nil {
		writeErr(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"categories": items})
}

// Articles handles GET /help/articles?section=&category=&q=.
func (h *Support) Articles(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()
	items, err := h.svc.Articles(r.Context(), q.Get("section"), q.Get("category"), q.Get("q"))
	if err != nil {
		writeErr(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"articles": items})
}

// Article handles GET /help/articles/{slug}.
func (h *Support) Article(w http.ResponseWriter, r *http.Request) {
	a, err := h.svc.ArticleBySlug(r.Context(), chi.URLParam(r, "slug"))
	if err != nil {
		writeErr(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"article": a})
}

// CreateArticle handles POST /admin/articles.
func (h *Support) CreateArticle(w http.ResponseWriter, r *http.Request) {
	var req struct {
		CategoryID  *string `json:"category_id,omitempty"`
		Title       string  `json:"title"`
		Excerpt     string  `json:"excerpt,omitempty"`
		Content     string  `json:"content"`
		Section     string  `json:"section"`
		IsPublished bool    `json:"is_published"`
	}
	if err := decode(r, &req); err != nil {
		writeErr(w, r, domain.E(domain.KindInvalid, "BAD_JSON", err.Error()))
		return
	}
	a, err := h.svc.CreateArticle(r.Context(), &domain.HelpArticle{
		CategoryID: req.CategoryID, Title: req.Title, Excerpt: req.Excerpt,
		Content: req.Content, Section: req.Section, IsPublished: req.IsPublished,
	})
	if err != nil {
		writeErr(w, r, err)
		return
	}
	writeJSON(w, http.StatusCreated, map[string]any{"article": a})
}

// UpdateArticle handles PUT /admin/articles/{id}.
func (h *Support) UpdateArticle(w http.ResponseWriter, r *http.Request) {
	var req struct {
		CategoryID  *string `json:"category_id,omitempty"`
		Title       string  `json:"title"`
		Excerpt     string  `json:"excerpt,omitempty"`
		Content     string  `json:"content"`
		Section     string  `json:"section"`
		IsPublished bool    `json:"is_published"`
	}
	if err := decode(r, &req); err != nil {
		writeErr(w, r, domain.E(domain.KindInvalid, "BAD_JSON", err.Error()))
		return
	}
	a, err := h.svc.UpdateArticle(r.Context(), chi.URLParam(r, "id"), &domain.HelpArticle{
		CategoryID: req.CategoryID, Title: req.Title, Excerpt: req.Excerpt,
		Content: req.Content, Section: req.Section, IsPublished: req.IsPublished,
	})
	if err != nil {
		writeErr(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"article": a})
}

// DeleteArticle handles DELETE /admin/articles/{id}.
func (h *Support) DeleteArticle(w http.ResponseWriter, r *http.Request) {
	if err := h.svc.DeleteArticle(r.Context(), chi.URLParam(r, "id")); err != nil {
		writeErr(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]bool{"deleted": true})
}

// AllArticles handles GET /admin/articles.
func (h *Support) AllArticles(w http.ResponseWriter, r *http.Request) {
	items, err := h.svc.AllArticles(r.Context())
	if err != nil {
		writeErr(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"articles": items})
}

// --- tickets ---

type ticketCreateRequest struct {
	OrderID  string `json:"order_id,omitempty"`
	Subject  string `json:"subject"`
	Category string `json:"category"`
	Priority string `json:"priority"`
	Message  string `json:"message"`
}

// OpenTicket handles POST /tickets.
func (h *Support) OpenTicket(w http.ResponseWriter, r *http.Request) {
	user := middleware.UserFrom(r.Context())
	var req ticketCreateRequest
	if err := decode(r, &req); err != nil {
		writeErr(w, r, domain.E(domain.KindInvalid, "BAD_JSON", err.Error()))
		return
	}
	t, err := h.svc.OpenTicket(r.Context(), service.CreateTicketInput{
		UserID: user.ID, OrderID: req.OrderID, Subject: req.Subject,
		Category: req.Category, Priority: req.Priority, Message: req.Message,
	})
	if err != nil {
		writeErr(w, r, err)
		return
	}
	writeJSON(w, http.StatusCreated, map[string]any{"ticket": t})
}

// MyTickets handles GET /tickets.
func (h *Support) MyTickets(w http.ResponseWriter, r *http.Request) {
	user := middleware.UserFrom(r.Context())
	items, err := h.svc.MyTickets(r.Context(), user.ID)
	if err != nil {
		writeErr(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"tickets": items})
}

// TicketDetail handles GET /tickets/{id}.
func (h *Support) TicketDetail(w http.ResponseWriter, r *http.Request) {
	user := middleware.UserFrom(r.Context())
	staff := user.HasRole(domain.RoleSupport) || user.HasRole(domain.RoleAdmin)
	t, err := h.svc.TicketFor(r.Context(), chi.URLParam(r, "id"), user.ID, staff)
	if err != nil {
		writeErr(w, r, err)
		return
	}
	messages, err := h.svc.TicketMessages(r.Context(), t.ID, staff)
	if err != nil {
		writeErr(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"ticket": t, "messages": messages})
}

type ticketMessageRequest struct {
	Body     string `json:"body"`
	Internal bool   `json:"internal,omitempty"`
}

// Reply handles POST /tickets/{id}/messages.
func (h *Support) Reply(w http.ResponseWriter, r *http.Request) {
	user := middleware.UserFrom(r.Context())
	staff := user.HasRole(domain.RoleSupport) || user.HasRole(domain.RoleAdmin)
	t, err := h.svc.TicketFor(r.Context(), chi.URLParam(r, "id"), user.ID, staff)
	if err != nil {
		writeErr(w, r, err)
		return
	}
	var req ticketMessageRequest
	if err := decode(r, &req); err != nil {
		writeErr(w, r, domain.E(domain.KindInvalid, "BAD_JSON", err.Error()))
		return
	}
	if req.Internal && !staff {
		writeErr(w, r, domain.E(domain.KindForbidden, "FORBIDDEN", "internal notes are staff-only"))
		return
	}
	role := "customer"
	if staff {
		role = "agent"
	}
	msg, err := h.svc.ReplyMessage(r.Context(), t.ID, user.ID, role, req.Body, req.Internal)
	if err != nil {
		writeErr(w, r, err)
		return
	}
	writeJSON(w, http.StatusCreated, map[string]any{"message": msg})
}

// Queue handles GET /support/tickets?status=.
func (h *Support) Queue(w http.ResponseWriter, r *http.Request) {
	items, err := h.svc.TicketQueue(r.Context(), r.URL.Query().Get("status"))
	if err != nil {
		writeErr(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"tickets": items})
}

type ticketStatusRequest struct {
	Status string `json:"status"`
}

// SetStatus handles POST /support/tickets/{id}/status.
func (h *Support) SetStatus(w http.ResponseWriter, r *http.Request) {
	var req ticketStatusRequest
	if err := decode(r, &req); err != nil {
		writeErr(w, r, domain.E(domain.KindInvalid, "BAD_JSON", err.Error()))
		return
	}
	if err := h.svc.SetTicketStatus(r.Context(), chi.URLParam(r, "id"), req.Status); err != nil {
		writeErr(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]bool{"updated": true})
}

// Assign handles POST /support/tickets/{id}/assign.
func (h *Support) Assign(w http.ResponseWriter, r *http.Request) {
	var req struct {
		AgentID string `json:"agent_id"`
	}
	if err := decode(r, &req); err != nil {
		writeErr(w, r, domain.E(domain.KindInvalid, "BAD_JSON", err.Error()))
		return
	}
	if err := h.svc.AssignTicket(r.Context(), chi.URLParam(r, "id"), req.AgentID); err != nil {
		writeErr(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]bool{"assigned": true})
}

// Notifications exposes the persisted notification feed.
type Notifications struct {
	svc *service.NotificationService
}

// NewNotifications creates a Notifications handler.
func NewNotifications(svc *service.NotificationService) *Notifications {
	return &Notifications{svc: svc}
}

// List handles GET /notifications.
func (h *Notifications) List(w http.ResponseWriter, r *http.Request) {
	user := middleware.UserFrom(r.Context())
	items, err := h.svc.List(r.Context(), user.ID, intQuery(r.URL.Query().Get("limit"), 20), r.URL.Query().Get("type"))
	if err != nil {
		writeErr(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"notifications": items})
}

// Preferences handles GET /notifications/preferences.
func (h *Notifications) Preferences(w http.ResponseWriter, r *http.Request) {
	user := middleware.UserFrom(r.Context())
	prefs, err := h.svc.Preferences(r.Context(), user.ID)
	if err != nil {
		writeErr(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"preferences": prefs})
}

// SetPreference handles PUT /notifications/preferences {category,in_app,email}.
func (h *Notifications) SetPreference(w http.ResponseWriter, r *http.Request) {
	user := middleware.UserFrom(r.Context())
	var req struct {
		Category string `json:"category"`
		InApp    *bool  `json:"in_app"`
		Email    *bool  `json:"email"`
	}
	if err := decode(r, &req); err != nil {
		writeErr(w, r, domain.E(domain.KindInvalid, "BAD_JSON", err.Error()))
		return
	}
	prefs, err := h.svc.Preferences(r.Context(), user.ID)
	if err != nil {
		writeErr(w, r, err)
		return
	}
	var inApp, email = true, false
	for _, p := range prefs {
		if p.Category == req.Category {
			inApp, email = p.InApp, p.Email
			break
		}
	}
	if req.InApp != nil {
		inApp = *req.InApp
	}
	if req.Email != nil {
		email = *req.Email
	}
	if err := h.svc.SetPreference(r.Context(), user.ID, req.Category, inApp, email); err != nil {
		writeErr(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]bool{"updated": true})
}

// Unread handles GET /notifications/unread-count.
func (h *Notifications) Unread(w http.ResponseWriter, r *http.Request) {
	user := middleware.UserFrom(r.Context())
	count, err := h.svc.UnreadCount(r.Context(), user.ID)
	if err != nil {
		writeErr(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"unread": count})
}

// MarkRead handles POST /notifications/read{?id=}.
func (h *Notifications) MarkRead(w http.ResponseWriter, r *http.Request) {
	user := middleware.UserFrom(r.Context())
	id, _ := strconv.ParseInt(r.URL.Query().Get("id"), 10, 64)
	if err := h.svc.MarkRead(r.Context(), user.ID, id); err != nil {
		writeErr(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]bool{"read": true})
}
