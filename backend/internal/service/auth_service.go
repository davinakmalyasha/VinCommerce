package service

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"net"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/pquerna/otp/totp"
	"github.com/vincommerce/backend/internal/config"
	"github.com/vincommerce/backend/internal/domain"
	"github.com/vincommerce/backend/internal/mail"
	"github.com/vincommerce/backend/internal/repository"
)

// AuthService implements identity use cases.
type AuthService struct {
	users    *repository.UserRepository
	sessions *repository.SessionRepository
	pass     *Password
	tokens   *TokenManager
	mailer   *mail.Client
	cfg      *config.Config
}

// NewAuthService wires identity dependencies.
func NewAuthService(
	users *repository.UserRepository,
	sessions *repository.SessionRepository,
	pass *Password,
	tokens *TokenManager,
	mailer *mail.Client,
	cfg *config.Config,
) *AuthService {
	return &AuthService{users: users, sessions: sessions, pass: pass, tokens: tokens, mailer: mailer, cfg: cfg}
}

// Tokens bundles the results of an authentication.
type Tokens struct {
	AccessToken  string       `json:"access_token"`
	RefreshToken string       `json:"refresh_token"`
	ExpiresIn    int64        `json:"expires_in"`
	ExpiresAt    time.Time    `json:"expires_at"`
	User         *domain.User `json:"user"`
}

// RegisterInput for account creation.
type RegisterInput struct {
	Email      string `json:"email"`
	Phone      string `json:"phone,omitempty"`
	Password   string `json:"password"`
	FullName   string `json:"full_name"`
	DeviceName string `json:"device_name,omitempty"`
	IPAddress  string `json:"-"`
	UserAgent  string `json:"-"`
}

// Register creates a buyer account and issues tokens.
func (s *AuthService) Register(ctx context.Context, in RegisterInput) (*Tokens, error) {
	if len(in.Password) < 8 {
		return nil, domain.E(domain.KindInvalid, "WEAK_PASSWORD", "password must be at least 8 characters")
	}
	if !strings.Contains(in.Email, "@") {
		return nil, domain.E(domain.KindInvalid, "INVALID_EMAIL", "invalid email address")
	}

	hash, err := s.pass.Hash(in.Password)
	if err != nil {
		return nil, domain.Wrap(domain.KindInternal, "HASH_FAILED", "failed to hash password", err)
	}

	user := &domain.User{
		ID:       uuid.NewString(),
		Email:    strings.ToLower(strings.TrimSpace(in.Email)),
		Phone:    strings.TrimSpace(in.Phone),
		FullName: strings.TrimSpace(in.FullName),
		Roles:    []string{domain.RoleBuyer},
		Status:   domain.UserStatusActive,
	}
	if err := s.users.Create(ctx, user, hash); err != nil {
		return nil, err
	}

	s.audit(ctx, user.ID, "auth.register", "user", user.ID, in.IPAddress, in.UserAgent, nil)
	if s.mailer.IsConfigured() {
		s.sendVerifyEmail(ctx, user)
	}

	return s.issueTokens(ctx, user, in.DeviceName, in.IPAddress, in.UserAgent)
}

// LoginInput for password authentication.
type LoginInput struct {
	Email      string `json:"email"`
	Password   string `json:"password"`
	TotpCode   string `json:"totp_code,omitempty"`
	DeviceName string `json:"device_name,omitempty"`
	IPAddress  string `json:"-"`
	UserAgent  string `json:"-"`
}

// Me returns the full profile for an authenticated user.
func (s *AuthService) Me(ctx context.Context, userID string) (*domain.User, error) {
	user, err := s.users.ByID(ctx, userID)
	if err != nil {
		return nil, err
	}
	return user, nil
}

// UpdateProfileInput for editing the profile.
type UpdateProfileInput struct {
	UserID    string
	FullName  string
	Phone     string
	AvatarURL string
}

