package domain

import (
	"strings"
	"testing"
)

const validHash = "0123456789abcdef0123456789abcdef"

func TestNormalizeEvidenceURLsAcceptsUploadedFiles(t *testing.T) {
	raw := []string{
		"/uploads/" + validHash + ".jpg",
		"  https://api.example.com/uploads/" + validHash + ".webp  ",
	}
	got, err := NormalizeEvidenceURLs(raw, "https://api.example.com")
	if err != nil {
		t.Fatalf("NormalizeEvidenceURLs(%q) error = %v", raw, err)
	}
	if len(got) != 2 {
		t.Fatalf("got %d urls, want 2: %q", len(got), got)
	}
	// Padding is trimmed so the stored value is canonical.
	if got[1] != "https://api.example.com/uploads/"+validHash+".webp" {
		t.Fatalf("second url = %q, want the trimmed absolute form", got[1])
	}
}

func TestNormalizeEvidenceURLsRejectsAnythingWeDidNotUpload(t *testing.T) {
	cases := map[string]string{
		"javascript scheme":  "javascript:alert(1)",
		"data uri":           "data:image/svg+xml;base64,PHN2Zz48L3N2Zz4=",
		"file scheme":        "file:///etc/passwd",
		"external host":      "https://evil.example.com/uploads/" + validHash + ".jpg",
		"path traversal":     "/uploads/../../etc/passwd",
		"wrong directory":    "/static/" + validHash + ".jpg",
		"arbitrary filename": "/uploads/evil.jpg",
		"query string":       "/uploads/" + validHash + ".jpg?track=1",
		"fragment":           "/uploads/" + validHash + ".jpg#x",
		"userinfo":           "https://user:pass@api.example.com/uploads/" + validHash + ".jpg",
		"svg extension":      "/uploads/" + validHash + ".svg",
	}
	for name, raw := range cases {
		t.Run(name, func(t *testing.T) {
			if _, err := NormalizeEvidenceURLs([]string{raw}, "https://api.example.com"); err == nil {
				t.Fatalf("NormalizeEvidenceURLs(%q) = nil error, want BAD_EVIDENCE_URL", raw)
			}
		})
	}
}

func TestNormalizeEvidenceURLsCapsAndDeduplicates(t *testing.T) {
	if _, err := NormalizeEvidenceURLs([]string{
		"/uploads/" + validHash + ".jpg",
		"/uploads/" + validHash + ".png",
		"/uploads/" + validHash + ".webp",
		"/uploads/" + validHash + ".gif",
	}, "https://api.example.com"); err == nil || !strings.Contains(err.Error(), "at most 3") {
		t.Fatalf("four photos = %v, want the cap error", err)
	}

	got, err := NormalizeEvidenceURLs([]string{
		"/uploads/" + validHash + ".jpg",
		"https://api.example.com/uploads/" + validHash + ".jpg",
		" ",
	}, "https://api.example.com")
	if err != nil {
		t.Fatalf("duplicate hash error = %v", err)
	}
	if len(got) != 1 {
		t.Fatalf("duplicate of the same upload = %q, want one entry", got)
	}
}

func TestNormalizeEvidenceURLsAllowsNoPhotos(t *testing.T) {
	got, err := NormalizeEvidenceURLs(nil, "https://api.example.com")
	if err != nil || len(got) != 0 {
		t.Fatalf("no photos = %q, %v; want empty and no error", got, err)
	}
}
