package handler

import (
	"net/http"

	"github.com/go-chi/chi/v5"
	"github.com/vincommerce/backend/internal/ai"
	"github.com/vincommerce/backend/internal/domain"
	"github.com/vincommerce/backend/internal/httpapi/middleware"
	"github.com/vincommerce/backend/internal/service"
)

// Chat exposes real-time support chat (customer + staff).
type Chat struct {
	svc *service.ChatService
}

// NewChat creates a Chat handler.
func NewChat(svc *service.ChatService) *Chat {
	return &Chat{svc: svc}
}

// Open handles POST /chat/sessions.
func (h *Chat) Open(w http.ResponseWriter, r *http.Request) {
	user := middleware.UserFrom(r.Context())
	var req struct {
		Source string `json:"source,omitempty"`
	}
	_ = decode(r, &req)
	sess, err := h.svc.OpenSession(r.Context(), user.ID, req.Source)
	if err != nil {
		writeErr(w, r, err)
		return
	}
	writeJSON(w, http.StatusCreated, map[string]any{"session": sess})
}

// List handles GET /chat/sessions.
func (h *Chat) List(w http.ResponseWriter, r *http.Request) {
	user := middleware.UserFrom(r.Context())
	sessions, err := h.svc.MySessions(r.Context(), user.ID)
	if err != nil {
		writeErr(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"sessions": sessions})
}

// Detail handles GET /chat/sessions/{id}.
func (h *Chat) Detail(w http.ResponseWriter, r *http.Request) {
	user := middleware.UserFrom(r.Context())
	staff := user.HasRole(domain.RoleSupport) || user.HasRole(domain.RoleAdmin)
	sess, err := h.svc.Session(r.Context(), chi.URLParam(r, "id"), user.ID, staff)
	if err != nil {
		writeErr(w, r, err)
		return
	}
	messages, err := h.svc.Messages(r.Context(), sess.ID)
	if err != nil {
		writeErr(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"session": sess, "messages": messages})
}

type chatMessageRequest struct {
	Body string `json:"body"`
}

// Send handles POST /chat/sessions/{id}/messages.
func (h *Chat) Send(w http.ResponseWriter, r *http.Request) {
	user := middleware.UserFrom(r.Context())
	staff := user.HasRole(domain.RoleSupport) || user.HasRole(domain.RoleAdmin)
	sess, err := h.svc.Session(r.Context(), chi.URLParam(r, "id"), user.ID, staff)
	if err != nil {
		writeErr(w, r, err)
		return
	}
	var req chatMessageRequest
	if err := decode(r, &req); err != nil {
		writeErr(w, r, domain.E(domain.KindInvalid, "BAD_JSON", err.Error()))
		return
	}
	role := domain.ChatRoleCustomer
	if staff {
		role = domain.ChatRoleAgent
	}
	msg, err := h.svc.SendMessage(r.Context(), sess.ID, user.ID, role, req.Body)
	if err != nil {
		writeErr(w, r, err)
		return
	}
	writeJSON(w, http.StatusCreated, map[string]any{"message": msg})
}

// Close handles POST /chat/sessions/{id}/close.
func (h *Chat) Close(w http.ResponseWriter, r *http.Request) {
	user := middleware.UserFrom(r.Context())
	staff := user.HasRole(domain.RoleSupport) || user.HasRole(domain.RoleAdmin)
	sess, err := h.svc.Session(r.Context(), chi.URLParam(r, "id"), user.ID, staff)
	if err != nil {
		writeErr(w, r, err)
		return
	}
	if err := h.svc.Close(r.Context(), sess.ID); err != nil {
		writeErr(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]bool{"closed": true})
}

// Queue handles GET /support/chat/queue (staff).
func (h *Chat) Queue(w http.ResponseWriter, r *http.Request) {
	sessions, err := h.svc.Queue(r.Context())
	if err != nil {
		writeErr(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"sessions": sessions})
}

// Claim handles POST /support/chat/sessions/{id}/claim (staff).
func (h *Chat) Claim(w http.ResponseWriter, r *http.Request) {
	user := middleware.UserFrom(r.Context())
	if err := h.svc.Claim(r.Context(), chi.URLParam(r, "id"), user.ID); err != nil {
		writeErr(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]bool{"claimed": true})
}