// UpdateProfile edits name, phone and avatar.
func (s *AuthService) UpdateProfile(ctx context.Context, in UpdateProfileInput) (*domain.User, error) {
	user, err := s.users.ByID(ctx, in.UserID)
	if err != nil {
		return nil, err
	}
	if strings.TrimSpace(in.FullName) != "" {
		user.FullName = strings.TrimSpace(in.FullName)
	}
	user.Phone = strings.TrimSpace(in.Phone)
	user.AvatarURL = strings.TrimSpace(in.AvatarURL)
	if err := s.users.UpdateProfile(ctx, user.ID, user.FullName, user.Phone, user.AvatarURL); err != nil {
		return nil, err
	}
	return user, nil
}

// ChangePassword verifies the current password, sets a new one and revokes sessions.
func (s *AuthService) ChangePassword(ctx context.Context, userID, currentPassword, newPassword string) error {
	if len(newPassword) < 8 {
		return domain.E(domain.KindInvalid, "WEAK_PASSWORD", "password must be at least 8 characters")
	}
	hash, err := s.users.PasswordHash(ctx, userID)
	if err != nil {
		return err
	}
	ok, err := s.pass.Verify(currentPassword, hash)
	if err != nil || !ok {
		return domain.ErrInvalidCreds
	}
	newHash, err := s.pass.Hash(newPassword)
	if err != nil {
		return domain.Wrap(domain.KindInternal, "HASH_FAILED", "failed to hash password", err)
	}
	if err := s.users.SetPassword(ctx, userID, newHash); err != nil {
		return err
	}
	return s.sessions.RevokeAllForUser(ctx, userID)
}

// Login verifies credentials, enforcing 2FA when enabled.
func (s *AuthService) Login(ctx context.Context, in LoginInput) (*Tokens, error) {
	user, err := s.users.ByEmail(ctx, in.Email)
	if err != nil {
		return nil, domain.ErrInvalidCreds
	}
	if !user.IsActive() {
		return nil, domain.ErrUserDisabled
	}

	hash, err := s.users.PasswordHash(ctx, user.ID)
	if err != nil {
		return nil, err
	}
	ok, err := s.pass.Verify(in.Password, hash)
	if err != nil || !ok {
		s.audit(ctx, user.ID, "auth.login_failed", "user", user.ID, in.IPAddress, in.UserAgent, nil)
		return nil, domain.ErrInvalidCreds
	}

	if user.TwoFactorEnabled {
		secret, confirmed, err := s.users.TOTPSecret(ctx, user.ID)
		if err != nil {
			return nil, err
		}
		if !confirmed {
			return nil, domain.E(domain.KindInternal, "TOTP_STATE", "2FA misconfigured")
		}
		if !totp.Validate(in.TotpCode, secret) {
			// fall back to a one-time backup code
			used, err := s.users.ConsumeBackupCode(ctx, user.ID, hashSHA256(in.TotpCode))
			if err != nil {
				return nil, err
			}
			if !used {
				s.audit(ctx, user.ID, "auth.totp_failed", "user", user.ID, in.IPAddress, in.UserAgent, nil)
				return nil, domain.ErrTwoFactorInvalid
			}
			s.audit(ctx, user.ID, "auth.backup_code_used", "user", user.ID, in.IPAddress, in.UserAgent, nil)
		}
	}

	if err := s.users.SetLastLogin(ctx, user.ID, time.Now().UTC()); err != nil {
		return nil, err
	}
	s.audit(ctx, user.ID, "auth.login", "user", user.ID, in.IPAddress, in.UserAgent, nil)
	return s.issueTokens(ctx, user, in.DeviceName, in.IPAddress, in.UserAgent)
}

