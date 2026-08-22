package repository

import (
	"context"
	"errors"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/vincommerce/backend/internal/db"
	"github.com/vincommerce/backend/internal/domain"
)

// GamificationRepository persists voucher claims, check-in streaks and
// spin-the-wheel plays.
type GamificationRepository struct {
	pool *db.Pool
}

// NewGamificationRepository creates a GamificationRepository.
func NewGamificationRepository(pool *db.Pool) *GamificationRepository {
	return &GamificationRepository{pool: pool}
}

// ClaimedCoupon is a coupon a user has claimed to their account.
type ClaimedCoupon struct {
	CouponID    string     `json:"coupon_id"`
	Code        string     `json:"code"`
	Type        string     `json:"type"`
	Value       float64    `json:"value"`
	MinSubtotal float64    `json:"min_subtotal"`
	StoreName   string     `json:"store_name,omitempty"`
	ClaimedAt   time.Time  `json:"claimed_at"`
}

const couponCols = `id, code, type, value, min_subtotal,
	max_discount, usage_limit, used_count, per_user_limit, valid_from, valid_until, is_active`

// ClaimCoupon attaches an active coupon to a user's account.
func (r *GamificationRepository) ClaimCoupon(ctx context.Context, userID, code string) (*domain.Coupon, error) {
	c, err := scanCoupon(r.pool.QueryRow(ctx,
		`SELECT `+couponCols+` FROM coupons WHERE UPPER(code) = UPPER($1) AND is_active`, code))
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, domain.E(domain.KindNotFound, "COUPON_NOT_FOUND", "kupon tidak ditemukan atau tidak aktif")
	}
	if err != nil {
		return nil, err
	}
	tag, err := r.pool.Exec(ctx, `
		INSERT INTO claimed_coupons (user_id, coupon_id) VALUES ($1, $2)
		ON CONFLICT (user_id, coupon_id) DO NOTHING`, userID, c.ID)
	if err != nil {
		return nil, err
	}
	if tag.RowsAffected() == 0 {
		return nil, domain.E(domain.KindConflict, "ALREADY_CLAIMED", "kupon ini sudah ada di akunmu")
	}
	return c, nil
}

