package handler

import (
	"net/http"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/vincommerce/backend/internal/domain"
	"github.com/vincommerce/backend/internal/httpapi/middleware"
	"github.com/vincommerce/backend/internal/service"
)

// Live exposes the livestream commerce API.
type Live struct {
	svc *service.LiveService
}

// NewLive creates a Live handler.
func NewLive(svc *service.LiveService) *Live {
	return &Live{svc: svc}
}

// List handles GET /live.
func (h *Live) List(w http.ResponseWriter, r *http.Request) {
	items, err := h.svc.List(r.Context(), intQuery(r.URL.Query().Get("limit"), 20))
	if err != nil {
		writeErr(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"sessions": items})
}

// Detail handles GET /live/{id}.
func (h *Live) Detail(w http.ResponseWriter, r *http.Request) {
	sess, err := h.svc.Detail(r.Context(), chi.URLParam(r, "id"))
	if err != nil {
		writeErr(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"session": sess})
}

type createLiveRequest struct {
	Title          string `json:"title"`
	ThumbnailURL   string `json:"thumbnail_url,omitempty"`
	YoutubeVideoID string `json:"youtube_video_id"`
	ScheduledAt    string `json:"scheduled_at,omitempty"`
}

// Create handles POST /seller/live.
func (h *Live) Create(w http.ResponseWriter, r *http.Request) {
	user := middleware.UserFrom(r.Context())
	var req createLiveRequest
	if err := decode(r, &req); err != nil {
		writeErr(w, r, domain.E(domain.KindInvalid, "BAD_JSON", err.Error()))
		return
	}
	var scheduledAt *time.Time
	if req.ScheduledAt != "" {
		if t, err := time.Parse(time.RFC3339, req.ScheduledAt); err == nil {
			scheduledAt = &t
		}
	}
	sess, err := h.svc.Create(r.Context(), user.ID, req.Title, req.ThumbnailURL, req.YoutubeVideoID, scheduledAt)
	if err != nil {
		writeErr(w, r, err)
		return
	}
	writeJSON(w, http.StatusCreated, map[string]any{"session": sess})
}

// MySessions handles GET /seller/live.
func (h *Live) MySessions(w http.ResponseWriter, r *http.Request) {
	user := middleware.UserFrom(r.Context())
	items, err := h.svc.MySessions(r.Context(), user.ID)
	if err != nil {
		writeErr(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"sessions": items})
}

// Transition handles POST /seller/live/{id}/transition {to}.
func (h *Live) Transition(w http.ResponseWriter, r *http.Request) {
	user := middleware.UserFrom(r.Context())
	var req struct {
		To string `json:"to"`
	}
	if err := decode(r, &req); err != nil {
		writeErr(w, r, domain.E(domain.KindInvalid, "BAD_JSON", err.Error()))
		return
	}
	sess, err := h.svc.Transition(r.Context(), chi.URLParam(r, "id"), user.ID, req.To)
	if err != nil {
		writeErr(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"session": sess})
}

// Attach handles PUT /seller/live/{id}/products {variant_ids}.
func (h *Live) Attach(w http.ResponseWriter, r *http.Request) {
	user := middleware.UserFrom(r.Context())
	var req struct {
		VariantIDs []string `json:"variant_ids"`
	}
	if err := decode(r, &req); err != nil {
		writeErr(w, r, domain.E(domain.KindInvalid, "BAD_JSON", err.Error()))
		return
	}
	if err := h.svc.AttachProducts(r.Context(), chi.URLParam(r, "id"), user.ID, req.VariantIDs); err != nil {
		writeErr(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]bool{"attached": true})
}

// Pin handles POST /seller/live/{id}/pin {variant_id, pinned}.
func (h *Live) Pin(w http.ResponseWriter, r *http.Request) {
	user := middleware.UserFrom(r.Context())
	var req struct {
		VariantID string `json:"variant_id"`
		Pinned    bool   `json:"pinned"`
	}
	if err := decode(r, &req); err != nil {
		writeErr(w, r, domain.E(domain.KindInvalid, "BAD_JSON", err.Error()))
		return
	}
	if err := h.svc.Pin(r.Context(), chi.URLParam(r, "id"), user.ID, req.VariantID, req.Pinned); err != nil {
		writeErr(w, r, err)
		return
	}
	pinned, _ := h.svc.Pinned(r.Context(), chi.URLParam(r, "id"))
	writeJSON(w, http.StatusOK, map[string]any{"pinned": pinned})
}

type liveChatRequest struct {
	Body string `json:"body"`
}

// ChatPost handles POST /live/{id}/chat — ephemeral viewer chat.
func (h *Live) ChatPost(w http.ResponseWriter, r *http.Request) {
	user := middleware.UserFrom(r.Context())
	var req liveChatRequest
	if err := decode(r, &req); err != nil {
		writeErr(w, r, domain.E(domain.KindInvalid, "BAD_JSON", err.Error()))
		return
	}
	name := user.FullName
	if name == "" {
		name = user.Email
	}
	if err := h.svc.PostChat(r.Context(), chi.URLParam(r, "id"), name, req.Body); err != nil {
		writeErr(w, r, err)
		return
	}
	writeJSON(w, http.StatusCreated, map[string]bool{"sent": true})
}

// Pinned handles GET /live/{id}/pinned — pollable shelf fallback.
func (h *Live) Pinned(w http.ResponseWriter, r *http.Request) {
	pinned, err := h.svc.Pinned(r.Context(), chi.URLParam(r, "id"))
	if err != nil {
		writeErr(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"pinned": pinned})
}

// Catalog handles GET /seller/live/{id}/catalog.
func (h *Live) Catalog(w http.ResponseWriter, r *http.Request) {
	user := middleware.UserFrom(r.Context())
	items, err := h.svc.Catalog(r.Context(), chi.URLParam(r, "id"), user.ID)
	if err != nil {
		writeErr(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"products": items})
}
