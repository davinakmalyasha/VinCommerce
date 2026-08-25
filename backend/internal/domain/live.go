package domain

import "time"

// Live session statuses.
const (
	LiveScheduled = "scheduled"
	LiveRunning   = "live"
	LiveEnded     = "ended"
)

// CanLiveTransition validates session status changes.
func CanLiveTransition(from, to string) bool {
	switch {
	case from == LiveScheduled && to == LiveRunning:
		return true
	case from == LiveRunning && to == LiveEnded:
		return true
	}
	return false
}

// LiveSession is one seller broadcast.
type LiveSession struct {
	ID             string     `json:"id"`
	SellerID       string     `json:"seller_id"`
	Title          string     `json:"title"`
	ThumbnailURL   string     `json:"thumbnail_url,omitempty"`
	YoutubeVideoID string     `json:"youtube_video_id,omitempty"`
	Status         string     `json:"status"`
	ViewerPeak     int        `json:"viewer_peak"`
	ScheduledAt    *time.Time `json:"scheduled_at,omitempty"`
	StartedAt      *time.Time `json:"started_at,omitempty"`
	EndedAt        *time.Time `json:"ended_at,omitempty"`
	CreatedAt      time.Time  `json:"created_at"`

	// enriched
	SellerName  string         `json:"seller_name,omitempty"`
	Pinned      []*LiveProduct `json:"pinned,omitempty"`
	ViewerCount int            `json:"viewer_count,omitempty"`
}

// LiveProduct is a catalog item attached to a stream, optionally pinned live.
type LiveProduct struct {
	VariantID     string     `json:"variant_id"`
	SessionID     string     `json:"session_id,omitempty"`
	PriceOverride *float64   `json:"price_override,omitempty"`
	PinnedAt      *time.Time `json:"pinned_at,omitempty"`
	UnpinnedAt    *time.Time `json:"unpinned_at,omitempty"`

	// enriched
	ProductName  string  `json:"product_name,omitempty"`
	VariantName  string  `json:"variant_name,omitempty"`
	ImageURL     string  `json:"image_url,omitempty"`
	RegularPrice float64 `json:"regular_price"`
	Stock        int     `json:"stock"`
}
