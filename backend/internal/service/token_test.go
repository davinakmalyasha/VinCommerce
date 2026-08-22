package service

import (
	"testing"
	"time"

	"github.com/vincommerce/backend/internal/domain"
)

func TestPasswordHashAndVerify(t *testing.T) {
	p := NewPassword(64*1024, 3, 2, 16)

	hash, err := p.Hash("correct horse battery staple")
	if err != nil {
		t.Fatalf("hash: %v", err)
	}

	ok, err := p.Verify("correct horse battery staple", hash)
	if err != nil || !ok {
		t.Fatalf("expected valid password to verify: ok=%v err=%v", ok, err)
	}

	ok, err = p.Verify("wrong password", hash)
	if err != nil || ok {
		t.Fatalf("expected wrong password to fail: ok=%v err=%v", ok, err)
	}
}

func TestPasswordRejectsMalformedHash(t *testing.T) {
	p := NewPassword(64*1024, 3, 2, 16)
	if _, err := p.Verify("pw", "not-a-hash"); err == nil {
		t.Fatal("expected error for malformed hash")
	}
}

func TestTokenIssueAndParse(t *testing.T) {
	tm := NewTokenManager("test-secret", 15*time.Minute, 24*time.Hour)
	user := &domain.User{ID: "user-1", Roles: []string{domain.RoleBuyer}}

	token, _, err := tm.IssueAccess(user)
	if err != nil {
		t.Fatalf("issue: %v", err)
	}

	parsed, err := tm.ParseAccess(token)
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	if parsed.ID != "user-1" {
		t.Fatalf("expected user-1, got %s", parsed.ID)
	}
	if !parsed.HasRole(domain.RoleBuyer) {
		t.Fatal("expected buyer role")
	}
}

func TestTokenRejectsWrongSecret(t *testing.T) {
	tm := NewTokenManager("test-secret", 15*time.Minute, 24*time.Hour)
	other := NewTokenManager("different-secret", 15*time.Minute, 24*time.Hour)

	token, _, err := tm.IssueAccess(&domain.User{ID: "user-1"})
	if err != nil {
		t.Fatalf("issue: %v", err)
	}
	if _, err := other.ParseAccess(token); err == nil {
		t.Fatal("expected parse failure with wrong secret")
	}
}

func TestRefreshTokenHashing(t *testing.T) {
	tm := NewTokenManager("s", time.Minute, time.Hour)
	raw, hash, err := tm.NewRefreshToken()
	if err != nil {
		t.Fatalf("new: %v", err)
	}
	if tm.HashRefresh(raw) != hash {
		t.Fatal("hash mismatch for same token")
	}
	if hash == raw {
		t.Fatal("hash must not equal raw token")
	}
}
