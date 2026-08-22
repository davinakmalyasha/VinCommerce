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

// Load reads configuration from environment variables.
func Load() (*Config, error) {
	var cfg Config
	if err := env.Parse(&cfg); err != nil {
		return nil, fmt.Errorf("parse config: %w", err)
	}
	return &cfg, nil
}
