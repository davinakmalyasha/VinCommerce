package service

import (
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"time"

	"github.com/golang-jwt/jwt/v5"
	"github.com/vincommerce/backend/internal/domain"
)

// TokenManager issues and verifies JWT access tokens and opaque refresh tokens.
type TokenManager struct {
	secret     []byte
	accessTTL  time.Duration
	refreshTTL time.Duration
}

// NewTokenManager creates a TokenManager.
func NewTokenManager(secret string, accessTTL, refreshTTL time.Duration) *TokenManager {
	return &TokenManager{secret: []byte(secret), accessTTL: accessTTL, refreshTTL: refreshTTL}
}

type accessClaims struct {
	Roles []string `json:"roles"`
	Ver   int      `json:"ver"`
	jwt.RegisteredClaims
}

// IssueAccess creates a signed JWT for a user.
func (t *TokenManager) IssueAccess(user *domain.User) (string, time.Time, error) {
	now := time.Now()
	exp := now.Add(t.accessTTL)
	claims := accessClaims{
		Roles: user.Roles,
		Ver:   1,
		RegisteredClaims: jwt.RegisteredClaims{
			Subject:   user.ID,
			IssuedAt:  jwt.NewNumericDate(now),
			ExpiresAt: jwt.NewNumericDate(exp),
			Issuer:    "vincommerce-api",
			Audience:  jwt.ClaimStrings{"vincommerce-web"},
			ID:        newTokenID(),
		},
	}
	token := jwt.NewWithClaims(jwt.SigningMethodHS256, claims)
	signed, err := token.SignedString(t.secret)
	if err != nil {
		return "", time.Time{}, err
	}
	return signed, exp, nil
}

// ParseAccess verifies and decodes a JWT.
func (t *TokenManager) ParseAccess(tokenStr string) (*domain.User, error) {
	claims := &accessClaims{}
	token, err := jwt.ParseWithClaims(tokenStr, claims, func(token *jwt.Token) (any, error) {
		if _, ok := token.Method.(*jwt.SigningMethodHMAC); !ok {
			return nil, domain.ErrTokenInvalid
		}
		return t.secret, nil
	}, jwt.WithIssuer("vincommerce-api"), jwt.WithAudience("vincommerce-web"), jwt.WithValidMethods([]string{"HS256"}))
	if err != nil {
		return nil, domain.ErrTokenInvalid
	}
	if !token.Valid {
		return nil, domain.ErrTokenInvalid
	}
	return &domain.User{ID: claims.Subject, Roles: claims.Roles}, nil
}

// NewRefreshToken generates a random opaque refresh token and its SHA-256 hash.
func (t *TokenManager) NewRefreshToken() (token, hash string, err error) {
	token = newTokenID()
	hash = hashToken(token)
	return token, hash, nil
}

// HashRefresh hashes a refresh token for storage/comparison.
func (t *TokenManager) HashRefresh(token string) string { return hashToken(token) }

// RefreshTTL returns the configured refresh token lifetime.
func (t *TokenManager) RefreshTTL() time.Duration { return t.refreshTTL }

func hashToken(token string) string {
	sum := sha256.Sum256([]byte(token))
	return hex.EncodeToString(sum[:])
}

func newTokenID() string {
	b := make([]byte, 32)
	if _, err := rand.Read(b); err != nil {
		panic(err)
	}
	return hex.EncodeToString(b)
}