// Refresh rotates a refresh token, revoking the family on reuse.
func (s *AuthService) Refresh(ctx context.Context, refreshToken, ipAddress, userAgent string) (*Tokens, error) {
	hash := s.tokens.HashRefresh(refreshToken)
	sess, err := s.sessions.ByHash(ctx, hash)
	if err != nil {
		return nil, err
	}

	user, err := s.users.ByID(ctx, sess.UserID)
	if err != nil {
		return nil, err
	}
	if !user.IsActive() {
		return nil, domain.ErrUserDisabled
	}

	newToken, newHash, err := s.tokens.NewRefreshToken()
	if err != nil {
		return nil, domain.Wrap(domain.KindInternal, "TOKEN_GEN", "failed to generate token", err)
	}
	if err := s.sessions.Rotate(ctx, sess.ID, hash, newHash); err != nil {
		return nil, err
	}

	return s.issueForUser(ctx, user, newToken, sess)
}

// Logout revokes the presented refresh session.
func (s *AuthService) Logout(ctx context.Context, refreshToken string) error {
	hash := s.tokens.HashRefresh(refreshToken)
	sess, err := s.sessions.ByHash(ctx, hash)
	if err != nil {
		return err
	}
	return s.sessions.Revoke(ctx, sess.ID)
}

// LogoutAll revokes every session for the user.
func (s *AuthService) LogoutAll(ctx context.Context, userID string) error {
	return s.sessions.RevokeAllForUser(ctx, userID)
}

// Sessions lists active sessions for the user.
func (s *AuthService) Sessions(ctx context.Context, userID string) ([]*domain.RefreshSession, error) {
	return s.sessions.ListForUser(ctx, userID)
}

// RevokeSession revokes one of the user's sessions.
func (s *AuthService) RevokeSession(ctx context.Context, userID, sessionID string) error {
	sess, err := s.sessions.ByID(ctx, sessionID)
	if err != nil {
		return err
	}
	if sess.UserID != userID {
		return domain.E(domain.KindForbidden, "NOT_OWNED", "session does not belong to user")
	}
	return s.sessions.Revoke(ctx, sessionID)
}

// RequestEmailVerification sends a verification email (idempotent).
func (s *AuthService) RequestEmailVerification(ctx context.Context, userID string) error {
	user, err := s.users.ByID(ctx, userID)
	if err != nil {
		return err
	}
	if user.EmailVerifiedAt != nil {
		return domain.E(domain.KindConflict, "ALREADY_VERIFIED", "email already verified")
	}
	if !s.mailer.IsConfigured() {
		return nil
	}
	return s.sendVerifyEmail(ctx, user)
}

// VerifyEmail consumes the token and marks the email verified.
func (s *AuthService) VerifyEmail(ctx context.Context, tokenStr string) error {
	hash := s.tokens.HashRefresh(tokenStr)
	userID, err := s.sessions.ConsumeEmailToken(ctx, hash, "verify_email")
	if err != nil {
		return err
	}
	return s.users.VerifyEmail(ctx, userID)
}

// RequestPasswordReset sends a reset link (always succeeds to avoid user enumeration).
func (s *AuthService) RequestPasswordReset(ctx context.Context, email string) error {
	user, err := s.users.ByEmail(ctx, email)
	if err != nil {
		return nil
	}
	if !s.mailer.IsConfigured() {
		return nil
	}

	rawToken, err := randomToken()
	if err != nil {
		return err
	}
	if err := s.sessions.SaveEmailToken(ctx, s.tokens.HashRefresh(rawToken), user.ID, "reset_password", time.Now().Add(1*time.Hour)); err != nil {
		return err
	}
	actionURL := fmt.Sprintf("%s/reset-password?token=%s", s.cfg.App.WebURL, rawToken)
	return s.mailer.Send(ctx, user.Email, "Reset your password", "password_reset", map[string]any{
		"Name": user.FullName, "ActionURL": actionURL,
	})
}

