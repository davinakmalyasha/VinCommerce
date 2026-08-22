package handler

import (
	"encoding/json"
	"log/slog"
	"net"
	"net/http"
	"time"

	"github.com/vincommerce/backend/internal/config"
	"github.com/vincommerce/backend/internal/domain"
	"github.com/vincommerce/backend/internal/httpapi/middleware"
	"github.com/vincommerce/backend/internal/service"
)

// refreshCookieName holds the httpOnly refresh token cookie.
const refreshCookieName = "vc_refresh"

// Auth exposes identity endpoints.
type Auth struct {
	svc        *service.AuthService
	logger     *slog.Logger
	secure     bool
	refreshTTL time.Duration
}

// NewAuth creates an Auth handler.
func NewAuth(svc *service.AuthService, logger *slog.Logger, cfg *config.Config) *Auth {
	return &Auth{
		svc: svc, logger: logger,
		secure:     cfg.Environment == "production",
		refreshTTL: cfg.Auth.RefreshTokenTTL,
	}
}

// writeTokens responds with the token pair and sets the refresh token as an
// httpOnly cookie (kept in the body too for non-browser clients).
func (h *Auth) writeTokens(w http.ResponseWriter, status int, tokens *service.Tokens) {
	http.SetCookie(w, &http.Cookie{
		Name:     refreshCookieName,
		Value:    tokens.RefreshToken,
		Path:     "/api/v1",
		HttpOnly: true,
		Secure:   h.secure,
		SameSite: http.SameSiteLaxMode,
		MaxAge:   int(h.refreshTTL.Seconds()),
	})
	writeJSON(w, status, tokens)
}

func (h *Auth) clearRefreshCookie(w http.ResponseWriter) {
	http.SetCookie(w, &http.Cookie{
		Name:     refreshCookieName,
		Value:    "",
		Path:     "/api/v1",
		HttpOnly: true,
		Secure:   h.secure,
		SameSite: http.SameSiteLaxMode,
		MaxAge:   -1,
	})
}

// refreshTokenFromRequest prefers the httpOnly cookie, falling back to the body.
func (h *Auth) refreshTokenFromRequest(r *http.Request) (string, bool) {
	if c, err := r.Cookie(refreshCookieName); err == nil && c.Value != "" {
		return c.Value, true
	}
	var req refreshRequest
	if err := decode(r, &req); err == nil && req.RefreshToken != "" {
		return req.RefreshToken, true
	}
	return "", false
}

type registerRequest struct {
	Email      string `json:"email"`
	Phone      string `json:"phone,omitempty"`
	Password   string `json:"password"`
	FullName   string `json:"full_name"`
	DeviceName string `json:"device_name,omitempty"`
}

// Register handles POST /auth/register.
func (h *Auth) Register(w http.ResponseWriter, r *http.Request) {
	var req registerRequest
	if err := decode(r, &req); err != nil {
		writeErr(w, r, domain.E(domain.KindInvalid, "BAD_JSON", err.Error()))
		return
	}
	tokens, err := h.svc.Register(r.Context(), service.RegisterInput{
		Email: req.Email, Phone: req.Phone, Password: req.Password, FullName: req.FullName,
		DeviceName: req.DeviceName, IPAddress: clientIP(r), UserAgent: r.UserAgent(),
	})
	if err != nil {
		writeErr(w, r, err)
		return
	}
	h.writeTokens(w, http.StatusCreated, tokens)
}

type loginRequest struct {
	Email      string `json:"email"`
	Password   string `json:"password"`
	TotpCode   string `json:"totp_code,omitempty"`
	DeviceName string `json:"device_name,omitempty"`
}

// Login handles POST /auth/login.
func (h *Auth) Login(w http.ResponseWriter, r *http.Request) {
	var req loginRequest
	if err := decode(r, &req); err != nil {
		writeErr(w, r, domain.E(domain.KindInvalid, "BAD_JSON", err.Error()))
		return
	}
	tokens, err := h.svc.Login(r.Context(), service.LoginInput{
		Email: req.Email, Password: req.Password, TotpCode: req.TotpCode,
		DeviceName: req.DeviceName, IPAddress: clientIP(r), UserAgent: r.UserAgent(),
	})
	if err != nil {
		writeErr(w, r, err)
		return
	}
	h.writeTokens(w, http.StatusOK, tokens)
}

type refreshRequest struct {
	RefreshToken string `json:"refresh_token"`
}

// Refresh handles POST /auth/refresh.
func (h *Auth) Refresh(w http.ResponseWriter, r *http.Request) {
	token, ok := h.refreshTokenFromRequest(r)
	if !ok {
		writeErr(w, r, domain.E(domain.KindInvalid, "BAD_JSON", "refresh_token required"))
		return
	}
	tokens, err := h.svc.Refresh(r.Context(), token, clientIP(r), r.UserAgent())
	if err != nil {
		writeErr(w, r, err)
		return
	}
	h.writeTokens(w, http.StatusOK, tokens)
}

type logoutRequest struct {
	RefreshToken string `json:"refresh_token"`
}