// ListClaims returns the user's claimed coupons (newest first).
func (r *GamificationRepository) ListClaims(ctx context.Context, userID string) ([]*ClaimedCoupon, error) {
	rows, err := r.pool.Query(ctx, `
		SELECT cc.coupon_id, cp.code, cp.type, cp.value, cp.min_subtotal,
		       COALESCE(s.name, ''), cc.claimed_at
		FROM claimed_coupons cc
		JOIN coupons cp ON cp.id = cc.coupon_id
		LEFT JOIN stores s ON s.id = cp.seller_id
		WHERE cc.user_id = $1
		ORDER BY cc.claimed_at DESC`, userID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	out := []*ClaimedCoupon{}
	for rows.Next() {
		var cc ClaimedCoupon
		if err := rows.Scan(&cc.CouponID, &cc.Code, &cc.Type, &cc.Value, &cc.MinSubtotal,
			&cc.StoreName, &cc.ClaimedAt); err != nil {
			return nil, err
		}
		out = append(out, &cc)
	}
	return out, rows.Err()
}

// CheckIn records today's attendance and returns the resulting streak.
// Returns conflict when already checked in today.
func (r *GamificationRepository) CheckIn(ctx context.Context, userID string) (int, error) {
	tx, err := r.pool.Begin(ctx)
	if err != nil {
		return 0, err
	}
	defer tx.Rollback(ctx)

	var prev int
	yesterday := time.Now().UTC().AddDate(0, 0, -1).Format("2006-01-02")
	err = tx.QueryRow(ctx,
		`SELECT streak FROM check_ins WHERE user_id = $1 AND checkin_date = $2`,
		userID, yesterday).Scan(&prev)
	if err != nil && !errors.Is(err, pgx.ErrNoRows) {
		return 0, err
	}
	streak := prev + 1

	tag, err := tx.Exec(ctx, `
		INSERT INTO check_ins (user_id, checkin_date, streak)
		VALUES ($1, CURRENT_DATE, $2)
		ON CONFLICT (user_id, checkin_date) DO NOTHING`, userID, streak)
	if err != nil {
		return 0, err
	}
	if tag.RowsAffected() == 0 {
		return 0, domain.E(domain.KindConflict, "ALREADY_CHECKED_IN", "kamu sudah check-in hari ini")
	}
	return streak, tx.Commit(ctx)
}

// CheckInStatus reports today's state, current streak and this month's dates.
type CheckInStatus struct {
	CheckedToday bool     `json:"checked_in_today"`
	Streak       int      `json:"streak"`
	Dates        []string `json:"dates"`
}

// CheckInStatus loads the check-in dashboard payload.
func (r *GamificationRepository) CheckInStatus(ctx context.Context, userID string) (*CheckInStatus, error) {
	st := &CheckInStatus{Dates: []string{}}

	var todayRow bool
	err := r.pool.QueryRow(ctx,
		`SELECT EXISTS (SELECT 1 FROM check_ins WHERE user_id = $1 AND checkin_date = CURRENT_DATE)`,
		userID).Scan(&todayRow)
	if err != nil {
		return nil, err
	}
	st.CheckedToday = todayRow

	// current streak: walk back from today/yesterday
	base := time.Now().UTC()
	if !todayRow {
		base = base.AddDate(0, 0, -1)
	}
	for i := 0; i < 365; i++ {
		d := base.AddDate(0, 0, -i).Format("2006-01-02")
		var exists bool
		if err := r.pool.QueryRow(ctx,
			`SELECT EXISTS (SELECT 1 FROM check_ins WHERE user_id = $1 AND checkin_date = $2)`,
			userID, d).Scan(&exists); err != nil {
			return nil, err
		}
		if !exists {
			break
		}
		st.Streak++
	}

	rows, err := r.pool.Query(ctx, `
		SELECT to_char(checkin_date, 'YYYY-MM-DD') FROM check_ins
		WHERE user_id = $1 AND checkin_date >= date_trunc('month', CURRENT_DATE)::date
		ORDER BY checkin_date`, userID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	for rows.Next() {
		var d string
		if err := rows.Scan(&d); err != nil {
			return nil, err
		}
		st.Dates = append(st.Dates, d)
	}
	return st, rows.Err()
}

// CanSpin reports whether the daily free spin is available.
func (r *GamificationRepository) CanSpin(ctx context.Context, userID string) (bool, error) {
	var n int
	err := r.pool.QueryRow(ctx,
		`SELECT COUNT(*)::int FROM game_spins WHERE user_id = $1 AND spun_at >= CURRENT_DATE`,
		userID).Scan(&n)
	return n == 0, err
}

// RecordSpin persists a spin result.
func (r *GamificationRepository) RecordSpin(ctx context.Context, userID, prizeType, prizeCouponID string, prizePoints int) error {
	_, err := r.pool.Exec(ctx, `
		INSERT INTO game_spins (user_id, prize_type, prize_coupon_id, prize_points)
		VALUES ($1, $2, NULLIF($3, ''), $4)`, userID, prizeType, prizeCouponID, prizePoints)
	return err
}

// RandomPrizeCoupon picks one active platform coupon flagged as a wheel prize.
func (r *GamificationRepository) RandomPrizeCoupon(ctx context.Context) (*domain.Coupon, error) {
	c, err := scanCoupon(r.pool.QueryRow(ctx,
		`SELECT `+couponCols+` FROM coupons WHERE is_prize AND is_active ORDER BY random() LIMIT 1`))
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, domain.E(domain.KindNotFound, "NO_PRIZES", "tidak ada hadiah kupon tersedia")
	}
	if err != nil {
		return nil, err
	}
	return c, nil
}

// GrantPrizeCoupon claims a won coupon bypassing the duplicate guard.
func (r *GamificationRepository) GrantPrizeCoupon(ctx context.Context, userID, couponID string) error {
	_, err := r.pool.Exec(ctx, `
		INSERT INTO claimed_coupons (user_id, coupon_id) VALUES ($1, $2)
		ON CONFLICT (user_id, coupon_id) DO UPDATE SET claimed_at = now()`, userID, couponID)
	return err
}
