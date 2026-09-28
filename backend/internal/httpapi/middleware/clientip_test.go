package middleware

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func req(remoteAddr string, headers map[string]string) *http.Request {
	r := httptest.NewRequest(http.MethodGet, "/api/v1/products", nil)
	r.RemoteAddr = remoteAddr
	for k, v := range headers {
		r.Header.Set(k, v)
	}
	return r
}

func TestClientIPIgnoresSpoofedForwardedForWhenPeerIsUntrusted(t *testing.T) {
	SetTrustedProxies(nil) // nothing is trusted: the safe default

	cases := []struct {
		name    string
		remote  string
		headers map[string]string
		want    string
	}{
		{
			name:   "no header at all",
			remote: "203.0.113.7:51344",
			want:   "203.0.113.7",
		},
		{
			// The exact bypass: chi's RealIP took the leftmost forwarded
			// value unconditionally, so this became the rate-limit key.
			name:    "forged X-Forwarded-For from an untrusted peer",
			remote:  "203.0.113.7:51344",
			headers: map[string]string{"X-Forwarded-For": "1.2.3.4"},
			want:    "203.0.113.7",
		},
		{
			name:    "multi-entry forged chain",
			remote:  "203.0.113.7:51344",
			headers: map[string]string{"X-Forwarded-For": "1.2.3.4, 5.6.7.8, 9.10.11.12"},
			want:    "203.0.113.7",
		},
		{
			name:    "forged X-Real-IP",
			remote:  "203.0.113.7:51344",
			headers: map[string]string{"X-Real-IP": "1.2.3.4"},
			want:    "203.0.113.7",
		},
		{
			name:    "garbage header value",
			remote:  "203.0.113.7:51344",
			headers: map[string]string{"X-Forwarded-For": "not-an-ip"},
			want:    "203.0.113.7",
		},
	}

	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := ClientIP(req(c.remote, c.headers)); got != c.want {
				t.Fatalf("ClientIP = %q, want %q", got, c.want)
			}
		})
	}
}

func TestClientIPExcludesSourcePort(t *testing.T) {
	SetTrustedProxies(nil)
	// The bug: ipKey used r.RemoteAddr, so the ephemeral port made every
	// connection a fresh rate-limit bucket.
	a := ClientIP(req("203.0.113.7:51344", nil))
	b := ClientIP(req("203.0.113.7:60999", nil))
	if a != b {
		t.Fatalf("same IP, different ports produced different keys: %q vs %q", a, b)
	}
	if strings.Contains(a, ":") {
		t.Fatalf("ClientIP %q still contains a port", a)
	}
}

func TestClientIPWalksForwardedChainThroughTrustedProxy(t *testing.T) {
	SetTrustedProxies([]string{"10.0.0.0/8", "172.16.0.0/12"})

	cases := []struct {
		name    string
		remote  string
		headers map[string]string
		want    string
	}{
		{
			name:    "single hop",
			remote:  "10.0.0.5:1234",
			headers: map[string]string{"X-Forwarded-For": "198.51.100.22"},
			want:    "198.51.100.22",
		},
		{
			// nginx appends the real peer, so the chain is
			// "<forged>, <real client>". Walking right-to-left and skipping
			// trusted hops finds the real client, not the forged entry.
			name:    "forged entry prepended to a real chain",
			remote:  "10.0.0.5:1234",
			headers: map[string]string{"X-Forwarded-For": "1.1.1.1, 198.51.100.22"},
			want:    "198.51.100.22",
		},
		{
			name:    "two trusted hops",
			remote:  "172.20.0.9:1234",
			headers: map[string]string{"X-Forwarded-For": "198.51.100.22, 10.0.0.7"},
			want:    "198.51.100.22",
		},
		{
			name:   "trusted peer but no header",
			remote: "10.0.0.5:1234",
			want:   "10.0.0.5",
		},
	}

	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := ClientIP(req(c.remote, c.headers)); got != c.want {
				t.Fatalf("ClientIP = %q, want %q", got, c.want)
			}
		})
	}

	SetTrustedProxies(nil)
}

func TestSetTrustedProxiesAcceptsBareIPsAndIgnoresGarbage(t *testing.T) {
	SetTrustedProxies([]string{"10.0.0.5", "192.168.1.0/24", "not-a-cidr", "", "999.999.999.999"})
	r := req("10.0.0.5:1234", map[string]string{"X-Forwarded-For": "198.51.100.9"})
	if got := ClientIP(r); got != "198.51.100.9" {
		t.Fatalf("bare IP should be trusted, got %q", got)
	}
	r2 := req("10.9.9.9:1234", map[string]string{"X-Forwarded-For": "198.51.100.9"})
	if got := ClientIP(r2); got != "10.9.9.9" {
		t.Fatalf("unlisted IP must not be trusted, got %q", got)
	}
	SetTrustedProxies(nil)
}

