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

	// CommissionComputed distinguishes "the split has not been derived yet"
	// from "the split was derived and the fee is genuinely zero".
	//
	// The code previously used `FeeAmount == 0` as that predicate, which is
	// ambiguous. A promo-period order with a 0% commission has FeeAmount == 0
	// after escrow release, and a refund on that order afterwards recomputed the
	// fee from the CURRENTLY active rate and then debited it from the platform
	// wallet -- taking commission earned on other sellers' orders, or failing
	// the entire refund with INSUFFICIENT_BALANCE and leaving the buyer's return
	// stuck with no retry path.
	//
	// A boolean cannot be ambiguous, and it is what lets the rate card be
	// snapshotted onto the intent at creation rather than read at release time.
	CommissionComputed bool `json:"commission_computed"`

	// CommissionRatePct and CommissionRateFixed are the rate card values that
	// were in force when the charge was created. A refund reverses the fee that
	// was actually taken, not the fee that today's settings would produce.
	CommissionRatePct   float64 `json:"commission_rate_pct"`
	CommissionRateFixed float64 `json:"commission_rate_fixed"`
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
