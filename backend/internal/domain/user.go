package domain

import "time"

// Role names used by RBAC.
const (
	RoleBuyer   = "buyer"
	RoleSeller  = "seller"
	RoleAdmin   = "admin"
	RoleSupport = "support"
)

// UserStatus values.
const (
	UserStatusActive    = "active"
	UserStatusDisabled  = "disabled"
	UserStatusSuspended = "suspended"
)

// User is the identity aggregate.
type User struct {
	ID               string     `json:"id"`
	Email            string     `json:"email"`
	Phone            string     `json:"phone,omitempty"`
	FullName         string     `json:"full_name"`
	Roles            []string   `json:"roles"`
	Status           string     `json:"status"`
	EmailVerifiedAt  *time.Time `json:"email_verified_at,omitempty"`
	TwoFactorEnabled bool       `json:"two_factor_enabled"`
	LastLoginAt      *time.Time `json:"last_login_at,omitempty"`
	CreatedAt        time.Time  `json:"created_at"`
	UpdatedAt        time.Time  `json:"updated_at"`
	AvatarURL        string     `json:"avatar_url,omitempty"`
	// TokenVer is embedded into access-token claims; server-side bumps
	// (role/status changes) invalidate outstanding tokens immediately.
	TokenVer int `json:"-"`
	// ActingAs is set when the token was minted via admin impersonation and
	// holds the ADMIN's id (the real actor). Subject remains the target.
	ActingAs *string `json:"-"`
}

// HasRole reports role membership.
func (u *User) HasRole(role string) bool {
	for _, r := range u.Roles {
		if r == role {
			return true
		}
	}
	return false
}

// IsActive reports whether the account can be used.
func (u *User) IsActive() bool { return u.Status == UserStatusActive }

// RefreshSession is a device session backing a rotating refresh token.
type RefreshSession struct {
	ID          string     `json:"id"`
	UserID      string     `json:"user_id"`
	RefreshHash string     `json:"-"`
	FamilyID    string     `json:"family_id"`
	DeviceName  string     `json:"device_name,omitempty"`
	IPAddress   string     `json:"ip_address,omitempty"`
	UserAgent   string     `json:"user_agent,omitempty"`
	ExpiresAt   time.Time  `json:"expires_at"`
	RevokedAt   *time.Time `json:"revoked_at,omitempty"`
	LastUsedAt  time.Time  `json:"last_used_at"`
	CreatedAt   time.Time  `json:"created_at"`
}

// AuditEntry records a privileged or sensitive action.
type AuditEntry struct {
	ActorID    string
	Action     string
	EntityType string
	EntityID   string
	IPAddress  string
	UserAgent  string
	Metadata   map[string]any
}
