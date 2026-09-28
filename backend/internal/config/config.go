package config

import (
	"fmt"
	"strings"
	"time"

	"github.com/caarlos0/env/v11"
)

// Config holds all runtime configuration, loaded from environment variables.
type Config struct {
	Environment string        `env:"APP_ENV" envDefault:"development"`
	Port        int           `env:"PORT" envDefault:"8080"`
	ShutdownGap time.Duration `env:"SHUTDOWN_GAP" envDefault:"10s"`

	// TrustedProxyCIDRs are the networks whose X-Forwarded-For / X-Real-IP
	// headers may be believed when resolving the client IP. Empty means NO
	// proxy is trusted and the transport peer is used, which is the safe
	// default: it degrades to "cannot see through the proxy" rather than
	// "trusts any client that sends a header".
	//
	// Getting this wrong is the difference between a working rate limiter and
	// no rate limiter at all: trusting everything lets a client mint a fresh
	// bucket per request by forging the header.
	TrustedProxyCIDRs []string `env:"TRUSTED_PROXY_CIDRS" envSeparator:","`

	Database DatabaseConfig
	Redis    RedisConfig
	Auth     AuthConfig
	SMTP     SMTPConfig
	Payments PaymentConfig
	OAuth    OAuthConfig
	AI       AIConfig
	App      AppConfig
}

// PaymentConfig selects the gateway adapter.
type PaymentConfig struct {
	Gateway        string `env:"PAYMENT_GATEWAY" envDefault:"sandbox"`
	SandboxBaseURL string `env:"PAYMENT_SANDBOX_BASE_URL" envDefault:"http://localhost:8080/api/v1"`

	MidtransServerKey string `env:"MIDTRANS_SERVER_KEY" envDefault:""`
	MidtransClientKey string `env:"MIDTRANS_CLIENT_KEY" envDefault:""`
	MidtransEnv       string `env:"MIDTRANS_ENV" envDefault:"sandbox"` // sandbox | production
	// Comma-separated allow-list of Midtrans payment channels
	// (gopay,qris,bank_transfer,credit_card,kredivo,akulaku,...). Empty = all.
	EnabledMethods string `env:"PAYMENT_ENABLED_METHODS" envDefault:""`
	// Optional shipping insurance: percentage of bundle subtotal charged when
	// the buyer opts in at checkout. 0 disables the feature.
	ShippingInsurancePct float64 `env:"SHIPPING_INSURANCE_PCT" envDefault:"0.3"`
	// Auto-approve return claims whose item value is at or below this amount.
	// 0 disables instant approval.
	ReturnAutoApproveMax float64 `env:"RETURN_AUTO_APPROVE_MAX" envDefault:"50000"`
}

// OAuthConfig enables social login (Google).
type OAuthConfig struct {
	GoogleClientID     string `env:"GOOGLE_CLIENT_ID" envDefault:""`
	GoogleClientSecret string `env:"GOOGLE_CLIENT_SECRET" envDefault:""`
	GoogleRedirectURL  string `env:"GOOGLE_REDIRECT_URL" envDefault:"http://localhost:5173/oauth/google/callback"`
}

// AIConfig enables the LLM-backed assistant. Leave AI_API_KEY empty for offline mode.
type AIConfig struct {
	BaseURL string `env:"AI_BASE_URL" envDefault:"https://api.openai.com/v1"`
	APIKey  string `env:"AI_API_KEY" envDefault:""`
	Model   string `env:"AI_MODEL" envDefault:"gpt-4o-mini"`
}

type DatabaseConfig struct {
	Host     string `env:"DB_HOST" envDefault:"localhost"`
	Port     int    `env:"DB_PORT" envDefault:"5432"`
	User     string `env:"DB_USER" envDefault:"vincom"`
	Password string `env:"DB_PASSWORD" envDefault:"vincom_dev"`
	Name     string `env:"DB_NAME" envDefault:"vincom"`
	SSLMode  string `env:"DB_SSL_MODE" envDefault:"disable"`

	MaxConns int           `env:"DB_MAX_CONNS" envDefault:"20"`
	MinConns int           `env:"DB_MIN_CONNS" envDefault:"2"`
	MaxIdle  time.Duration `env:"DB_MAX_IDLE" envDefault:"5m"`
	MaxLife  time.Duration `env:"DB_MAX_LIFE" envDefault:"30m"`

	MigrateOnStart bool `env:"DB_MIGRATE_ON_START" envDefault:"true"`
}

