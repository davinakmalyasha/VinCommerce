package service

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/vincommerce/backend/internal/domain"
	"github.com/vincommerce/backend/internal/repository"
	"golang.org/x/oauth2"
	"golang.org/x/oauth2/google"
)

// OAuthService implements social login (Google).
type OAuthService struct {
	users    *repository.UserRepository
	sessions *repository.SessionRepository
	pass     *Password
	tokens   *TokenManager
	cfg      oauth2.Config
	enabled  bool
}

// NewOAuthService creates the social login service (disabled without credentials).
func NewOAuthService(users *repository.UserRepository, sessions *repository.SessionRepository, pass *Password, tokens *TokenManager, clientID, clientSecret, redirectURL string) *OAuthService {
	cfg := oauth2.Config{
		ClientID:     clientID,
		ClientSecret: clientSecret,
		RedirectURL:  redirectURL,
		Scopes:       []string{"openid", "email", "profile"},
		Endpoint:     google.Endpoint,
	}
	return &OAuthService{
		users: users, sessions: sessions, pass: pass, tokens: tokens,
		cfg: cfg, enabled: clientID != "" && clientSecret != "",
	}
}

// Enabled reports whether OAuth is configured.
func (s *OAuthService) Enabled() bool { return s.enabled }

// AuthURL returns the Google consent URL (state stored for verification).
func (s *OAuthService) AuthURL() (string, string, error) {
	state, err := randomHex(16)
	if err != nil {
		return "", "", err
	}
	return s.cfg.AuthCodeURL(state, oauth2.AccessTypeOffline), state, nil
}

// HandleCallback exchanges the code and provisions the user.
func (s *OAuthService) HandleCallback(ctx context.Context, code string) (*Tokens, error) {
	tok, err := s.cfg.Exchange(ctx, code)
	if err != nil {
		return nil, domain.E(domain.KindUnauthenticated, "OAUTH_FAILED", "unable to exchange code")
	}

	profile, err := fetchGoogleProfile(ctx, s.cfg, tok)
	if err != nil {
		return nil, err
	}
	if profile.Email == "" {
		return nil, domain.E(domain.KindUnauthenticated, "OAUTH_NO_EMAIL", "google account has no email")
	}
	// Refuse unverified Google emails: an attacker can attach someone else's
	// address as an UNVERIFIED secondary email on their own Google account,
	// then use this flow to take over the matching local account.
	if !profile.EmailVerified {
		return nil, domain.E(domain.KindUnauthenticated, "OAUTH_EMAIL_UNVERIFIED",
			"google account email is not verified")
	}

	user, err := s.users.ByEmail(ctx, profile.Email)
	if err != nil {
		// auto-provision: email verified by Google
		user = &domain.User{
			ID:       uuid.NewString(),
			Email:    strings.ToLower(profile.Email),
			FullName: profile.Name,
			Roles:    []string{domain.RoleBuyer},
			Status:   domain.UserStatusActive,
		}
		hash, err := s.pass.Hash(randomPassword())
		if err != nil {
			return nil, err
		}
		if err := s.users.Create(ctx, user, hash); err != nil {
			return nil, err
		}
		if err := s.users.VerifyEmail(ctx, user.ID); err != nil {
			return nil, err
		}
	}

	refresh, refreshHash, err := s.tokens.NewRefreshToken()
	if err != nil {
		return nil, err
	}
	sess := &domain.RefreshSession{
		ID: uuid.NewString(), UserID: user.ID, RefreshHash: refreshHash, FamilyID: uuid.NewString(),
		ExpiresAt: time.Now().UTC().Add(s.tokens.RefreshTTL()),
	}
	if err := s.sessions.Create(ctx, sess); err != nil {
		return nil, err
	}
	return s.issue(ctx, user, refresh)
}

func (s *OAuthService) issue(ctx context.Context, user *domain.User, refreshToken string) (*Tokens, error) {
	access, exp, err := s.tokens.IssueAccess(user)
	if err != nil {
		return nil, err
	}
	return &Tokens{
		AccessToken: access, RefreshToken: refreshToken,
		ExpiresIn: int64(time.Until(exp).Seconds()), ExpiresAt: exp, User: user,
	}, nil
}

type googleProfile struct {
	Email         string `json:"email"`
	Name          string `json:"name"`
	EmailVerified bool   `json:"email_verified"`
}

func fetchGoogleProfile(ctx context.Context, cfg oauth2.Config, tok *oauth2.Token) (*googleProfile, error) {
	client := cfg.Client(ctx, tok)
	resp, err := client.Get("https://openidconnect.googleapis.com/v1/userinfo")
	if err != nil {
		return nil, domain.Wrap(domain.KindInternal, "OAUTH_PROFILE", "unable to fetch profile", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil, domain.E(domain.KindUnauthenticated, "OAUTH_PROFILE", "profile fetch failed")
	}
	var p googleProfile
	if err := json.NewDecoder(io.LimitReader(resp.Body, 1<<20)).Decode(&p); err != nil {
		return nil, domain.E(domain.KindUnauthenticated, "OAUTH_PROFILE", "profile parse failed")
	}
	return &p, nil
}

func randomHex(n int) (string, error) {
	b := make([]byte, n)
	if _, err := rand.Read(b); err != nil {
		return "", err
	}
	return hex.EncodeToString(b), nil
}

func randomPassword() string {
	// Not used for login; satisfies the NOT NULL password_hash column.
	return fmt.Sprintf("oauth-%s", mustHex(24))
}

func mustHex(n int) string {
	s, err := randomHex(n)
	if err != nil {
		panic(err)
	}
	return s
}