func TestRequestIDRejectsHostileValues(t *testing.T) {
	cases := []struct {
		name   string
		header string
		wantOK bool
	}{
		{"valid uuid", "6f1c2d3e-4a5b-6c7d-8e9f-0a1b2c3d4e5f", true},
		{"simple token", "abc-123_x.y", true},
		{"empty", "", false},
		// entity_id is VARCHAR(64): an oversized value made the audit INSERT
		// fail with "value too long", silently dropping the record.
		{"too long", strings.Repeat("a", 65), false},
		{"exactly 64", strings.Repeat("a", 64), true},
		// CR/LF would be written into every log line and the audit trail.
		{"crlf injection", "abc\r\nGET /admin/x HTTP/1.1", false},
		{"null byte", "abc\x00def", false},
		{"semicolon", "abc;def", false},
		{"space", "abc def", false},
		{"angle bracket", "<script>", false},
	}

	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := validRequestID(c.header); got != c.wantOK {
				t.Fatalf("validRequestID(%q) = %v, want %v", c.header, got, c.wantOK)
			}
		})
	}
}

func TestRequestIDAlwaysProducesBoundedID(t *testing.T) {
	for _, h := range []string{"", strings.Repeat("z", 500), "bad\r\nid"} {
		r := httptest.NewRequest(http.MethodGet, "/x", nil)
		if h != "" {
			r.Header.Set("X-Request-ID", h)
		}
		w := httptest.NewRecorder()
		var got string
		RequestID(http.HandlerFunc(func(_ http.ResponseWriter, rr *http.Request) {
			got = RequestIDFrom(rr.Context())
		})).ServeHTTP(w, r)

		if got == "" {
			t.Fatalf("header %q produced an empty request id", h)
		}
		if len(got) > maxRequestIDLen {
			t.Fatalf("header %q produced id %q of length %d, over the %d column",
				h, got, len(got), maxRequestIDLen)
		}
		if w.Header().Get("X-Request-ID") != got {
			t.Fatal("response header does not match the context value")
		}
	}
}

func TestSafeInetRejectsHostileAddresses(t *testing.T) {
	// A non-empty unparseable value made `NULLIF($5,'')::inet` raise and fail
	// the entire audit INSERT.
	bad := []string{"x", "javascript:alert(1)", "1.2.3.4.5", ":::", "999.999.999.999", "<script>"}
	for _, v := range bad {
		if got := safeInet(v); got != "" {
			t.Errorf("safeInet(%q) = %q, want empty", v, got)
		}
	}
	good := map[string]string{
		"203.0.113.7":      "203.0.113.7",
		"203.0.113.7:1234": "203.0.113.7",
		"  203.0.113.7  ":  "203.0.113.7",
		"2001:db8::1":      "2001:db8::1",
		"":                 "",
	}
	for in, want := range good {
		if got := safeInet(in); got != want {
			t.Errorf("safeInet(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestAuditActionAlwaysFitsColumn(t *testing.T) {
	// A path over ~58 characters overflowed audit_log.action VARCHAR(64),
	// making those routes permanently unauditable.
	long := "/api/v1/payments/sandbox/orders/6f1c2d3e-4a5b-6c7d-8e9f-0a1b2c3d4e5f/approve"
	r := httptest.NewRequest(http.MethodPost, long, nil)
	got := auditAction(r)
	if len([]rune(got)) > maxAuditActionLen {
		t.Fatalf("auditAction = %q (%d runes), over the %d column", got, len([]rune(got)), maxAuditActionLen)
	}
	if !strings.HasPrefix(got, "POST ") {
		t.Fatalf("auditAction = %q, want a POST prefix", got)
	}
	// Identifiers must be reduced to a placeholder so the action is stable
	// across requests and filterable.
	if !strings.Contains(got, ":id") {
		t.Fatalf("auditAction = %q, want the uuid reduced to :id", got)
	}
}

func TestAuditActionIsDiscriminating(t *testing.T) {
	cases := []struct {
		method, path, want string
	}{
		{"POST", "/api/v1/orders", "POST orders"},
		{"POST", "/api/v1/orders/6f1c2d3e-4a5b-6c7d-8e9f-0a1b2c3d4e5f/cancel", "POST orders/:id/cancel"},
		{"DELETE", "/api/v1/admin/users/12345", "DELETE admin/users/:id"},
		{"PUT", "/api/v1/seller/products/42/variants", "PUT seller/products/:id/variants"},
	}
	for _, c := range cases {
		if got := auditAction(httptest.NewRequest(c.method, c.path, nil)); got != c.want {
			t.Errorf("auditAction(%s %s) = %q, want %q", c.method, c.path, got, c.want)
		}
	}
}