// Logout handles POST /auth/logout.
func (h *Auth) Logout(w http.ResponseWriter, r *http.Request) {
	token, _ := h.refreshTokenFromRequest(r)
	h.clearRefreshCookie(w)
	if token != "" {
		if err := h.svc.Logout(r.Context(), token); err != nil {
			writeErr(w, r, err)
			return
		}
	}
	writeJSON(w, http.StatusOK, map[string]bool{"success": true})
}

// LogoutAll handles POST /auth/logout-all.
func (h *Auth) LogoutAll(w http.ResponseWriter, r *http.Request) {
	user := middleware.UserFrom(r.Context())
	if err := h.svc.LogoutAll(r.Context(), user.ID); err != nil {
		writeErr(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]bool{"success": true})
}

// Me handles GET /auth/me.
func (h *Auth) Me(w http.ResponseWriter, r *http.Request) {
	user := middleware.UserFrom(r.Context())
	profile, err := h.svc.Me(r.Context(), user.ID)
	if err != nil {
		writeErr(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"user": profile})
}

// UpdateProfile handles PUT /auth/profile.
func (h *Auth) UpdateProfile(w http.ResponseWriter, r *http.Request) {
	user := middleware.UserFrom(r.Context())
	var req struct {
		FullName  string `json:"full_name"`
		Phone     string `json:"phone"`
		AvatarURL string `json:"avatar_url"`
	}
	if err := decode(r, &req); err != nil {
		writeErr(w, r, domain.E(domain.KindInvalid, "BAD_JSON", err.Error()))
		return
	}
	profile, err := h.svc.UpdateProfile(r.Context(), service.UpdateProfileInput{
		UserID: user.ID, FullName: req.FullName, Phone: req.Phone, AvatarURL: req.AvatarURL,
	})
	if err != nil {
		writeErr(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"user": profile})
}

// ChangePassword handles POST /auth/password/change.
func (h *Auth) ChangePassword(w http.ResponseWriter, r *http.Request) {
	user := middleware.UserFrom(r.Context())
	var req struct {
		CurrentPassword string `json:"current_password"`
		NewPassword     string `json:"new_password"`
	}
	if err := decode(r, &req); err != nil {
		writeErr(w, r, domain.E(domain.KindInvalid, "BAD_JSON", err.Error()))
		return
	}
	if err := h.svc.ChangePassword(r.Context(), user.ID, req.CurrentPassword, req.NewPassword); err != nil {
		writeErr(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]bool{"changed": true})
}

// Sessions handles GET /auth/sessions.
func (h *Auth) Sessions(w http.ResponseWriter, r *http.Request) {
	user := middleware.UserFrom(r.Context())
	sessions, err := h.svc.Sessions(r.Context(), user.ID)
	if err != nil {
		writeErr(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"sessions": sessions})
}

type revokeSessionRequest struct {
	SessionID string `json:"session_id"`
}

// RevokeSession handles DELETE /auth/sessions/{id}.
func (h *Auth) RevokeSession(w http.ResponseWriter, r *http.Request) {
	user := middleware.UserFrom(r.Context())
	var req revokeSessionRequest
	if err := decode(r, &req); err != nil {
		writeErr(w, r, domain.E(domain.KindInvalid, "BAD_JSON", err.Error()))
		return
	}
	if err := h.svc.RevokeSession(r.Context(), user.ID, req.SessionID); err != nil {
		writeErr(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]bool{"success": true})
}

// RequestEmailVerification handles POST /auth/verify-email/request.
func (h *Auth) RequestEmailVerification(w http.ResponseWriter, r *http.Request) {
	user := middleware.UserFrom(r.Context())
	if err := h.svc.RequestEmailVerification(r.Context(), user.ID); err != nil {
		writeErr(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]bool{"sent": true})
}

type verifyEmailRequest struct {
	Token string `json:"token"`
}

// VerifyEmail handles POST /auth/verify-email.
func (h *Auth) VerifyEmail(w http.ResponseWriter, r *http.Request) {
	var req verifyEmailRequest
	if err := decode(r, &req); err != nil {
		writeErr(w, r, domain.E(domain.KindInvalid, "BAD_JSON", err.Error()))
		return
	}
	if err := h.svc.VerifyEmail(r.Context(), req.Token); err != nil {
		writeErr(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]bool{"verified": true})
}

type requestResetRequest struct {
	Email string `json:"email"`
}

// RequestPasswordReset handles POST /auth/password/reset-request.
func (h *Auth) RequestPasswordReset(w http.ResponseWriter, r *http.Request) {
	var req requestResetRequest
	if err := decode(r, &req); err != nil {
		writeErr(w, r, domain.E(domain.KindInvalid, "BAD_JSON", err.Error()))
		return
	}
	if err := h.svc.RequestPasswordReset(r.Context(), req.Email); err != nil {
		writeErr(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]bool{"sent": true})
}

type resetPasswordRequest struct {
	Token       string `json:"token"`
	NewPassword string `json:"new_password"`
}

