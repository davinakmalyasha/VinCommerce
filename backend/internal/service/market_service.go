package service

import (
	"context"
	"fmt"
	"math/rand"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/vincommerce/backend/internal/domain"
	"github.com/vincommerce/backend/internal/mail"
	"github.com/vincommerce/backend/internal/repository"
)

// MarketService implements Q&A, bundles, price alerts and flash-sale admin.
type MarketService struct {
	market   *repository.QARepository
	notifs   *NotificationService
	loyalty  *repository.LoyaltyRepository
	disputes *repository.DisputeRepository
	game     *repository.GamificationRepository
	users    *repository.UserRepository
	mailer   *mail.Client
	webURL   string
	payments *PaymentService
}

// NewMarketService creates a MarketService.
func NewMarketService(market *repository.QARepository) *MarketService {
	return &MarketService{market: market}
}

// SetGamification enables voucher claims, check-ins and the wheel.
func (s *MarketService) SetGamification(g *repository.GamificationRepository) { s.game = g }

// --- gamification ---

// ClaimVoucher attaches an active coupon to the user's account by code.
func (s *MarketService) ClaimVoucher(ctx context.Context, userID, code string) (*domain.Coupon, error) {
	if s.game == nil {
		return nil, domain.E(domain.KindInternal, "UNAVAILABLE", "voucher claims unavailable")
	}
	return s.game.ClaimCoupon(ctx, userID, code)
}

// MyClaims lists the user's claimed coupons.
func (s *MarketService) MyClaims(ctx context.Context, userID string) ([]*repository.ClaimedCoupon, error) {
	if s.game == nil {
		return nil, domain.E(domain.KindInternal, "UNAVAILABLE", "voucher claims unavailable")
	}
	return s.game.ListClaims(ctx, userID)
}

// CheckInResult is the outcome of a daily check-in.
type CheckInResult struct {
	Streak        int `json:"streak"`
	PointsAwarded int `json:"points_awarded"`
}

// PointsPerCheckIn scales with the streak, capped at a 7-day cycle.
func PointsPerCheckIn(streak int) int { return 10 * min(streak, 7) }

// DailyCheckIn records attendance and awards streak-scaled points.
func (s *MarketService) DailyCheckIn(ctx context.Context, userID string) (*CheckInResult, error) {
	if s.game == nil || s.loyalty == nil {
		return nil, domain.E(domain.KindInternal, "UNAVAILABLE", "check-in unavailable")
	}
	streak, err := s.game.CheckIn(ctx, userID)
	if err != nil {
		return nil, err
	}
	awarded := PointsPerCheckIn(streak)
	if err := s.loyalty.Add(ctx, userID, awarded, "checkin", time.Now().UTC().Format("2006-01-02")); err != nil {
		return nil, err
	}
	return &CheckInResult{Streak: streak, PointsAwarded: awarded}, nil
}

// CheckInStatus reports today's state + this month's calendar.
func (s *MarketService) CheckInStatus(ctx context.Context, userID string) (*repository.CheckInStatus, error) {
	if s.game == nil {
		return nil, domain.E(domain.KindInternal, "UNAVAILABLE", "check-in unavailable")
	}
	return s.game.CheckInStatus(ctx, userID)
}

// SpinResult is what the wheel landed on.
type SpinResult struct {
	Type   string         `json:"type"` // coupon | points
	Label  string         `json:"label"`
	Coupon *domain.Coupon `json:"coupon,omitempty"`
	Points int            `json:"points,omitempty"`
}