func (d DatabaseConfig) DSN() string {
	return fmt.Sprintf(
		"host=%s port=%d user=%s password=%s dbname=%s sslmode=%s",
		d.Host, d.Port, d.User, d.Password, d.Name, d.SSLMode,
	)
}

type RedisConfig struct {
	Addr     string `env:"REDIS_ADDR" envDefault:"127.0.0.1:6379"`
	Password string `env:"REDIS_PASSWORD" envDefault:""`
	DB       int    `env:"REDIS_DB" envDefault:"0"`
}

type AuthConfig struct {
	JWTSecret         string        `env:"JWT_SECRET" envDefault:"dev-secret-change-me"`
	AccessTokenTTL    time.Duration `env:"ACCESS_TOKEN_TTL" envDefault:"15m"`
	RefreshTokenTTL   time.Duration `env:"REFRESH_TOKEN_TTL" envDefault:"720h"`
	Argon2Memory      uint32        `env:"ARGON2_MEMORY" envDefault:"65536"`
	Argon2Iterations  uint32        `env:"ARGON2_ITERATIONS" envDefault:"3"`
	Argon2Parallelism uint8         `env:"ARGON2_PARALLELISM" envDefault:"2"`
	Argon2SaltLength  uint32        `env:"ARGON2_SALT_LENGTH" envDefault:"16"`
}

type AppConfig struct {
	BaseURL   string `env:"APP_BASE_URL" envDefault:"http://localhost:8080"`
	WebURL    string `env:"WEB_URL" envDefault:"http://localhost:5173"`
	CORSAllow string `env:"CORS_ALLOW_ORIGINS" envDefault:"http://localhost:5173"`
	UploadDir string `env:"UPLOAD_DIR" envDefault:"./data/uploads"`
}

// SplitOrigins returns the allowed CORS origins as a slice.
func (a AppConfig) SplitOrigins() []string {
	return strings.Split(a.CORSAllow, ",")
}

// SMTPConfig configures transactional email delivery.
type SMTPConfig struct {
	Host     string `env:"SMTP_HOST" envDefault:""`
	Port     int    `env:"SMTP_PORT" envDefault:"1025"`
	From     string `env:"SMTP_FROM" envDefault:"VinCommerce <no-reply@vincommerce.local>"`
	Username string `env:"SMTP_USERNAME" envDefault:""`
	Password string `env:"SMTP_PASSWORD" envDefault:""`
}

// LoadRedis parses ONLY the Redis settings.
//
// The worker's container health probe needs nothing else, and running the
// full Validate() there would couple queue liveness to secrets the worker
// never uses. Validate() rejects a weak JWT_SECRET in staging/production,
// but the worker signs no tokens: giving the probe the full config would
// mark a worker with a perfectly healthy queue as unhealthy purely because
// JWT_SECRET was not injected into the worker's environment.
func LoadRedis() (RedisConfig, error) {
	var r RedisConfig
	if err := env.Parse(&r); err != nil {
		return RedisConfig{}, fmt.Errorf("parse redis config: %w", err)
	}
	return r, nil
}

// Load reads configuration from environment variables.
func Load() (*Config, error) {
	var cfg Config
	if err := env.Parse(&cfg); err != nil {
		return nil, fmt.Errorf("parse config: %w", err)
	}
	if err := cfg.Validate(); err != nil {
		return nil, err
	}
	return &cfg, nil
}

// Environment is the normalised deployment environment.
type Environment string

const (
	EnvDevelopment Environment = "development"
	EnvStaging     Environment = "staging"
	EnvProduction  Environment = "production"
)

// ParseEnvironment normalises APP_ENV. An unrecognised value is a hard
// startup error rather than a silent fall-through to "not production" —
// otherwise `APP_ENV=prod` or a trailing space disables every safety guard.
func ParseEnvironment(raw string) (Environment, error) {
	switch strings.ToLower(strings.TrimSpace(raw)) {
	case "", "development", "dev", "local":
		return EnvDevelopment, nil
	case "staging", "stage":
		return EnvStaging, nil
	case "production", "prod":
		return EnvProduction, nil
	default:
		return "", fmt.Errorf("APP_ENV must be one of development|staging|production, got %q", raw)
	}
}

// IsProduction reports whether the stricter production guards apply.
// Staging is deliberately included: it is an internet-facing environment.
func (e Environment) IsProduction() bool { return e == EnvProduction || e == EnvStaging }

// IsDevelopment reports whether dev-only affordances (sandbox gateway,
// sandbox payment routes) may be mounted.
func (e Environment) IsDevelopment() bool { return e == EnvDevelopment }

// IsDev reports whether development-only affordances may be mounted. Callers
// must run after Validate(), which normalises Environment to a canonical value.
func (c *Config) IsDev() bool { return c.Environment == string(EnvDevelopment) }

