package domain

import (
	"net/url"
	"path"
	"regexp"
	"strings"
	"time"
)

// Store statuses.
const (
	StorePending   = "pending"
	StoreActive    = "active"
	StoreSuspended = "suspended"
	StoreRejected  = "rejected"
)

// Return statuses.
const (
	ReturnRequested = "requested"
	ReturnApproved  = "approved"
	// ReturnRefunding: the provider has been asked for a gateway refund and the
	// answer is not back yet. Distinct from `refunded` because a provider call
	// can fail, time out, or need an operator -- none of which is "done".
	ReturnRefunding = "refunding"
	ReturnRejected  = "rejected"
	ReturnReturned  = "returned"
	ReturnRefunded  = "refunded"
	ReturnClosed    = "closed"
)

// Store is the seller's marketplace profile.
type Store struct {
	ID            string    `json:"id"`
	OwnerID       string    `json:"owner_id"`
	Name          string    `json:"name"`
	Slug          string    `json:"slug"`
	Description   string    `json:"description,omitempty"`
	LogoURL       string    `json:"logo_url,omitempty"`
	BannerURL     string    `json:"banner_url,omitempty"`
	Status        string    `json:"status"`
	Rating        float64   `json:"rating"`
	RatingCount   int       `json:"rating_count"`
	ProductsCount int       `json:"products_count"`
	FollowerCount int       `json:"follower_count"`
	JoinedAt      time.Time `json:"joined_at"`
	UpdatedAt     time.Time `json:"updated_at"`

	IsFollowing bool `json:"is_following,omitempty"`
	IsVerified  bool `json:"is_verified,omitempty"`

	// Presence & responsiveness signals (public store views).
	Online          bool `json:"online,omitempty"`
	ResponseRatePct *int `json:"response_rate_pct,omitempty"`
	AvgReplyMinutes *int `json:"avg_reply_minutes,omitempty"`
	IsPowerSeller   bool `json:"is_power_seller,omitempty"`
}

// SellerKYC is the identity verification record.
type SellerKYC struct {
	ID        string `json:"id"`
	StoreID   string `json:"store_id"`
	OwnerName string `json:"owner_name"`
	IDNumber  string `json:"id_number"`
	// NPWP is optional: an individual seller without a registered tax number is
	// valid, and the invoice omits the line rather than printing a placeholder.
	NPWP          string     `json:"npwp,omitempty"`
	IDDocumentURL string     `json:"id_document_url,omitempty"`
	BankName      string     `json:"bank_name"`
	BankAccount   string     `json:"bank_account"`
	Status        string     `json:"status"`
	AdminNote     string     `json:"admin_note,omitempty"`
	ReviewedAt    *time.Time `json:"reviewed_at,omitempty"`
	CreatedAt     time.Time  `json:"created_at"`
}

// ReturnRequest is a buyer's return/refund claim.
type ReturnRequest struct {
	ID           string     `json:"id"`
	OrderID      string     `json:"order_id"`
	OrderItemID  string     `json:"order_item_id"`
	BuyerID      string     `json:"buyer_id"`
	SellerID     string     `json:"seller_id"`
	IssueType    string     `json:"issue_type,omitempty"` // return | item_not_received
	Reason       string     `json:"reason"`
	Description  string     `json:"description"`
	EvidenceURLs []string   `json:"evidence_urls"`
	Status       string     `json:"status"`
	Resolution   string     `json:"resolution,omitempty"`
	SellerNote   string     `json:"seller_note,omitempty"`
	AdminNote    string     `json:"admin_note,omitempty"`
	RequestedAt  time.Time  `json:"requested_at"`
	ResolvedAt   *time.Time `json:"resolved_at,omitempty"`

	ItemName string  `json:"item_name,omitempty"`
	Amount   float64 `json:"amount,omitempty"`
}

// MaxReturnEvidence caps how many photos a return claim may attach. Three is
// what the review flow already allows, and each one costs upload quota.
const MaxReturnEvidence = 3

// uploadedFilePattern is the exact shape MediaService.Save mints: 16 random
// bytes hex-encoded plus a raster extension. Anything else is not a file this
// platform produced.
var uploadedFilePattern = regexp.MustCompile(`^[a-f0-9]{32}\.(jpg|png|webp|gif)$`)

// NormalizeEvidenceURLs validates the photos attached to a return claim.
//
// Before this existed the handler copied `evidence_urls` straight from the
// request body into the database. Nothing rendered them yet, which is the only
// reason it was not already an injection surface: the moment a seller or admin
// screen showed them, any string a buyer sent -- `javascript:...`, a data: URI,
// or a third-party tracking pixel -- would have been displayed on a staff page.
// The rule is intentionally narrow: a URL must resolve to a file THIS service
// uploaded, so the accepted forms are `/uploads/<hex>.<ext>` and an absolute
// URL on publicBaseURL with the same path. `publicBaseURL` is the API's own
// APP_BASE_URL, which is what the upload endpoint prefixes its result with.
func NormalizeEvidenceURLs(raw []string, publicBaseURL string) ([]string, error) {
	if len(raw) > MaxReturnEvidence {
		return nil, E(KindInvalid, "TOO_MANY_EVIDENCE",
			"at most 3 evidence photos are allowed")
	}
	base, err := url.Parse(strings.TrimSpace(publicBaseURL))
	if err != nil {
		base = &url.URL{}
	}
	out := make([]string, 0, len(raw))
	seen := make(map[string]struct{}, len(raw))
	for _, entry := range raw {
		entry = strings.TrimSpace(entry)
		if entry == "" {
			continue
		}
		parsed, err := url.Parse(entry)
		if err != nil {
			return nil, E(KindInvalid, "BAD_EVIDENCE_URL", "evidence must be an uploaded image")
		}
		if parsed.Scheme != "" && parsed.Scheme != "http" && parsed.Scheme != "https" {
			return nil, E(KindInvalid, "BAD_EVIDENCE_URL", "evidence must be an uploaded image")
		}
		if parsed.User != nil || parsed.RawQuery != "" || parsed.Fragment != "" {
			return nil, E(KindInvalid, "BAD_EVIDENCE_URL", "evidence must be an uploaded image")
		}
		// An absolute URL must point at this platform. Without the host check
		// `https://evil.example.com/uploads/<valid>.jpg` passed, and once these
		// are rendered it is a tracking pixel on a staff page.
		if parsed.Host != "" && (base.Host == "" || !strings.EqualFold(parsed.Host, base.Host)) {
			return nil, E(KindInvalid, "BAD_EVIDENCE_URL", "evidence must be an uploaded image")
		}
		dir, name := path.Split(parsed.Path)
		if dir != "/uploads/" || !uploadedFilePattern.MatchString(name) {
			return nil, E(KindInvalid, "BAD_EVIDENCE_URL", "evidence must be an uploaded image")
		}
		if _, dup := seen[name]; dup {
			continue
		}
		seen[name] = struct{}{}
		out = append(out, entry)
	}
	return out, nil
}
