package service

import (
	"context"
	"testing"

	"github.com/vincommerce/backend/internal/repository"
)

// The widget badge needs each session's newest message without fetching every
// session's history. SessionsForUser carries it via a LATERAL join; this
// asserts the join reports the AGENT's latest line (not the customer's) and
// nothing at all for a session with no messages.
func TestSessionListingCarriesTheNewestMessage(t *testing.T) {
	pool := integrationPool(t)
	ctx := context.Background()

	var buyerID string
	if err := pool.QueryRow(ctx,
		`SELECT id::text FROM users WHERE email = 'buyer.sample@vincommerce.com'`,
	).Scan(&buyerID); err != nil {
		t.Fatalf("seeded buyer missing: %v", err)
	}

	var withMessages, empty string
	if err := pool.QueryRow(ctx,
		`INSERT INTO chat_sessions (user_id) VALUES ($1) RETURNING id::text`,
		buyerID).Scan(&withMessages); err != nil {
		t.Fatalf("insert session: %v", err)
	}
	if err := pool.QueryRow(ctx,
		`INSERT INTO chat_sessions (user_id) VALUES ($1) RETURNING id::text`,
		buyerID).Scan(&empty); err != nil {
		t.Fatalf("insert empty session: %v", err)
	}
	t.Cleanup(func() {
		_, _ = pool.Exec(ctx,
			`DELETE FROM chat_sessions WHERE id = $1 OR id = $2`, withMessages, empty)
	})

	if _, err := pool.Exec(ctx, `
		INSERT INTO chat_messages (session_id, sender_role, body) VALUES
			($1, 'customer', 'halo'),
			($1, 'agent', 'ada yang bisa dibantu?')`, withMessages); err != nil {
		t.Fatalf("insert messages: %v", err)
	}

	sessions, err := repository.NewChatRepository(pool).SessionsForUser(ctx, buyerID)
	if err != nil {
		t.Fatalf("SessionsForUser: %v", err)
	}
	byID := map[string]struct {
		role string
		at   bool
	}{}
	for _, s := range sessions {
		if s.ID == withMessages || s.ID == empty {
			byID[s.ID] = struct {
				role string
				at   bool
			}{s.LastMessageRole, s.LastMessageAt != nil}
		}
	}

	messaged, ok := byID[withMessages]
	if !ok {
		t.Fatal("the session with messages is missing from the listing")
	}
	if messaged.role != "agent" {
		t.Fatalf("newest message role = %q, want agent (the later of the two lines)", messaged.role)
	}
	if !messaged.at {
		t.Fatal("the session with messages reports no timestamp")
	}

	quiet, ok := byID[empty]
	if !ok {
		t.Fatal("the empty session is missing from the listing")
	}
	if quiet.role != "" || quiet.at {
		t.Fatalf("empty session reports role %q timestamp-present=%v, want neither",
			quiet.role, quiet.at)
	}
}
