package handler

import (
	"net/http"

	"github.com/vincommerce/backend/internal/service"
)

// SEO serves robots.txt and sitemap.xml.
//
// The sitemap was generated here until this handler was 97 lines of XML
// composition holding two concrete repositories and a cache store, which
// inverted the dependency direction: a transport type reaching into persistence
// and building a business artefact. It is now SeoService, and this handler only
// translates an HTTP request into a service call and a service result into a
// response.
type SEO struct {
	svc *service.SeoService
}

// NewSEO creates a SEO handler.
func NewSEO(svc *service.SeoService) *SEO { return &SEO{svc: svc} }

// Robots handles GET /robots.txt.
func (h *SEO) Robots(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "text/plain; charset=utf-8")
	_, _ = w.Write(h.svc.RobotsTxt())
}

// Sitemap handles GET /sitemap.xml.
//
// Cache-Control is the same TTL the service rebuilds at, so a crawler is never
// told to hold a copy for longer than the server would serve a fresh one.
func (h *SEO) Sitemap(w http.ResponseWriter, r *http.Request) {
	body, err := h.svc.Sitemap(r.Context())
	if err != nil {
		writeErr(w, r, err)
		return
	}
	w.Header().Set("Content-Type", "application/xml; charset=utf-8")
	w.Header().Set("Cache-Control", "public, max-age=3600")
	_, _ = w.Write(body)
}
