package domain

import "testing"

// These tests cover the authorization primitive that replaced the
// `isSeller bool` / `staff bool` parameters that handlers used to pass inward.
//
// The bug being locked down: six order handlers called
//
//	svc.ByID(ctx, id, user.ID, user.HasRole(RoleSeller))
//
// which decides *whose* order it is by asking the caller what role the caller
// has, in the transport layer, and then handing the answer inward as a boolean.
// Two consequences, both real. A user holding BOTH roles -- the normal case for
// any active seller, since sellers also buy -- was routed down the seller branch
// for their own purchase and denied, because the check compared them against
// SellerID. And because `paid -> cancelled` is a legal transition, a seller
// reaching their own sub-order through the buyer-facing cancel endpoint was
// authorised to cancel it, with only the escrow guard in the way.
//
// The parallel case in chat and support was worse: `Session(ctx, id, userID,
// staff bool)` accepted a privilege bit the service could not verify, so any
// future caller that passed `true` would have full access to every ticket and
// chat session.

func TestActorRoleMembership(t *testing.T) {
	a := Actor{ID: "u1", Roles: []string{RoleBuyer, RoleSeller}}
	if !a.HasRole(RoleSeller) {
		t.Error("multi-role actor lost its seller role")
	}
	if !a.HasRole(RoleBuyer) {
		t.Error("multi-role actor lost its buyer role")
	}
	if a.HasRole(RoleAdmin) {
		t.Error("actor gained a role it does not hold")
	}
}

func TestZeroActorFailsClosed(t *testing.T) {
	// A zero Actor is what an anonymous request produces. Every predicate must
	// be false, because a service that forgets to check for anonymous should
	// fail closed rather than open.
	var a Actor
	if !a.IsZero() {
		t.Fatal("zero value must report IsZero")
	}
	if a.HasRole(RoleAdmin) {
		t.Error("zero actor claimed to be an admin")
	}
	if a.IsAdmin() {
		t.Error("zero actor claimed to be an admin")
	}
	if a.IsStaff() {
		t.Error("zero actor claimed to be staff")
	}
	if a.CanReadOrder("buyer-1", "seller-1") {
		t.Error("zero actor could read an order -- this is the fail-open case")
	}
}

func TestActorCanReadOrder(t *testing.T) {
	const (
		buyerID  = "11111111-1111-1111-1111-111111111111"
		sellerID = "22222222-2222-2222-2222-222222222222"
		otherID  = "33333333-3333-3333-3333-333333333333"
	)
	cases := []struct {
		name  string
		actor Actor
		want  bool
	}{
		{"buyer reads own order", Actor{ID: buyerID}, true},
		{"seller reads own order", Actor{ID: sellerID}, true},
		{"support reads any order", Actor{ID: otherID, Roles: []string{RoleSupport}}, true},
		{"admin reads any order", Actor{ID: otherID, Roles: []string{RoleAdmin}}, true},
		{"unrelated buyer is denied", Actor{ID: otherID, Roles: []string{RoleBuyer}}, false},
		{"unrelated seller is denied", Actor{ID: otherID, Roles: []string{RoleSeller}}, false},
		{"anonymous is denied", Actor{}, false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := tc.actor.CanReadOrder(buyerID, sellerID); got != tc.want {
				t.Errorf("CanReadOrder = %v, want %v (roles %v)", got, tc.want, tc.actor.Roles)
			}
		})
	}
}

// TestActorDoesNotMutateUserRoles pins the copy in ActorFrom. The Actor holds a
// slice; if it aliased the User's slice, a handler that appended to
// actor.Roles would silently escalate its own privileges for the rest of the
// request.
func TestActorDoesNotAliasUserRoles(t *testing.T) {
	u := &User{ID: "u1", Roles: []string{RoleBuyer}}
	a := ActorFrom(u)
	a.Roles[0] = RoleAdmin
	a.Roles = append(a.Roles, RoleSeller)

	if u.Roles[0] != RoleBuyer {
		t.Errorf("mutating the Actor's roles changed the User's roles: got %q", u.Roles[0])
	}
	if len(u.Roles) != 1 {
		t.Errorf("appending to the Actor's roles changed the User's role count: got %d", len(u.Roles))
	}
	if a.HasRole(RoleBuyer) {
		t.Error("actor lost the role it started with")
	}
}

// TestActorRealIDPrefersImpersonator: an audit row must record the admin who
// performed an action performed as a customer, not the customer.
func TestActorRealIDPrefersImpersonator(t *testing.T) {
	const (
		adminID    = "aaaaaaaa-0000-0000-0000-000000000000"
		customerID = "bbbbbbbb-0000-0000-0000-000000000000"
	)
	normal := ActorFrom(&User{ID: customerID, Roles: []string{RoleBuyer}})
	if normal.RealID() != customerID {
		t.Errorf("non-impersonating RealID = %q, want the subject %q", normal.RealID(), customerID)
	}

	impersonating := ActorFrom(&User{ID: customerID, Roles: []string{RoleBuyer}, ActingAs: ptr(adminID)})
	if impersonating.RealID() != adminID {
		t.Errorf("impersonating RealID = %q, want the ADMIN %q", impersonating.RealID(), adminID)
	}
	// The subject is still the customer: they are the one whose session is active
	// and whose data is in scope.
	if impersonating.ID != customerID {
		t.Errorf("impersonating ID = %q, want the subject %q", impersonating.ID, customerID)
	}
}

// TestActorIsStaffIsNotAdmin: support staff must be able to read orders and work
// tickets without being able to change platform configuration.
func TestActorIsStaffIsNotAdmin(t *testing.T) {
	support := Actor{ID: "s", Roles: []string{RoleSupport}}
	if !support.IsStaff() {
		t.Error("support should be staff")
	}
	if support.IsAdmin() {
		t.Error("support must not be admin")
	}
	admin := Actor{ID: "a", Roles: []string{RoleAdmin}}
	if !admin.IsStaff() {
		t.Error("admin should also be staff (they can read orders)")
	}
	if !admin.IsAdmin() {
		t.Error("admin should be admin")
	}
}

func ptr(s string) *string { return &s }