// ResetPassword validates the token and sets a new password.
func (s *AuthService) ResetPassword(ctx context.Context, tokenStr, newPassword string) error {
	if len(newPassword) < 8 {
		return domain.E(domain.KindInvalid, "WEAK_PASSWORD", "password must be at least 8 characters")
	}
	hash := s.tokens.HashRefresh(tokenStr)
	userID, err := s.sessions.ConsumeEmailToken(ctx, hash, "reset_password")
	if err != nil {
		return err
	}
	newHash, err := s.pass.Hash(newPassword)
	if err != nil {
		return domain.Wrap(domain.KindInternal, "HASH_FAILED", "failed to hash password", err)
	}
	if err := s.users.SetPassword(ctx, userID, newHash); err != nil {
		return err
	}
	// Invalidate every session on password change.
	return s.sessions.RevokeAllForUser(ctx, userID)
}

// SetupTOTP generates a new TOTP secret for enrollment.
func (s *AuthService) SetupTOTP(ctx context.Context, userID string) (secret, otpauthURL string, err error) {
	key, err := totp.Generate(totp.GenerateOpts{
		Issuer:      "VinCommerce",
		AccountName: s.accountName(ctx, userID),
		Period:      30,
	})
	if err != nil {
		return "", "", domain.Wrap(domain.KindInternal, "TOTP_GEN", "failed to generate secret", err)
	}
	if err := s.users.SaveTOTPSecret(ctx, userID, key.Secret()); err != nil {
		return "", "", err
	}
	return key.Secret(), key.URL(), nil
}

// ConfirmTOTP validates a code against the pending secret and enables 2FA,
// returning one-time backup codes (shown to the user exactly once).
func (s *AuthService) ConfirmTOTP(ctx context.Context, userID, code string) ([]string, error) {
	secret, confirmed, err := s.users.TOTPSecret(ctx, userID)
	if err != nil {
		return nil, err
	}
	if confirmed {
		return nil, domain.E(domain.KindConflict, "ALREADY_ENABLED", "2FA is already enabled")
	}
	if !totp.Validate(code, secret) {
		return nil, domain.ErrTwoFactorInvalid
	}
	if err := s.users.ConfirmTOTP(ctx, userID); err != nil {
		return nil, err
	}
	return s.RegenerateBackupCodes(ctx, userID)
}

// RegenerateBackupCodes replaces all backup codes and returns the new plaintext set.
func (s *AuthService) RegenerateBackupCodes(ctx context.Context, userID string) ([]string, error) {
	codes := make([]string, 0, 10)
	hashes := make([]string, 0, 10)
	for i := 0; i < 10; i++ {
		code, err := newBackupCode()
		if err != nil {
			return nil, domain.Wrap(domain.KindInternal, "CODE_GEN", "failed to generate backup codes", err)
		}
		codes = append(codes, code)
		hashes = append(hashes, hashSHA256(code))
	}
	if err := s.users.SaveBackupCodes(ctx, userID, hashes); err != nil {
		return nil, err
	}
	return codes, nil
}

// BackupCodeInfo describes the stored backup-code set (hashes never exposed).
func (s *AuthService) BackupCodeInfo(ctx context.Context, userID string) (total, remaining int, err error) {
	codes, err := s.users.BackupCodes(ctx, userID)
	if err != nil {
		return 0, 0, err
	}
	total = len(codes)
	for _, c := range codes {
		if c.UsedAt == nil {
			remaining++
		}
	}
	return total, remaining, nil
}

// DisableTOTP turns off 2FA given a valid code.
func (s *AuthService) DisableTOTP(ctx context.Context, userID, code string) error {
	secret, confirmed, err := s.users.TOTPSecret(ctx, userID)
	if err != nil {
		return err
	}
	if !confirmed {
		return domain.E(domain.KindConflict, "NOT_ENABLED", "2FA is not enabled")
	}
	if !totp.Validate(code, secret) {
		return domain.ErrTwoFactorInvalid
	}
	return s.users.DisableTOTP(ctx, userID)
}

// --- internal helpers ---

