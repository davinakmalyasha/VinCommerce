-- +goose Up
CREATE TABLE IF NOT EXISTS claimed_coupons (
    id         UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    user_id    UUID NOT NULL REFERENCES users (id) ON DELETE CASCADE,
    coupon_id  UUID NOT NULL REFERENCES coupons (id) ON DELETE CASCADE,
    claimed_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    used_at    TIMESTAMPTZ,
    UNIQUE (user_id, coupon_id)
);
CREATE INDEX IF NOT EXISTS idx_claimed_coupons_user ON claimed_coupons (user_id);

CREATE TABLE IF NOT EXISTS check_ins (
    id           UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    user_id      UUID NOT NULL REFERENCES users (id) ON DELETE CASCADE,
    checkin_date DATE NOT NULL,
    streak       INT NOT NULL DEFAULT 1,
    created_at   TIMESTAMPTZ NOT NULL DEFAULT now(),
    UNIQUE (user_id, checkin_date)
);
CREATE INDEX IF NOT EXISTS idx_check_ins_user ON check_ins (user_id, checkin_date DESC);

CREATE TABLE IF NOT EXISTS game_spins (
    id              UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    user_id         UUID NOT NULL REFERENCES users (id) ON DELETE CASCADE,
    prize_type      VARCHAR(8) NOT NULL CHECK (prize_type IN ('coupon', 'points')),
    prize_coupon_id UUID REFERENCES coupons (id),
    prize_points    INT NOT NULL DEFAULT 0,
    spun_at         TIMESTAMPTZ NOT NULL DEFAULT now()
);
CREATE INDEX IF NOT EXISTS idx_game_spins_user ON game_spins (user_id, spun_at DESC);

ALTER TABLE coupons ADD COLUMN IF NOT EXISTS is_prize BOOLEAN NOT NULL DEFAULT FALSE;

-- +goose Down
ALTER TABLE coupons DROP COLUMN IF EXISTS is_prize;
DROP TABLE IF EXISTS game_spins;
DROP TABLE IF EXISTS check_ins;
DROP TABLE IF EXISTS claimed_coupons;