// SpinWheel plays the daily free game: either a prize coupon (auto-claimed)
// or a points drop. One spin per calendar day.
func (s *MarketService) SpinWheel(ctx context.Context, userID string) (*SpinResult, error) {
	if s.game == nil || s.loyalty == nil {
		return nil, domain.E(domain.KindInternal, "UNAVAILABLE", "game unavailable")
	}
	ok, err := s.game.CanSpin(ctx, userID)
	if err != nil {
		return nil, err
	}
	if !ok {
		return nil, domain.E(domain.KindConflict, "SPIN_USED", "spin gratis hari ini sudah dipakai — kembali besok!")
	}
	coupon, cerr := s.game.RandomPrizeCoupon(ctx)
	if cerr == nil {
		if err := s.game.GrantPrizeCoupon(ctx, userID, coupon.ID); err != nil {
			return nil, err
		}
		if err := s.game.RecordSpin(ctx, userID, "coupon", coupon.ID, 0); err != nil {
			return nil, err
		}
		label := "Kupon " + coupon.Code
		switch coupon.Type {
		case "percent":
			label = fmt.Sprintf("%v%% off — %s", trimFloat(coupon.Value), coupon.Code)
		case "fixed":
			label = fmt.Sprintf("Rp %.0f off — %s", coupon.Value, coupon.Code)
		}
		return &SpinResult{Type: "coupon", Label: label, Coupon: coupon}, nil
	}
	points := 20 + rand.Intn(81) // 20–100 points drop
	if err := s.loyalty.Add(ctx, userID, points, "wheel_prize", ""); err != nil {
		return nil, err
	}
	if err := s.game.RecordSpin(ctx, userID, "points", "", points); err != nil {
		return nil, err
	}
	return &SpinResult{Type: "points", Label: fmt.Sprintf("+%d Poin", points), Points: points}, nil
}

// WheelStatus reports spin availability.
func (s *MarketService) WheelStatus(ctx context.Context, userID string) (map[string]bool, error) {
	ok, err := s.game.CanSpin(ctx, userID)
	if err != nil {
		return nil, err
	}
	return map[string]bool{"can_spin": ok}, nil
}

func trimFloat(v float64) string {
	s := fmt.Sprintf("%.0f", v)
	return strings.TrimRight(strings.TrimRight(s, "0"), ".")
}

// SetNotificationService enables price-drop alerts.
func (s *MarketService) SetNotificationService(n *NotificationService) { s.notifs = n }

// SetUsers enables dispute resolution emails.
func (s *MarketService) SetUsers(u *repository.UserRepository) { s.users = u }

// SetMailer enables dispute resolution emails.
func (s *MarketService) SetMailer(m *mail.Client, webURL string) { s.mailer = m; s.webURL = webURL }

// SetLoyalty enables loyalty and disputes.
func (s *MarketService) SetLoyalty(l *repository.LoyaltyRepository, d *repository.DisputeRepository) {
	s.loyalty = l
	s.disputes = d
}

// SetPaymentService enables dispute decisions with real money effects
// (buyer-win full refunds, split settlements).
func (s *MarketService) SetPaymentService(p *PaymentService) { s.payments = p }

// --- Q&A ---

// AskQuestion posts a buyer question.
func (s *MarketService) AskQuestion(ctx context.Context, productID, userID, question string) (*domain.ProductQA, error) {
	if strings.TrimSpace(question) == "" {
		return nil, domain.E(domain.KindInvalid, "QUESTION_REQUIRED", "question is required")
	}
	q := &domain.ProductQA{
		ID: uuid.NewString(), ProductID: productID, UserID: userID, Question: strings.TrimSpace(question),
	}
	if err := s.market.Ask(ctx, q.ID, q.ProductID, q.UserID, q.Question); err != nil {
		return nil, err
	}
	return q, nil
}

// AnswerQuestion records a seller answer.
// SellerQuestions lists questions across the seller's products
// (unanswered first) — the Seller Center Q&A inbox.
func (s *MarketService) SellerQuestions(ctx context.Context, sellerID string, limit int) ([]*domain.ProductQA, error) {
	return s.market.QABySeller(ctx, sellerID, limit)
}

