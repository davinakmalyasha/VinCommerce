package repository

import (
	"context"
	"errors"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/vincommerce/backend/internal/db"
	"github.com/vincommerce/backend/internal/domain"
)

// LiveRepository persists livestream sessions and their pinned products.
type LiveRepository struct {
	pool *db.Pool
}

// NewLiveRepository creates a LiveRepository.
func NewLiveRepository(pool *db.Pool) *LiveRepository {
	return &LiveRepository{pool: pool}
}

const liveCols = `l.id, l.seller_id, l.title, COALESCE(l.thumbnail_url,''), COALESCE(l.youtube_video_id,''),
	l.status, l.viewer_peak, l.scheduled_at, l.started_at, l.ended_at, l.created_at, u.full_name`

func scanLive(row interface{ Scan(...any) error }) (*domain.LiveSession, error) {
	var s domain.LiveSession
	err := row.Scan(&s.ID, &s.SellerID, &s.Title, &s.ThumbnailURL, &s.YoutubeVideoID,
		&s.Status, &s.ViewerPeak, &s.ScheduledAt, &s.StartedAt, &s.EndedAt, &s.CreatedAt, &s.SellerName)
	return &s, err
}

// Create schedules a new broadcast.
func (r *LiveRepository) Create(ctx context.Context, s *domain.LiveSession) error {
	return r.pool.QueryRow(ctx, `
		INSERT INTO live_sessions (seller_id, title, thumbnail_url, youtube_video_id, scheduled_at)
		VALUES ($1, $2, NULLIF($3, ''), NULLIF($4, ''), $5)
		RETURNING id, created_at`,
		s.SellerID, s.Title, s.ThumbnailURL, s.YoutubeVideoID, s.ScheduledAt).
		Scan(&s.ID, &s.CreatedAt)
}

// List returns live sessions first, then scheduled, then recently ended.
func (r *LiveRepository) List(ctx context.Context, limit int) ([]*domain.LiveSession, error) {
	if limit < 1 || limit > 50 {
		limit = 20
	}
	rows, err := r.pool.Query(ctx, `
		SELECT `+liveCols+`
		FROM live_sessions l JOIN users u ON u.id = l.seller_id
		ORDER BY (status = 'live') DESC,
		         (status = 'scheduled') DESC,
		         created_at DESC
		LIMIT $1`, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []*domain.LiveSession{}
	for rows.Next() {
		s, err := scanLive(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, s)
	}
	return out, rows.Err()
}

// ByID loads one session with its currently-pinned products.
func (r *LiveRepository) ByID(ctx context.Context, id string) (*domain.LiveSession, error) {
	s, err := scanLive(r.pool.QueryRow(ctx,
		`SELECT `+liveCols+` FROM live_sessions l JOIN users u ON u.id = l.seller_id WHERE l.id = $1`, id))
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, domain.ErrNotFound
	}
	if err != nil {
		return nil, err
	}
	pinned, err := r.Pinned(ctx, id)
	if err == nil {
		s.Pinned = pinned
	}
	return s, nil
}

