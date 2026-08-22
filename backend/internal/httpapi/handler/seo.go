package handler

import (
	"bytes"
	"context"
	"fmt"
	"net/http"
	"sync"
	"time"

	"github.com/vincommerce/backend/internal/cache"
	"github.com/vincommerce/backend/internal/domain"
	"github.com/vincommerce/backend/internal/repository"
)

// SEO serves robots.txt and sitemap.xml for search engines.
type SEO struct {
	products   *repository.ProductRepository
	categories *repository.CategoryRepository
	cache      *cache.Store
	webURL     string

	mu      sync.Mutex
	xml     []byte
	builtAt time.Time
}

// NewSEO creates a SEO handler.
func NewSEO(products *repository.ProductRepository, categories *repository.CategoryRepository, c *cache.Store, webURL string) *SEO {
	return &SEO{products: products, categories: categories, cache: c, webURL: webURL}
}

// Robots handles GET /robots.txt.
func (h *SEO) Robots(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "text/plain; charset=utf-8")
	_, _ = w.Write([]byte("User-agent: *\nAllow: /\n\nSitemap: " + h.webURL + "/sitemap.xml\n"))
}

// Sitemap handles GET /sitemap.xml (cached for 1 hour).
func (h *SEO) Sitemap(w http.ResponseWriter, r *http.Request) {
	body, err := h.sitemapXML(r.Context())
	if err != nil {
		writeErr(w, r, err)
		return
	}
	w.Header().Set("Content-Type", "application/xml; charset=utf-8")
	w.Header().Set("Cache-Control", "public, max-age=3600")
	_, _ = w.Write(body)
}

func (h *SEO) sitemapXML(ctx context.Context) ([]byte, error) {
	h.mu.Lock()
	defer h.mu.Unlock()
	if h.xml != nil && time.Since(h.builtAt) < time.Hour {
		return h.xml, nil
	}

	var buf bytes.Buffer
	buf.WriteString(`<?xml version="1.0" encoding="UTF-8"?>` + "\n")
	buf.WriteString(`<urlset xmlns="http://www.sitemaps.org/schemas/sitemap/0.9">` + "\n")

	static := []string{
		"/", "/search", "/flash-sales", "/vouchers", "/compare",
		"/help", "/faq", "/contact", "/terms", "/privacy", "/refund-policy", "/shipping-policy",
	}
	for _, p := range static {
		fmt.Fprintf(&buf, "  <url><loc>%s%s</loc><changefreq>weekly</changefreq></url>\n", h.webURL, p)
	}

	if tree, err := h.categories.Tree(ctx); err == nil {
		var walk func(items []*domain.Category)
		walk = func(items []*domain.Category) {
			for _, c := range items {
				fmt.Fprintf(&buf, "  <url><loc>%s/search?category=%s</loc><changefreq>daily</changefreq></url>\n", h.webURL, c.Slug)
				if len(c.Children) > 0 {
					walk(c.Children)
				}
			}
		}
		walk(tree)
	}

	if slugs, err := h.products.AllActiveSlugs(ctx); err == nil {
		for _, s := range slugs {
			fmt.Fprintf(&buf, "  <url><loc>%s/product/%s</loc><lastmod>%s</lastmod></url>\n",
				h.webURL, s.Slug, s.UpdatedAt.Format("2006-01-02"))
		}
	}

	buf.WriteString(`</urlset>` + "\n")

	// best-effort redis cache (keyed copy for memory too)
	_ = h.cache.Set(ctx, "sitemap.xml", buf.Bytes(), time.Hour)
	h.xml = append([]byte(nil), buf.Bytes()...)
	h.builtAt = time.Now()
	return h.xml, nil
}
