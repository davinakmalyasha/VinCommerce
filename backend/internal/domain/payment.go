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
	TxReasonTopUp         = "topup"
)

// Payment method names.
//
// Constants rather than literals because these are now compared for their
// accounting meaning, not just stored. COD in particular routes the capture
// journal to cod_receivable instead of gateway_clearing, on the grounds that the
// cash is held by the courier and posting it to a payment gateway's account would
// put the money in the wrong place for the several days it spends there. A
// comparison written as `intent.Method == "cod"` is a comparison that will be
// spelled three different ways across the codebase and mean two different things.
const (
	MethodCOD            = "cod"
	MethodBankTransfer   = "bank_transfer"
	MethodEWallet        = "e_wallet"
	MethodQRIS           = "qris"
	MethodVirtualAccount = "va"
	MethodRetailOutlet   = "retail_outlet"
	MethodCreditCard     = "credit_card"
	MethodWalletBalance  = "wallet_balance"
	MethodManualTransfer = "manual_transfer"
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

	// CommissionSnapshotPresent says the two rates above are a real snapshot,
	// rather than the `NOT NULL DEFAULT 0` that an intent predating migration
	// 00042 carries.
	//
	// The columns are `NOT NULL DEFAULT 0`, so "no snapshot" and "a genuine 0%
	// rate" are the same two values. Treating (0, 0) as absent is the safer of
	// the two guesses, but it is still a guess: if the active rate was 0% when
	// the order was placed and an admin raises it before delivery, the release
	// reads "no snapshot" and charges the seller the NEW rate on a sale that was
	// priced at 0%.
	//
	// A boolean cannot be ambiguous. This is the same argument the
	// CommissionComputed field above records for the fee split, applied to the
	// rate. `false` means "fall back to the active rate", which is what every
	// pre-00042 intent should keep doing.
	CommissionSnapshotPresent bool `json:"commission_snapshot_present"`
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

// Wallet top-up statuses.
const (
	TopUpPending = "pending"
	TopUpPaid    = "paid"
	TopUpFailed  = "failed"
	TopUpExpired = "expired"
)

// Top-up bounds. Whole rupiah, deliberately narrow: a Rp1 top-up costs more in
// gateway fees than it adds, and an unbounded amount is a typo away from a
// charge the buyer cannot afford.
const (
	MinTopUpAmount = 10_000
	MaxTopUpAmount = 20_000_000
)

// WalletTopUp is a buyer's attempt to add balance through the gateway.
type WalletTopUp struct {
	ID          string     `json:"id"`
	UserID      string     `json:"user_id"`
	Amount      float64    `json:"amount"`
	Status      string     `json:"status"`
	Gateway     string     `json:"gateway"`
	GatewayRef  string     `json:"gateway_ref,omitempty"`
	SnapToken   string     `json:"snap_token,omitempty"`
	RedirectURL string     `json:"payment_url,omitempty"`
	PaidAt      *time.Time `json:"paid_at,omitempty"`
	CreatedAt   time.Time  `json:"created_at"`
}
