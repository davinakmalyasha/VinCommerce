package domain

import "time"

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
	ID            string     `json:"id"`
	StoreID       string     `json:"store_id"`
	OwnerName     string     `json:"owner_name"`
	IDNumber      string     `json:"id_number"`
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