// IsProd reports whether production/staging guards apply. Staging counts: it
// is an internet-facing environment with real user data.
func (c *Config) IsProd() bool {
	return c.Environment == string(EnvProduction) || c.Environment == string(EnvStaging)
}

// knownPublicSecrets is the deny-list of secret values that appear in this
// repository (compose files, .env.example, docs, git history). Shipping any
// of them means the signing key is public, so they are rejected in EVERY
// environment where the value would be used to authenticate real traffic.
var knownPublicSecrets = []string{
	"dev-secret-change-me",
	"local-dev-secret-please-change-me-32chars",
	"vincommerce-dev-secret-change-me",
	"super-secret-jwt-key-change-in-production",
	"changeme",
	"sandbox-webhook-secret",
	"vincom_dev",
}

// isKnownPublicSecret reports whether secret matches a value published in
// this repository. Comparison is case-insensitive because these values are
// only ever compared for equality, never used as a live credential.
func isKnownPublicSecret(secret string) bool {
	needle := strings.ToLower(strings.TrimSpace(secret))
	if needle == "" {
		return false
	}
	for _, pub := range knownPublicSecrets {
		if needle == pub {
			return true
		}
	}
	return false
}

// weakSecret reports whether a secret is too weak to authenticate real
// traffic: shorter than 32 bytes, low-entropy filler, or a published value.
func weakSecret(secret string) bool {
	s := strings.TrimSpace(secret)
	if len(s) < 32 {
		return true
	}
	if isKnownPublicSecret(s) {
		return true
	}
	// Reject a single repeated character / obvious filler. Count distinct
	// runes; a real random secret has close to len(s) of them.
	distinct := 0
	seen := make(map[rune]bool, len(s))
	for _, r := range s {
		if !seen[r] {
			seen[r] = true
			distinct++
		}
	}
	return distinct < 8
}

// IsWeakSecret exposes weakSecret to the binaries for belt-and-braces checks
// after config.Load has already run.
func IsWeakSecret(secret string) bool { return weakSecret(secret) }

// IsKnownPublicSecret reports whether a value is published in this repository.
func IsKnownPublicSecret(secret string) bool { return isKnownPublicSecret(secret) }

// Validate enforces environment safety guards for every binary (api, worker,
// seed) so a misconfigured deployment fails loudly at startup instead of
// running with development defaults.
func (c *Config) Validate() error {
	env, err := ParseEnvironment(c.Environment)
	if err != nil {
		return err
	}
	// Normalise so downstream comparisons never see a raw, untrimmed value.
	c.Environment = string(env)

	prod := env.IsProduction()

	// The JWT signing key guards every authenticated route, so a published or
	// low-entropy value is rejected in any non-development environment. This
	// used to compare against a single sentinel, which the compose default
	// (41 chars, different text) sailed straight past.
	if prod && weakSecret(c.Auth.JWTSecret) {
		if isKnownPublicSecret(c.Auth.JWTSecret) {
			return fmt.Errorf("JWT_SECRET is a value published in this repository; " +
				"generate a unique one, e.g. `openssl rand -base64 48`")
		}
		return fmt.Errorf("JWT_SECRET must be set to a strong random value (>=32 chars, high entropy) in %s", env)
	}

	if c.Payments.Gateway == "" || c.Payments.Gateway == "sandbox" {
		if prod {
			return fmt.Errorf("PAYMENT_GATEWAY=sandbox is not allowed in %s; configure 'midtrans' or another real gateway", env)
		}
	}
	if c.Payments.MidtransEnv == "production" && !prod {
		return fmt.Errorf("MIDTRANS_ENV=production requires APP_ENV=production")
	}

	if prod && c.Database.SSLMode == "disable" {
		return fmt.Errorf("DB_SSL_MODE=disable is not allowed in %s; use 'require' or 'verify-full'", env)
	}
	if prod && isKnownPublicSecret(c.Database.Password) {
		return fmt.Errorf("DB_PASSWORD must be changed from the development default in %s", env)
	}

	switch c.Payments.MidtransEnv {
	case "", "sandbox", "production":
	default:
		return fmt.Errorf("MIDTRANS_ENV must be 'sandbox' or 'production', got %q", c.Payments.MidtransEnv)
	}
	switch c.Payments.Gateway {
	case "", "sandbox", "midtrans":
	default:
		return fmt.Errorf("unknown PAYMENT_GATEWAY %q (supported: sandbox, midtrans)", c.Payments.Gateway)
	}
	return nil
}