// OpenSellerChat handles POST /chat/orders/{orderId} â€” buyer-seller chat.
func (h *Chat) OpenSellerChat(w http.ResponseWriter, r *http.Request) {
	user := middleware.UserFrom(r.Context())
	sess, err := h.svc.OpenSellerChat(r.Context(), chi.URLParam(r, "orderId"), user.ID)
	if err != nil {
		writeErr(w, r, err)
		return
	}
	writeJSON(w, http.StatusCreated, map[string]any{"session": sess})
}

// SellerChatForOrder handles GET /chat/orders/{orderId} â€” existing session lookup.
func (h *Chat) SellerChatForOrder(w http.ResponseWriter, r *http.Request) {
	user := middleware.UserFrom(r.Context())
	sess, err := h.svc.SessionForOrder(r.Context(), chi.URLParam(r, "orderId"), user.ID)
	if err != nil {
		writeErr(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"session": sess})
}

// AI exposes the assistant endpoints.
type AI struct {
	svc *service.AIService
}

// NewAI creates an AI handler.
func NewAI(svc *service.AIService) *AI {
	return &AI{svc: svc}
}

// Ask handles POST /ai/ask â€” order-aware for authenticated users.
func (h *AI) Ask(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Question string `json:"question"`
	}
	if err := decode(r, &req); err != nil {
		writeErr(w, r, domain.E(domain.KindInvalid, "BAD_JSON", err.Error()))
		return
	}
	if req.Question == "" {
		writeErr(w, r, domain.E(domain.KindInvalid, "QUESTION_REQUIRED", "question is required"))
		return
	}
	var (
		ans *ai.Answer
		err error
	)
	if user := middleware.UserFrom(r.Context()); user != nil {
		ans, err = h.svc.AskForUser(r.Context(), user.ID, req.Question)
	} else {
		ans, err = h.svc.Ask(r.Context(), req.Question)
	}
	if err != nil {
		writeErr(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, ans)
}

// ReviewSummary handles POST /ai/review-summary.
func (h *AI) ReviewSummary(w http.ResponseWriter, r *http.Request) {
	var req struct {
		ProductID string `json:"product_id"`
	}
	if err := decode(r, &req); err != nil {
		writeErr(w, r, domain.E(domain.KindInvalid, "BAD_JSON", err.Error()))
		return
	}
	res, err := h.svc.ReviewSummary(r.Context(), req.ProductID)
	if err != nil {
		writeErr(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, res)
}

// TitleSuggestions handles POST /ai/title-suggest.
func (h *AI) TitleSuggestions(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Name string `json:"name"`
	}
	if err := decode(r, &req); err != nil {
		writeErr(w, r, domain.E(domain.KindInvalid, "BAD_JSON", err.Error()))
		return
	}
	titles, err := h.svc.TitleSuggestions(r.Context(), req.Name)
	if err != nil {
		writeErr(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"titles": titles})
}

// DescribeProduct handles POST /ai/describe-product.
func (h *AI) DescribeProduct(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Name     string            `json:"name"`
		Category string            `json:"category"`
		Attrs    map[string]string `json:"attributes,omitempty"`
	}
	if err := decode(r, &req); err != nil {
		writeErr(w, r, domain.E(domain.KindInvalid, "BAD_JSON", err.Error()))
		return
	}
	if req.Name == "" {
		writeErr(w, r, domain.E(domain.KindInvalid, "NAME_REQUIRED", "product name is required"))
		return
	}
	desc, err := h.svc.DescribeProduct(r.Context(), req.Name, req.Category, req.Attrs)
	if err != nil {
		writeErr(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"description": desc})
}

// Suggest handles GET /search/suggestions?q=.
func (h *AI) Suggest(w http.ResponseWriter, r *http.Request) {
	res, err := h.svc.Suggest(r.Context(), r.URL.Query().Get("q"), intQuery(r.URL.Query().Get("limit"), 6))
	if err != nil {
		writeErr(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, res)
}