func (s *MarketService) AnswerQuestion(ctx context.Context, qaID, sellerID, answer string) (*domain.ProductQA, error) {
	ok, err := s.market.QASeller(ctx, qaID, sellerID)
	if err != nil {
		return nil, err
	}
	if !ok {
		return nil, domain.E(domain.KindForbidden, "NOT_OWNED", "only the product seller may answer")
	}
	if err := s.market.Answer(ctx, qaID, sellerID, strings.TrimSpace(answer)); err != nil {
		return nil, err
	}
	return s.market.QAByID(ctx, qaID)
}

// QAByProduct lists questions (public).
func (s *MarketService) QAByProduct(ctx context.Context, productID string, limit int) ([]*domain.ProductQA, error) {
	return s.market.QAByProduct(ctx, productID, limit)
}

// --- bundles ---

// CreateBundle adds a seller bundle.
func (s *MarketService) CreateBundle(ctx context.Context, b *domain.Bundle, items []domain.BundleItem) (*domain.Bundle, error) {
	if strings.TrimSpace(b.Name) == "" || len(items) == 0 {
		return nil, domain.E(domain.KindInvalid, "INCOMPLETE", "bundle needs a name and at least one item")
	}
	b.ID = uuid.NewString()
	if err := s.market.CreateBundle(ctx, b, items); err != nil {
		return nil, err
	}
	return b, nil
}

// Bundles lists active bundles.
func (s *MarketService) Bundles(ctx context.Context, sellerID string) ([]*domain.Bundle, error) {
	return s.market.Bundles(ctx, sellerID)
}

// --- price alerts ---

// WatchPrice registers a price alert.
func (s *MarketService) WatchPrice(ctx context.Context, userID, variantID string, targetPrice float64) (*domain.PriceAlert, error) {
	if targetPrice <= 0 {
		return nil, domain.E(domain.KindInvalid, "BAD_PRICE", "target price must be positive")
	}
	a := &domain.PriceAlert{
		ID: uuid.NewString(), UserID: userID, VariantID: variantID,
		TargetPrice: targetPrice, Status: "active",
	}
	if err := s.market.CreatePriceAlert(ctx, a); err != nil {
		return nil, err
	}
	return a, nil
}

// MyPriceAlerts lists the user's watches.
func (s *MarketService) MyPriceAlerts(ctx context.Context, userID string) ([]*domain.PriceAlert, error) {
	return s.market.PriceAlerts(ctx, userID)
}

// CancelPriceAlert removes a watch.
func (s *MarketService) CancelPriceAlert(ctx context.Context, alertID, userID string) error {
	return s.market.CancelPriceAlert(ctx, alertID, userID)
}

// ProcessPriceAlerts fires notifications for triggered alerts (worker).
func (s *MarketService) ProcessPriceAlerts(ctx context.Context, limit int) (int, error) {
	alerts, err := s.market.TriggeredAlerts(ctx, limit)
	if err != nil {
		return 0, err
	}
	if len(alerts) == 0 {
		return 0, nil
	}
	ids := make([]string, 0, len(alerts))
	for _, a := range alerts {
		ids = append(ids, a.ID)
		if s.notifs != nil {
			_ = s.notifs.Notify(ctx, a.UserID, "price_alert", "Harga turun! ⚡",
				a.ProductName+" kini "+formatIDR(a.CurrentPrice)+" (target: "+formatIDR(a.TargetPrice)+")",
				map[string]any{"variant_id": a.VariantID, "product_slug": a.ProductSlug})
		}
	}
	if err := s.market.MarkAlertsTriggered(ctx, ids); err != nil {
		return 0, err
	}
	return len(alerts), nil
}

// --- back-in-stock alerts ---

// WatchRestock registers a restock alert for a variant.
func (s *MarketService) WatchRestock(ctx context.Context, userID, variantID string) (*domain.BackInStockAlert, error) {
	a := &domain.BackInStockAlert{
		ID: uuid.NewString(), UserID: userID, VariantID: variantID, Status: "active",
	}
	if err := s.market.CreateBackInStock(ctx, a); err != nil {
		return nil, err
	}
	return a, nil
}

