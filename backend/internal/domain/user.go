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

// Actor is an authenticated caller, carrying its identity and its roles.
//
// This exists because authorization decisions were being made by passing a
// `bool` across a layer boundary. `ChatService.Session(ctx, id, requesterID,
// staff bool)` and `SupportService.TicketFor(ctx, id, requesterID, staff bool)`
// both took the caller's privilege as a boolean that the *transport* computed
// from `user.HasRole(RoleSupport) || user.HasRole(RoleAdmin)` and then handed
// inward. A boolean privilege bit cannot be verified by the code that consumes
// it: the service has to trust it, and the next caller that passes `true` grants
// full access to every ticket and chat session in the system. The same shape
// appeared six more times in the order handlers, via
// `svc.ByID(ctx, id, user.ID, user.HasRole(RoleSeller))`.
//
// An Actor is unforgeable-by-accident: it carries the role list, so the
// receiving layer can re-derive any decision instead of trusting a summary of
// one. It is not a token and does not cross the network — it is constructed from
// the verified JWT subject by the auth middleware and nowhere else.
type Actor struct {
	// ID is the subject: the user whose session this is.
	ID string
	// Roles is the subject's role list, from the verified token.
	Roles []string
	// ActingAsID is the ADMIN's id when the session was minted through admin
	// impersonation, empty otherwise. The real actor of an action is never the
	// impersonated subject, so every audit row records this.
	ActingAsID string
}

// ActorFrom builds an Actor from a verified user.
func ActorFrom(u *User) Actor {
	a := Actor{ID: u.ID, Roles: append([]string(nil), u.Roles...)}
	if u.ActingAs != nil {
		a.ActingAsID = *u.ActingAs
	}
	return a
}

// HasRole reports role membership.
func (a Actor) HasRole(role string) bool {
	for _, r := range a.Roles {
		if r == role {
			return true
		}
	}
	return false
}

// IsZero reports whether the Actor is unset. A zero Actor is never a valid
// caller; treating it as one is how an unauthenticated request reaches a
// service method that only checks "is this the buyer?".
func (a Actor) IsZero() bool { return a.ID == "" }

// IsStaff reports operator-level access (support desk or admin).
func (a Actor) IsStaff() bool { return a.HasRole(RoleSupport) || a.HasRole(RoleAdmin) }

// IsAdmin reports administrative access.
func (a Actor) IsAdmin() bool { return a.HasRole(RoleAdmin) }

// RealID is the id that must be written to the audit log: the admin's, when
// impersonating, otherwise the subject's own.
func (a Actor) RealID() string {
	if a.ActingAsID != "" {
		return a.ActingAsID
	}
	return a.ID
}

// CanReadOrder reports whether this Actor may read an order bought by buyerID
// and sold by sellerID.
func (a Actor) CanReadOrder(buyerID, sellerID string) bool {
	if a.IsZero() {
		return false
	}
	return a.ID == buyerID || a.ID == sellerID || a.IsStaff()
}

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
