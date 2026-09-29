package domain

import (
	"errors"
	"fmt"
)

// ErrorKind classifies a domain error for HTTP mapping.
type ErrorKind string

const (
	KindInvalid         ErrorKind = "invalid_request"
	KindUnauthenticated ErrorKind = "unauthenticated"
	KindForbidden       ErrorKind = "forbidden"
	KindNotFound        ErrorKind = "not_found"
	KindConflict        ErrorKind = "conflict"
	KindRateLimited     ErrorKind = "rate_limited"
	KindInternal        ErrorKind = "internal"
)

// Error is the canonical typed error used across the application.
type Error struct {
	Kind    ErrorKind
	Code    string
	Message string
	Cause   error
}

func (e *Error) Error() string {
	if e.Cause != nil {
		return fmt.Sprintf("%s (%s): %v", e.Message, e.Code, e.Cause)
	}
	return fmt.Sprintf("%s (%s)", e.Message, e.Code)
}

func (e *Error) Unwrap() error { return e.Cause }

// Is lets errors.Is match a *Error against a sentinel by kind and code rather
// than by pointer identity.
//
// Without this, `errors.Is(err, domain.ErrNotFound)` silently degrades to
// comparing two *pointers*, because *Error has no Is method and its Unwrap
// returns Cause (usually nil). That fails even when the error IS the sentinel:
// `E(KindNotFound, "NOT_FOUND", ...)` builds a fresh struct. Every sentinel in
// this file is a package-level value, so no code path can ever return the same
// pointer, and every `errors.Is(err, domain.ErrX)` in the codebase was
// permanently false rather than occasionally false -- the worst failure mode
// for a guard, because it compiles, type-checks, and reads as a real check.
//
// Prefer domain.Is(err, kind, code) where you want to match on kind alone, for
// example a repository that reports "not found" with its own specific code.
func (e *Error) Is(target error) bool {
	t, ok := target.(*Error)
	if !ok {
		return false
	}
	return e.Kind == t.Kind && e.Code == t.Code
}

// E constructs a domain error.
func E(kind ErrorKind, code, message string) *Error {
	return &Error{Kind: kind, Code: code, Message: message}
}

// Wrap constructs a domain error with a cause.
func Wrap(kind ErrorKind, code, message string, cause error) *Error {
	return &Error{Kind: kind, Code: code, Message: message, Cause: cause}
}

// ErrorKindOf extracts the kind of an error, defaulting to KindInternal.
func ErrorKindOf(err error) ErrorKind {
	var de *Error
	if errors.As(err, &de) {
		return de.Kind
	}
	return KindInternal
}

// Is reports whether err is a domain error with the given kind or code.
func Is(err error, kind ErrorKind, code string) bool {
	var de *Error
	if !errors.As(err, &de) {
		return false
	}
	return de.Kind == kind && (code == "" || de.Code == code)
}

// Sentinel domain errors.
var (
	ErrEmailTaken        = E(KindConflict, "EMAIL_ALREADY_REGISTERED", "email is already registered")
	ErrPhoneTaken        = E(KindConflict, "PHONE_ALREADY_REGISTERED", "phone number is already registered")
	ErrInvalidCreds      = E(KindUnauthenticated, "INVALID_CREDENTIALS", "invalid email or password")
	ErrUserDisabled      = E(KindForbidden, "ACCOUNT_DISABLED", "account is disabled")
	ErrEmailUnverified   = E(KindForbidden, "EMAIL_UNVERIFIED", "email is not verified")
	ErrNotFound          = E(KindNotFound, "NOT_FOUND", "resource not found")
	ErrTokenExpired      = E(KindUnauthenticated, "TOKEN_EXPIRED", "token has expired")
	ErrTokenInvalid      = E(KindUnauthenticated, "TOKEN_INVALID", "token is invalid")
	ErrSessionRevoked    = E(KindUnauthenticated, "SESSION_REVOKED", "session has been revoked")
	ErrTwoFactorRequired = E(KindInvalid, "TWO_FACTOR_REQUIRED", "two-factor verification code required")
	ErrTwoFactorInvalid  = E(KindInvalid, "TWO_FACTOR_INVALID", "invalid two-factor code")
)