// MyBackInStockAlerts lists the user's restock watches.
func (s *MarketService) MyBackInStockAlerts(ctx context.Context, userID string) ([]*domain.BackInStockAlert, error) {
	return s.market.BackInStockAlerts(ctx, userID)
}

// CancelBackInStock removes a restock watch.
func (s *MarketService) CancelBackInStock(ctx context.Context, alertID, userID string) error {
	return s.market.CancelBackInStock(ctx, alertID, userID)
}

// ProcessBackInStock fires notifications for restocked variants (worker).
func (s *MarketService) ProcessBackInStock(ctx context.Context, limit int) (int, error) {
	alerts, err := s.market.RestockedAlerts(ctx, limit)
	if err != nil {
		return 0, err
	}
	if len(alerts) == 0 {
		return 0, nil
	}
	ids := make([]string, 0, len(alerts))
	for _, a := range alerts {
		ids = append(ids, a.ID)
		if s.notifs != nil {
			_ = s.notifs.Notify(ctx, a.UserID, "back_in_stock", "Stok tersedia lagi! ✅",
				a.ProductName+" ("+a.VariantName+") sudah bisa dipesan",
				map[string]any{"variant_id": a.VariantID, "product_slug": a.ProductSlug})
		}
	}
	if err := s.market.MarkBackInStockTriggered(ctx, ids); err != nil {
		return 0, err
	}
	return len(alerts), nil
}

// --- flash sale admin ---

// CreateFlashSale (admin).
func (s *MarketService) CreateFlashSale(ctx context.Context, name, description string, startsAt, endsAt time.Time) (*domain.FlashSale, error) {
	if strings.TrimSpace(name) == "" || !endsAt.After(startsAt) {
		return nil, domain.E(domain.KindInvalid, "BAD_RANGE", "sale needs a name and a valid time range")
	}
	return s.market.CreateFlashSale(ctx, name, description, startsAt, endsAt)
}

// AddFlashSaleItems (admin).
func (s *MarketService) AddFlashSaleItems(ctx context.Context, saleID string, items []domain.FlashSaleItem) error {
	if len(items) == 0 {
		return domain.E(domain.KindInvalid, "ITEMS_REQUIRED", "at least one item required")
	}
	return s.market.AddFlashSaleItems(ctx, saleID, items)
}

// ListFlashSales (admin).
func (s *MarketService) ListFlashSales(ctx context.Context) ([]*domain.FlashSale, error) {
	return s.market.ListFlashSales(ctx)
}

// SetFlashSaleActive (admin).
func (s *MarketService) SetFlashSaleActive(ctx context.Context, saleID string, active bool) error {
	return s.market.SetFlashSaleActive(ctx, saleID, active)
}

// --- loyalty ---

// LoyaltyBalance returns points + history.
func (s *MarketService) LoyaltyBalance(ctx context.Context, userID string) (int, []*domain.LoyaltyEntry, error) {
	bal, err := s.loyalty.Balance(ctx, userID)
	if err != nil {
		return 0, nil, err
	}
	ledger, err := s.loyalty.Ledger(ctx, userID, 20)
	return bal, ledger, err
}

// AwardPoints credits points to a user (e.g. purchase completion).
func (s *MarketService) AwardPoints(ctx context.Context, userID string, points int, reason, refID string) error {
	return s.loyalty.Add(ctx, userID, points, reason, refID)
}

// --- referrals ---

// MyReferralCode returns the user's invite code.
func (s *MarketService) MyReferralCode(ctx context.Context, userID string) (string, error) {
	return s.loyalty.ReferralCode(ctx, userID)
}