// hashSHA256 hex-encodes the SHA-256 digest of a secret.
func hashSHA256(s string) string {
	sum := sha256.Sum256([]byte(s))
	return hex.EncodeToString(sum[:])
}

// newBackupCode returns a human-friendly one-time code, e.g. "K7MQ-9X2P".
func newBackupCode() (string, error) {
	var buf [8]byte
	if _, err := rand.Read(buf[:]); err != nil {
		return "", err
	}
	const alphabet = "ABCDEFGHJKMNPQRSTUVWXYZ23456789"
	code := make([]byte, 9)
	for i := 0; i < 8; i++ {
		if i == 4 {
			code[i] = '-'
			continue
		}
		code[i] = alphabet[int(buf[i])%len(alphabet)]
	}
	code[8] = alphabet[int(buf[7])%len(alphabet)]
	return string(code), nil
}

func (s *AuthService) issueTokens(ctx context.Context, user *domain.User, deviceName, ip, ua string) (*Tokens, error) {
	refresh, refreshHash, err := s.tokens.NewRefreshToken()
	if err != nil {
		return nil, domain.Wrap(domain.KindInternal, "TOKEN_GEN", "failed to generate token", err)
	}
	sess := &domain.RefreshSession{
		ID:         uuid.NewString(),
		UserID:     user.ID,
		FamilyID:   uuid.NewString(),
		DeviceName: deviceName,
		IPAddress:  sanitizeIP(ip),
		UserAgent:  ua,
		ExpiresAt:  time.Now().UTC().Add(s.tokens.RefreshTTL()),
	}
	// Store hash via field set after creation
	sess.RefreshHash = refreshHash
	if err := s.sessions.Create(ctx, sess); err != nil {
		return nil, err
	}
	return s.issueForUser(ctx, user, refresh, sess)
}

func (s *AuthService) issueForUser(ctx context.Context, user *domain.User, refreshToken string, sess *domain.RefreshSession) (*Tokens, error) {
	access, exp, err := s.tokens.IssueAccess(user)
	if err != nil {
		return nil, domain.Wrap(domain.KindInternal, "TOKEN_GEN", "failed to sign token", err)
	}
	return &Tokens{
		AccessToken:  access,
		RefreshToken: refreshToken,
		ExpiresIn:    int64(time.Until(exp).Seconds()),
		ExpiresAt:    exp,
		User:         user,
	}, nil
}

func (s *AuthService) sendVerifyEmail(ctx context.Context, user *domain.User) error {
	rawToken, err := randomToken()
	if err != nil {
		return err
	}
	if err := s.sessions.SaveEmailToken(ctx, s.tokens.HashRefresh(rawToken), user.ID, "verify_email", time.Now().Add(24*time.Hour)); err != nil {
		return err
	}
	actionURL := fmt.Sprintf("%s/verify-email?token=%s", s.cfg.App.WebURL, rawToken)
	return s.mailer.Send(ctx, user.Email, "Verify your email", "email_verify", map[string]any{
		"Name": user.FullName, "ActionURL": actionURL,
	})
}

func (s *AuthService) accountName(ctx context.Context, userID string) string {
	user, err := s.users.ByID(ctx, userID)
	if err != nil {
		return userID
	}
	return user.Email
}

func (s *AuthService) audit(ctx context.Context, actorID, action, entityType, entityID, ip, ua string, meta map[string]any) {
	_ = s.sessions.Audit(ctx, &domain.AuditEntry{
		ActorID: actorID, Action: action, EntityType: entityType, EntityID: entityID,
		IPAddress: ip, UserAgent: ua, Metadata: meta,
	})
}

func randomToken() (string, error) {
	b := make([]byte, 32)
	if _, err := rand.Read(b); err != nil {
		return "", err
	}
	return hex.EncodeToString(b), nil
}

func sanitizeIP(ip string) string {
	if ip == "" {
		return ""
	}
	parsed := net.ParseIP(ip)
	if parsed == nil {
		return ""
	}
	return parsed.String()
}
