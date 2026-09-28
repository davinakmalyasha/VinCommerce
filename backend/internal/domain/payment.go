package domain

import "time"

// Payment intent statuses (escrow lifecycle).
const (
	IntentInitiated         = "initiated"
	IntentAuthorized        = "authorized"
	IntentCaptured          = "captured"
	IntentReleased          = "released"
	IntentRefunded          = "refunded"
	IntentPartiallyRefunded = "partially_refunded"
	IntentDisputedSplit     = "disputed_split"
	IntentFailed            = "failed"
	IntentExpired           = "expired"
)

// Wallet transaction reasons.
const (
	TxReasonEscrowRelease = "escrow_release"
	TxReasonRefund        = "refund"
	TxReasonPayout        = "payout"
	TxReasonAdjustment    = "adjustment"
)

// PaymentIntent is the escrow record for an order.
type PaymentIntent struct {
	ID               string     `json:"id"`
	OrderID          string     `json:"order_id"`
	BuyerID          string     `json:"buyer_id"`
	Amount           float64    `json:"amount"`
	Currency         string     `json:"currency"`
	Status           string     `json:"status"`
	Gateway          string     `json:"gateway"`
	GatewayRef       string     `json:"gateway_ref,omitempty"`
	SnapToken        string     `json:"snap_token,omitempty"` // Midtrans Snap checkout token
	GatewayTxnID     string     `json:"gateway_txn_id,omitempty"`
	RedirectURL      string     `json:"redirect_url,omitempty"`
	Method           string     `json:"method,omitempty"`
	IdempotencyKey   string     `json:"-"`
	EscrowReleasedAt *time.Time `json:"escrow_released_at,omitempty"`
	CapturedAt       *time.Time `json:"captured_at,omitempty"`
	RefundedAt       *time.Time `json:"refunded_at,omitempty"`
	CreatedAt        time.Time  `json:"created_at"`
	UpdatedAt        time.Time  `json:"updated_at"`
	Inserted         bool       `json:"-"`

	FeeAmount    float64 `json:"fee_amount"`
	SellerAmount float64 `json:"seller_amount"`
}

// Wallet holds seller balances.
type Wallet struct {
	UserID      string    `json:"user_id"`
	Balance     float64   `json:"balance"`
	HeldBalance float64   `json:"held_balance"`
	CreatedAt   time.Time `json:"created_at"`
	UpdatedAt   time.Time `json:"updated_at"`
}

// WalletTransaction is a ledger entry.
type WalletTransaction struct {
	ID           int64     `json:"id"`
	WalletID     string    `json:"wallet_id"`
	Kind         string    `json:"kind"`
	Reason       string    `json:"reason"`
	Amount       float64   `json:"amount"`
	BalanceAfter float64   `json:"balance_after"`
	RefID        string    `json:"ref_id,omitempty"`
	CreatedAt    time.Time `json:"created_at"`
}

// Payout is a seller withdrawal.
type Payout struct {
	ID          string     `json:"id"`
	WalletID    string     `json:"wallet_id"`
	Amount      float64    `json:"amount"`
	Status      string     `json:"status"`
	GatewayRef  string     `json:"gateway_ref,omitempty"`
	BankName    string     `json:"bank_name,omitempty"`
	BankAccount string     `json:"bank_account,omitempty"`
	RequestedAt time.Time  `json:"requested_at"`
	ProcessedAt *time.Time `json:"processed_at,omitempty"`
}