// RedeemReferral applies a referral code during registration: both parties earn points.
func (s *MarketService) RedeemReferral(ctx context.Context, newUserID, code string) error {
	referrer, err := s.loyalty.ReferralByCode(ctx, code)
	if err != nil {
		return err
	}
	if referrer == newUserID {
		return domain.E(domain.KindInvalid, "SELF_REFERRAL", "cannot use your own code")
	}
	if err := s.loyalty.Add(ctx, newUserID, 500, "referral_bonus", referrer); err != nil {
		return err
	}
	return s.loyalty.Add(ctx, referrer, 500, "referral_reward", newUserID)
}

// --- disputes ---

// OpenDispute escalates a return into a platform dispute. Only the buyer who
// owns the return may open a dispute on it.
func (s *MarketService) OpenDispute(ctx context.Context, returnID, userID, subject, description string) (*domain.Dispute, error) {
	if s.disputes == nil {
		return nil, domain.E(domain.KindConflict, "UNAVAILABLE", "disputes unavailable")
	}
	// resolve return details and verify ownership
	var buyerID, orderID, sellerID string
	err := s.market.ReturnBuyerOrderSeller(ctx, returnID, &buyerID, &orderID, &sellerID)
	if err != nil {
		return nil, err
	}
	if buyerID != userID {
		return nil, domain.E(domain.KindForbidden, "NOT_OWNED", "return does not belong to user")
	}
	d := &domain.Dispute{
		ID: uuid.NewString(), ReturnID: &returnID, OrderID: orderID,
		UserID: userID, SellerID: sellerID, Subject: subject, Description: description, Status: "open",
	}
	if err := s.disputes.Create(ctx, d); err != nil {
		return nil, err
	}
	return d, nil
}

// Disputes lists disputes (admin).
func (s *MarketService) Disputes(ctx context.Context, status string) ([]*domain.Dispute, error) {
	return s.disputes.ListByStatus(ctx, status)
}

// ResolveDispute decides a dispute (admin). Decisions carry REAL money
// effects: "buyer" triggers a full refund through the escrow pipeline;
// "split" credits the buyer half from the platform wallet; "seller"/"none"
// move nothing.
func (s *MarketService) ResolveDispute(ctx context.Context, disputeID, decision, note string) error {
	switch decision {
	case "buyer", "seller", "split", "none":
	default:
		return domain.E(domain.KindInvalid, "BAD_DECISION", "decision must be buyer, seller, split or none")
	}
	d, err := s.disputes.DisputeByID(ctx, disputeID)
	if err != nil {
		return err
	}
	if err := s.disputes.Resolve(ctx, disputeID, decision, note); err != nil {
		return err
	}
	if s.payments != nil {
		switch decision {
		case "buyer":
			if err := s.payments.RefundOrder(ctx, d.OrderID, "dispute resolution: buyer wins ("+disputeID+")", true); err != nil {
				return err
			}
		case "split":
			if err := s.payments.DisputeSplitCredit(ctx, d.OrderID); err != nil {
				return err
			}
		}
	}
	s.emailDisputeOutcome(ctx, disputeID, decision)
	return nil
}

func (s *MarketService) emailDisputeOutcome(ctx context.Context, disputeID, decision string) {
	if s.mailer == nil || s.users == nil {
		return
	}
	d, err := s.disputes.DisputeByID(ctx, disputeID)
	if err != nil {
		return
	}
	subject := "Hasil sengketa — VinCommerce"
	data := map[string]any{"Decision": decision}
	for _, uid := range []string{d.UserID, d.SellerID} {
		if uid == "" {
			continue
		}
		u, err := s.users.ByID(ctx, uid)
		if err != nil {
			continue
		}
		body := map[string]any{}
		for k, v := range data {
			body[k] = v
		}
		body["Name"] = u.FullName
		body["Subject"] = d.Subject
		_ = s.mailer.Send(ctx, u.Email, subject, "dispute_resolved", body)
	}
}

func formatIDR(v float64) string {
	return "Rp " + fmt.Sprintf("%.0f", v)
}
