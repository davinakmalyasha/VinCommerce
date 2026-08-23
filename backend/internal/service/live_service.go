package service

import (
	"context"
	"encoding/json"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/vincommerce/backend/internal/domain"
	"github.com/vincommerce/backend/internal/repository"
	"github.com/vincommerce/backend/internal/stream"
)

// LiveService powers livestream selling sessions.
type LiveService struct {
	live     *repository.LiveRepository
	products *repository.ProductRepository
	broker   *stream.Broker
}

// NewLiveService wires the livestream engine.
func NewLiveService(live *repository.LiveRepository, products *repository.ProductRepository) *LiveService {
	return &LiveService{live: live, products: products}
}

// SetBroker attaches the realtime event bus.
func (s *LiveService) SetBroker(b *stream.Broker) { s.broker = b }

func (s *LiveService) channel(id string) string { return "live:" + id }

// Create schedules a broadcast.
func (s *LiveService) Create(ctx context.Context, sellerID, title, thumbnailURL, youtubeVideoID string, scheduledAt *time.Time) (*domain.LiveSession, error) {
	title = strings.TrimSpace(title)
	if len(title) < 3 {
		return nil, domain.E(domain.KindInvalid, "TITLE_REQUIRED", "judul siaran minimal 3 karakter")
	}
	sess := &domain.LiveSession{
		ID:             uuid.NewString(),
		SellerID:       sellerID,
		Title:          title,
		ThumbnailURL:   thumbnailURL,
		YoutubeVideoID: strings.TrimSpace(youtubeVideoID),
		ScheduledAt:    scheduledAt,
	}
	if err := s.live.Create(ctx, sess); err != nil {
		return nil, err
	}
	return sess, nil
}

// List shows live/scheduled/ended broadcasts.
func (s *LiveService) List(ctx context.Context, limit int) ([]*domain.LiveSession, error) {
	return s.live.List(ctx, limit)
}

// Detail loads one broadcast (and bumps viewer counters).
func (s *LiveService) Detail(ctx context.Context, id string) (*domain.LiveSession, error) {
	sess, err := s.live.ByID(ctx, id)
	if err != nil {
		return nil, err
	}
	s.live.BumpViewer(ctx, id)
	return sess, nil
}

// MySessions lists a seller's own streams.
func (s *LiveService) MySessions(ctx context.Context, sellerID string) ([]*domain.LiveSession, error) {
	return s.live.ListBySeller(ctx, sellerID)
}

// Transition moves a stream through its status machine (owner only).
func (s *LiveService) Transition(ctx context.Context, sessionID, sellerID, to string) (*domain.LiveSession, error) {
	sess, err := s.mustOwn(ctx, sessionID, sellerID)
	if err != nil {
		return nil, err
	}
	if !domain.CanLiveTransition(sess.Status, to) {
		return nil, domain.E(domain.KindConflict, "INVALID_TRANSITION",
			"tidak bisa mengubah siaran dari "+sess.Status+" ke "+to)
	}
	if err := s.live.Transition(ctx, sessionID, sess.Status, to); err != nil {
		return nil, err
	}
	s.publish(ctx, sessionID, map[string]any{"type": "session.status", "status": to})
	return s.live.ByID(ctx, sessionID)
}

// AttachProducts replaces the stream's catalog (variants must be the seller's).
func (s *LiveService) AttachProducts(ctx context.Context, sessionID, sellerID string, variantIDs []string) error {
	if _, err := s.mustOwn(ctx, sessionID, sellerID); err != nil {
		return err
	}
	for _, vid := range variantIDs {
		owner, err := s.products.VariantSeller(ctx, vid)
		if err != nil || owner != sellerID {
			return domain.E(domain.KindForbidden, "NOT_OWNED", "variant does not belong to your store")
		}
	}
	return s.live.AttachProducts(ctx, sessionID, variantIDs)
}

// Pin puts an attached product on the live shelf.
func (s *LiveService) Pin(ctx context.Context, sessionID, sellerID, variantID string, pinned bool) error {
	if _, err := s.mustOwn(ctx, sessionID, sellerID); err != nil {
		return err
	}
	var err error
	if pinned {
		err = s.live.Pin(ctx, sessionID, variantID)
	} else {
		err = s.live.Unpin(ctx, sessionID, variantID)
	}
	if err != nil {
		return err
	}
	s.publish(ctx, sessionID, map[string]any{"type": "pinned.update"})
	return nil
}

// Pinned returns the current live shelf.
func (s *LiveService) Pinned(ctx context.Context, sessionID string) ([]*domain.LiveProduct, error) {
	return s.live.Pinned(ctx, sessionID)
}

// Catalog returns everything attached to a session (studio view).
func (s *LiveService) Catalog(ctx context.Context, sessionID, sellerID string) ([]*domain.LiveProduct, error) {
	if _, err := s.mustOwn(ctx, sessionID, sellerID); err != nil {
		return nil, err
	}
	return s.live.Catalog(ctx, sessionID)
}

// ViewerJoin records a viewer joining the SSE stream.
func (s *LiveService) ViewerJoin(ctx context.Context, sessionID string) {
	s.live.BumpViewer(ctx, sessionID)
	s.live.TouchStarted(ctx, sessionID)
}

// PostChat fans a chat message out to all stream viewers (ephemeral).
func (s *LiveService) PostChat(ctx context.Context, sessionID, userName, body string) error {
	body = strings.TrimSpace(body)
	if body == "" {
		return domain.E(domain.KindInvalid, "EMPTY_MESSAGE", "pesan kosong")
	}
	s.publish(ctx, sessionID, map[string]any{"type": "chat", "user": userName, "body": body})
	return nil
}

func (s *LiveService) mustOwn(ctx context.Context, sessionID, sellerID string) (*domain.LiveSession, error) {
	sess, err := s.live.ByID(ctx, sessionID)
	if err != nil {
		return nil, err
	}
	if sess.SellerID != sellerID {
		return nil, domain.E(domain.KindForbidden, "NOT_OWNED", "bukan siaran milikmu")
	}
	return sess, nil
}

func (s *LiveService) publish(ctx context.Context, sessionID string, payload map[string]any) {
	if s.broker == nil {
		return
	}
	payload["at"] = time.Now().UTC()
	b, err := json.Marshal(payload)
	if err == nil {
		_ = s.broker.PublishChannel(ctx, s.channel(sessionID), b)
	}
}
