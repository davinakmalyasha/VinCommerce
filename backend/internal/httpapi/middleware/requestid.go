package middleware

import (
	"context"
	"net"
	"net/http"
	"strings"
	"sync"

	"github.com/google/uuid"
)

type ctxKey string

const requestIDKey ctxKey = "request_id"

// maxRequestIDLen is the width of audit_log.entity_id. An inbound X-Request-ID
// was accepted at any length, so an oversized value made every audit INSERT for
// that request fail with `value too long`.
const maxRequestIDLen = 64

// validRequestIDChars keeps an inbound correlation ID from carrying CR/LF (log
// injection) or shell/JSON metacharacters into the audit trail.
func validRequestID(id string) bool {
	if len(id) == 0 || len(id) > maxRequestIDLen {
		return false
	}
	for _, r := range id {
		switch {
		case r >= 'a' && r <= 'z', r >= 'A' && r <= 'Z', r >= '0' && r <= '9':
		case r == '-', r == '_', r == '.', r == ':':
		default:
			return false
		}
	}
	return true
}

// RequestID injects a unique request ID into the context and response headers.
//
// An inbound X-Request-ID is honoured so a caller can correlate across
// services, but ONLY if it is well-formed. Previously any value was accepted
// verbatim and then written to audit_log.entity_id (VARCHAR(64)) as the
// entity identifier of every privileged action — so a caller could either
// overflow the column (destroying the audit row) or supply an arbitrary string
// that operators would then read as a trusted correlation ID.
func RequestID(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		id := r.Header.Get("X-Request-ID")
		if !validRequestID(id) {
			id = uuid.NewString()
		}
		w.Header().Set("X-Request-ID", id)
		ctx := context.WithValue(r.Context(), requestIDKey, id)
		next.ServeHTTP(w, r.WithContext(ctx))
	})
}

// RequestIDFrom returns the request ID stored in the context.
func RequestIDFrom(ctx context.Context) string {
	id, _ := ctx.Value(requestIDKey).(string)
	return id
}

// ---------------------------------------------------------------------------
// Client IP resolution
// ---------------------------------------------------------------------------

// trustedProxyNets is the set of networks whose forwarded-for headers we
// believe, installed by SetTrustedProxies at startup.
var (
	trustedProxyMu   sync.RWMutex
	trustedProxyNets []*net.IPNet
)

// ClientIP resolves the real client address.
//
// It replaces chi's deprecated RealIP middleware, which is documented as
// vulnerable to spoofing (GHSA-3fxj-6jh8-hvhx and two others) and takes the
// LEFTMOST X-Forwarded-For entry unconditionally — i.e. whatever the client
// sent first. That value keyed every per-IP rate limiter, so sending a fresh
// forged header per request gave an attacker an unlimited bucket per request:
// unlimited password brute force against /auth/login, unlimited account
// registration, and unmetered spend on the LLM-backed /ai/ask endpoint.
//
// The address is also returned WITHOUT the port. chi's RealIP only rewrites
// RemoteAddr when a forwarded header is present, so `ipKey` on the raw
// RemoteAddr produced "203.0.113.7:51344" — a different key per connection,
// because the source port is ephemeral. Any client that opens a new socket per
// request defeated every limiter.
//
// Algorithm: walk X-Forwarded-For right-to-left, skipping trusted proxies,
// and return the first untrusted address. Falls back to the transport peer.
func ClientIP(r *http.Request) string {
	if ip, ok := clientIPFromForwarded(r); ok {
		return ip
	}
	// r.RemoteAddr may have been rewritten by a proxy-aware layer; it holds a
	// bare address in that case, otherwise host:port.
	if host, _, err := net.SplitHostPort(r.RemoteAddr); err == nil {
		return host
	}
	return strings.TrimSpace(r.RemoteAddr)
}

func clientIPFromForwarded(r *http.Request) (string, bool) {
	header := r.Header.Get("X-Forwarded-For")
	if header == "" {
		header = r.Header.Get("X-Real-IP")
	}
	if header == "" {
		return "", false
	}
	// Only honour the header when the direct peer is itself a trusted proxy.
	// Without this check any client could forge the chain.
	peer := peerIP(r)
	if peer == nil || !isTrustedProxy(peer) {
		return "", false
	}

	parts := strings.Split(header, ",")
	for i := len(parts) - 1; i >= 0; i-- {
		candidate := strings.TrimSpace(parts[i])
		ip := net.ParseIP(candidate)
		if ip == nil {
			continue
		}
		if isTrustedProxy(ip) {
			continue
		}
		return ip.String(), true
	}
	return "", false
}

func peerIP(r *http.Request) net.IP {
	if host, _, err := net.SplitHostPort(r.RemoteAddr); err == nil {
		return net.ParseIP(host)
	}
	return net.ParseIP(strings.TrimSpace(r.RemoteAddr))
}

func isTrustedProxy(ip net.IP) bool {
	trustedProxyMu.RLock()
	nets := trustedProxyNets
	trustedProxyMu.RUnlock()
	for _, n := range nets {
		if n != nil && n.Contains(ip) {
			return true
		}
	}
	return false
}

// SetTrustedProxies installs the CIDR list used by ClientIP. An empty or
// unparseable list means NO proxy is trusted, so ClientIP ignores forwarded
// headers entirely and uses the transport peer. That is the safe default: it
// degrades to "cannot see through a proxy" rather than "trusts anything".
func SetTrustedProxies(cidrs []string) {
	trustedProxyMu.Lock()
	defer trustedProxyMu.Unlock()
	trustedProxyNets = nil
	for _, c := range cidrs {
		c = strings.TrimSpace(c)
		if c == "" {
			continue
		}
		if !strings.Contains(c, "/") {
			// Accept a bare IP as a /32 or /128.
			if ip := net.ParseIP(c); ip != nil {
				bits := 32
				if ip.To4() == nil {
					bits = 128
				}
				trustedProxyNets = append(trustedProxyNets, &net.IPNet{IP: ip, Mask: net.CIDRMask(bits, bits)})
			}
			continue
		}
		if _, n, err := net.ParseCIDR(c); err == nil {
			trustedProxyNets = append(trustedProxyNets, n)
		}
	}
}