// ListBySeller returns the seller's own sessions.
func (r *LiveRepository) ListBySeller(ctx context.Context, sellerID string) ([]*domain.LiveSession, error) {
	rows, err := r.pool.Query(ctx, `
		SELECT `+liveCols+`
		FROM live_sessions l JOIN users u ON u.id = l.seller_id
		WHERE l.seller_id = $1 ORDER BY created_at DESC LIMIT 30`, sellerID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []*domain.LiveSession{}
	for rows.Next() {
		s, err := scanLive(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, s)
	}
	return out, rows.Err()
}

// Transition moves a session through its status machine.
func (r *LiveRepository) Transition(ctx context.Context, sessionID, from, to string) error {
	tag, err := r.pool.Exec(ctx, `
		UPDATE live_sessions SET status = $2::varchar,
			started_at = CASE WHEN $2::varchar = 'live' THEN now() ELSE started_at END,
			ended_at   = CASE WHEN $2::varchar = 'ended' THEN now() ELSE ended_at END
		WHERE id = $1 AND status = $3::varchar`, sessionID, to, from)
	if err != nil {
		return err
	}
	if tag.RowsAffected() == 0 {
		return domain.E(domain.KindConflict, "INVALID_LIVE_TRANSITION", "cannot move stream from "+from+" to "+to)
	}
	return nil
}

// AttachProducts replaces a session's catalog set.
func (r *LiveRepository) AttachProducts(ctx context.Context, sessionID string, variantIDs []string) error {
	tx, err := r.pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer tx.Rollback(ctx)

	if _, err := tx.Exec(ctx, `DELETE FROM live_products WHERE session_id = $1`, sessionID); err != nil {
		return err
	}
	for _, vid := range variantIDs {
		if _, err := tx.Exec(ctx, `
			INSERT INTO live_products (session_id, variant_id) VALUES ($1, $2)
			ON CONFLICT DO NOTHING`, sessionID, vid); err != nil {
			return err
		}
	}
	return tx.Commit(ctx)
}

const liveProductCols = `
	lp.variant_id, lp.session_id, lp.price_override, lp.pinned_at, lp.unpinned_at,
	p.name AS product_name, v.name AS variant_name, COALESCE(v.image_url,''), v.price, v.stock`

func scanLiveProduct(row interface{ Scan(...any) error }) (*domain.LiveProduct, error) {
	var p domain.LiveProduct
	err := row.Scan(&p.VariantID, &p.SessionID, &p.PriceOverride, &p.PinnedAt, &p.UnpinnedAt,
		&p.ProductName, &p.VariantName, &p.ImageURL, &p.RegularPrice, &p.Stock)
	return &p, err
}

// Pinned lists products currently pinned on screen for a session.
func (r *LiveRepository) Pinned(ctx context.Context, sessionID string) ([]*domain.LiveProduct, error) {
	rows, err := r.pool.Query(ctx, `
		SELECT `+liveProductCols+`
		FROM live_products lp
		JOIN product_variants v ON v.id = lp.variant_id
		JOIN products p ON p.id = v.product_id
		WHERE lp.session_id = $1 AND lp.pinned_at IS NOT NULL AND lp.unpinned_at IS NULL
		ORDER BY lp.pinned_at`, sessionID)
	return r.collectProducts(rows, err)
}

// Catalog lists everything attached to a session (studio management view).
func (r *LiveRepository) Catalog(ctx context.Context, sessionID string) ([]*domain.LiveProduct, error) {
	rows, err := r.pool.Query(ctx, `
		SELECT `+liveProductCols+`
		FROM live_products lp
		JOIN product_variants v ON v.id = lp.variant_id
		JOIN products p ON p.id = v.product_id
		WHERE lp.session_id = $1`, sessionID)
	return r.collectProducts(rows, err)
}

func (r *LiveRepository) collectProducts(rows pgx.Rows, err error) ([]*domain.LiveProduct, error) {
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []*domain.LiveProduct{}
	for rows.Next() {
		p, err := scanLiveProduct(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, p)
	}
	return out, rows.Err()
}

// Pin puts a product on the live shelf.
func (r *LiveRepository) Pin(ctx context.Context, sessionID, variantID string) error {
	tag, err := r.pool.Exec(ctx, `
		UPDATE live_products SET pinned_at = now(), unpinned_at = NULL
		WHERE session_id = $1 AND variant_id = $2`, sessionID, variantID)
	if err != nil {
		return err
	}
	if tag.RowsAffected() == 0 {
		return domain.E(domain.KindInvalid, "NOT_ATTACHED", "variant is not attached to this stream")
	}
	return nil
}

// Unpin removes a product from the live shelf.
func (r *LiveRepository) Unpin(ctx context.Context, sessionID, variantID string) error {
	_, err := r.pool.Exec(ctx, `
		UPDATE live_products SET unpinned_at = now(), pinned_at = NULL
		WHERE session_id = $1 AND variant_id = $2 AND pinned_at IS NOT NULL`, sessionID, variantID)
	return err
}

// BumpViewer records a viewer join and raises the peak counter.
// GREATEST keeps peak a true high-water mark instead of accumulating joins.
func (r *LiveRepository) BumpViewer(ctx context.Context, sessionID string) {
	_, _ = r.pool.Exec(ctx, `
		UPDATE live_sessions SET
			viewer_peak = GREATEST(viewer_peak + 1, viewer_count + 1),
			viewer_count = viewer_count + 1
		WHERE id = $1`, sessionID)
}

// DropViewer records a viewer leaving the stream.
func (r *LiveRepository) DropViewer(ctx context.Context, sessionID string) {
	_, _ = r.pool.Exec(ctx, `
		UPDATE live_sessions SET viewer_count = GREATEST(viewer_count - 1, 0) WHERE id = $1`, sessionID)
}

// TouchStarted ensures started_at is set when streaming begins via SSE too.
func (r *LiveRepository) TouchStarted(ctx context.Context, sessionID string) time.Time {
	var t time.Time
	_ = r.pool.QueryRow(ctx,
		`UPDATE live_sessions SET started_at = COALESCE(started_at, now()) WHERE id = $1 RETURNING started_at`,
		sessionID).Scan(&t)
	return t
}