// ResetPassword handles POST /auth/password/reset.
func (h *Auth) ResetPassword(w http.ResponseWriter, r *http.Request) {
	var req resetPasswordRequest
	if err := decode(r, &req); err != nil {
		writeErr(w, r, domain.E(domain.KindInvalid, "BAD_JSON", err.Error()))
		return
	}
	if err := h.svc.ResetPassword(r.Context(), req.Token, req.NewPassword); err != nil {
		writeErr(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]bool{"reset": true})
}

// SetupTOTP handles POST /auth/2fa/setup.
func (h *Auth) SetupTOTP(w http.ResponseWriter, r *http.Request) {
	user := middleware.UserFrom(r.Context())
	secret, otpauthURL, err := h.svc.SetupTOTP(r.Context(), user.ID)
	if err != nil {
		writeErr(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]string{"secret": secret, "otpauth_url": otpauthURL})
}

type totpCodeRequest struct {
	Code string `json:"code"`
}

// ConfirmTOTP handles POST /auth/2fa/confirm — returns one-time backup codes.
func (h *Auth) ConfirmTOTP(w http.ResponseWriter, r *http.Request) {
	user := middleware.UserFrom(r.Context())
	var req totpCodeRequest
	if err := decode(r, &req); err != nil {
		writeErr(w, r, domain.E(domain.KindInvalid, "BAD_JSON", err.Error()))
		return
	}
	codes, err := h.svc.ConfirmTOTP(r.Context(), user.ID, req.Code)
	if err != nil {
		writeErr(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"enabled": true, "backup_codes": codes})
}

// BackupCodes handles GET /auth/2fa/backup-codes.
func (h *Auth) BackupCodes(w http.ResponseWriter, r *http.Request) {
	user := middleware.UserFrom(r.Context())
	total, remaining, err := h.svc.BackupCodeInfo(r.Context(), user.ID)
	if err != nil {
		writeErr(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"total": total, "remaining": remaining})
}

// RegenerateBackupCodes handles POST /auth/2fa/backup-codes/regenerate.
func (h *Auth) RegenerateBackupCodes(w http.ResponseWriter, r *http.Request) {
	user := middleware.UserFrom(r.Context())
	codes, err := h.svc.RegenerateBackupCodes(r.Context(), user.ID)
	if err != nil {
		writeErr(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"backup_codes": codes})
}

// DisableTOTP handles POST /auth/2fa/disable.
func (h *Auth) DisableTOTP(w http.ResponseWriter, r *http.Request) {
	user := middleware.UserFrom(r.Context())
	var req totpCodeRequest
	if err := decode(r, &req); err != nil {
		writeErr(w, r, domain.E(domain.KindInvalid, "BAD_JSON", err.Error()))
		return
	}
	if err := h.svc.DisableTOTP(r.Context(), user.ID, req.Code); err != nil {
		writeErr(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]bool{"disabled": true})
}

// OAuth exposes social login.
type OAuth struct {
	svc *service.OAuthService
}

// NewOAuth creates an OAuth handler.
func NewOAuth(svc *service.OAuthService) *OAuth {
	return &OAuth{svc: svc}
}

// Start handles GET /auth/oauth/google/start.
func (h *OAuth) Start(w http.ResponseWriter, r *http.Request) {
	if !h.svc.Enabled() {
		writeErr(w, r, domain.E(domain.KindConflict, "OAUTH_DISABLED", "social login is not configured"))
		return
	}
	authURL, state, err := h.svc.AuthURL()
	if err != nil {
		writeErr(w, r, err)
		return
	}
	http.SetCookie(w, &http.Cookie{
		Name: "oauth_state", Value: state, Path: "/", HttpOnly: true,
		SameSite: http.SameSiteLaxMode, MaxAge: 600,
	})
	writeJSON(w, http.StatusOK, map[string]string{"auth_url": authURL})
}

type oauthCallbackRequest struct {
	Code  string `json:"code"`
	State string `json:"state"`
}

// Callback handles POST /auth/oauth/google/callback.
func (h *OAuth) Callback(w http.ResponseWriter, r *http.Request) {
	var req oauthCallbackRequest
	if err := decode(r, &req); err != nil {
		writeErr(w, r, domain.E(domain.KindInvalid, "BAD_JSON", err.Error()))
		return
	}
	cookie, err := r.Cookie("oauth_state")
	if err != nil || cookie.Value == "" || cookie.Value != req.State {
		writeErr(w, r, domain.E(domain.KindInvalid, "OAUTH_STATE", "state mismatch"))
		return
	}
	tokens, err := h.svc.HandleCallback(r.Context(), req.Code)
	if err != nil {
		writeErr(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, tokens)
}

// --- shared request helpers ---

func decode(r *http.Request, v any) error {
	dec := json.NewDecoder(r.Body)
	dec.DisallowUnknownFields()
	if err := dec.Decode(v); err != nil {
		return err
	}
	return nil
}

func clientIP(r *http.Request) string {
	if ip := r.Header.Get("X-Forwarded-For"); ip != "" {
		if host, _, err := net.SplitHostPort(ip); err == nil {
			return host
		}
		return ip
	}
	host, _, err := net.SplitHostPort(r.RemoteAddr)
	if err == nil {
		return host
	}
	return r.RemoteAddr
}
