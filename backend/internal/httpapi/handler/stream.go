package handler

import (
	"encoding/json"
	"fmt"
	"log/slog"
	"net/http"
	"time"

	"github.com/vincommerce/backend/internal/domain"
	"github.com/vincommerce/backend/internal/httpapi/middleware"
	"github.com/vincommerce/backend/internal/service"
	"github.com/vincommerce/backend/internal/stream"
)

// Stream exposes realtime SSE endpoints.
type Stream struct {
	broker *stream.Broker
	chats  *service.ChatService
	logger *slog.Logger
}

// NewStream creates a Stream handler.
func NewStream(broker *stream.Broker, chats *service.ChatService, logger *slog.Logger) *Stream {
	return &Stream{broker: broker, chats: chats, logger: logger}
}

// Chat streams messages for a chat session via SSE (participants and staff only).
func (h *Stream) Chat(w http.ResponseWriter, r *http.Request) {
	user := middleware.UserFrom(r.Context())
	sessionID := r.URL.Query().Get("session")
	if sessionID == "" {
		http.Error(w, "session required", http.StatusBadRequest)
		return
	}
	if h.chats != nil {
		staff := user.HasRole(domain.RoleSupport) || user.HasRole(domain.RoleAdmin)
		if _, err := h.chats.Session(r.Context(), sessionID, user.ID, staff); err != nil {
			http.Error(w, "forbidden", http.StatusForbidden)
			return
		}
	}

	flusher, ok := w.(http.Flusher)
	if !ok {
		http.Error(w, "streaming unsupported", http.StatusInternalServerError)
		return
	}

	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-cache")
	w.Header().Set("Connection", "keep-alive")
	w.Header().Set("X-Accel-Buffering", "no")

	ctx := r.Context()
	ch, closeFn, err := h.broker.SubscribeChannel(ctx, "chat:"+sessionID)
	if err != nil {
		http.Error(w, "stream unavailable", http.StatusServiceUnavailable)
		return
	}
	defer closeFn()

	fmt.Fprintf(w, "event: connected\ndata: {}\n\n")
	flusher.Flush()

	heartbeat := time.NewTicker(25 * time.Second)
	defer heartbeat.Stop()

	for {
		select {
		case payload, ok := <-ch:
			if !ok {
				return
			}
			fmt.Fprintf(w, "event: message\ndata: %s\n\n", payload)
			flusher.Flush()
		case <-heartbeat.C:
			fmt.Fprintf(w, ": ping\n\n")
			flusher.Flush()
		case <-ctx.Done():
			return
		}
	}
}

// Orders streams order events for the authenticated user via SSE.
func (h *Stream) Orders(w http.ResponseWriter, r *http.Request) {
	user := middleware.UserFrom(r.Context())

	flusher, ok := w.(http.Flusher)
	if !ok {
		http.Error(w, "streaming unsupported", http.StatusInternalServerError)
		return
	}

	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-cache")
	w.Header().Set("Connection", "keep-alive")
	w.Header().Set("X-Accel-Buffering", "no")

	ctx := r.Context()
	ch, closeFn, err := h.broker.Subscribe(ctx, user.ID)
	if err != nil {
		http.Error(w, "stream unavailable", http.StatusServiceUnavailable)
		return
	}
	defer closeFn()

	writeEvent := func(ev stream.Event) {
		data, _ := json.Marshal(ev)
		fmt.Fprintf(w, "event: order\n")
		fmt.Fprintf(w, "data: %s\n\n", data)
		flusher.Flush()
	}

	writeEvent(stream.Event{Type: "connected", Message: "stream established", At: time.Now().UTC()})

	heartbeat := time.NewTicker(25 * time.Second)
	defer heartbeat.Stop()

	for {
		select {
		case ev, ok := <-ch:
			if !ok {
				return
			}
			writeEvent(ev)
		case <-heartbeat.C:
			fmt.Fprintf(w, ": ping\n\n")
			flusher.Flush()
		case <-ctx.Done():
			return
		}
	}
}
