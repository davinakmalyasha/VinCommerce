package service

import (
	"bytes"
	"context"
	"fmt"
	"sync"
	"time"

	"golang.org/x/sync/singleflight"

	"github.com/vincommerce/backend/internal/cache"
	"github.com/vincommerce/backend/internal/domain"
	"github.com/vincommerce/backend/internal/repository"
)

// SeoService builds robots.txt and sitemap.xml.
//
// It exists because this logic used to live in httpapi/handler/seo.go, where
// the handler held two concrete repositories and a cache store and composed the
// XML itself. That inverted the dependency direction -- a transport type
// reaching into persistence -- and it put a long-held mutex around two database
// queries (see the singleflight note on Sitemap below).
type SeoService struct {
	products   ProductSlugSource
	categories CategoryTreeSource
	cache      *cache.Store
	webURL     string

	// inflight collapses concurrent rebuilds. See Sitemap.
	inflight singleflight.Group
	mu       sync.Mutex
	xml      []byte
	builtAt  time.Time
}

// ProductSlugSource is the persistence slice SeoService needs from the product
// catalogue. Defining it here, at the consumer, is the dependency inversion the
// rest of the codebase is missing: the service depends on what it uses, not on
// a concrete repository type.
type ProductSlugSource interface {
	AllActiveSlugs(ctx context.Context) ([]*repository.SlugEntry, error)
}

// CategoryTreeSource is the persistence slice SeoService needs from categories.
type CategoryTreeSource interface {
	Tree(ctx context.Context) ([]*domain.Category, error)
}

// NewSeoService constructs the service.
func NewSeoService(products ProductSlugSource, categories CategoryTreeSource, c *cache.Store, webURL string) *SeoService {
	return &SeoService{products: products, categories: categories, cache: c, webURL: webURL}
}

// sitemapTTL is both the in-process freshness window and the HTTP
// Cache-Control the handler sends. One constant so they cannot drift: a
// Cache-Control longer than the rebuild interval would serve a stale sitemap to
// crawlers for no reason, and a shorter one would rebuild needlessly.
const sitemapTTL = time.Hour

// staticSitemapPaths are the pages that always exist and never change content
// in a way a crawler needs to re-fetch.
var staticSitemapPaths = []string{
	"/", "/search", "/flash-sales", "/vouchers", "/compare",
	"/help", "/faq", "/contact", "/terms", "/privacy", "/refund-policy", "/shipping-policy",
}

// Sitemap returns the sitemap body, rebuilding it at most once per TTL.
//
// The previous implementation held a plain sync.Mutex across the whole build,
// including both database queries. A single slow catalogue scan therefore
// serialised every concurrent request for the duration of that scan, and the
// mutex queue behind it was unbounded -- a handful of concurrent crawlers was
// enough to turn one slow query into a latency spike for all of them.
//
// The fix is singleflight semantics: concurrent callers that arrive during a
// rebuild all wait for the SAME build rather than each queueing to redo it. The
// fast path (cache still warm) does not take the lock at all.
func (s *SeoService) Sitemap(ctx context.Context) ([]byte, error) {
	// Fast path: read under the mutex, then do all I/O outside it.
	s.mu.Lock()
	fresh := s.xml != nil && time.Since(s.builtAt) < sitemapTTL
	body := s.xml
	s.mu.Unlock()
	if fresh {
		return body, nil
	}

	built, err, _ := s.inflight.Do("sitemap", func() (any, error) {
		return s.build(ctx)
	})
	if err != nil {
		return nil, err
	}
	return built.([]byte), nil
}

// build renders the XML. Only ever called through the singleflight group.
func (s *SeoService) build(ctx context.Context) ([]byte, error) {
	var buf bytes.Buffer
	buf.WriteString(`<?xml version="1.0" encoding="UTF-8"?>` + "\n")
	buf.WriteString(`<urlset xmlns="http://www.sitemaps.org/schemas/sitemap/0.9">` + "\n")

	for _, p := range staticSitemapPaths {
		fmt.Fprintf(&buf, "  <url><loc>%s%s</loc><changefreq>weekly</changefreq></url>\n", s.webURL, p)
	}

	// A category-tree failure emits the static half rather than failing the
	// whole request: a degraded sitemap is better than a 500 to a crawler.
	if tree, err := s.categories.Tree(ctx); err == nil {
		var walk func(items []*domain.Category)
		walk = func(items []*domain.Category) {
			for _, c := range items {
				fmt.Fprintf(&buf, "  <url><loc>%s/search?category=%s</loc><changefreq>daily</changefreq></url>\n", s.webURL, c.Slug)
				if len(c.Children) > 0 {
					walk(c.Children)
				}
			}
		}
		walk(tree)
	} else {
		return nil, err
	}

	slugs, err := s.products.AllActiveSlugs(ctx)
	if err != nil {
		return nil, err
	}
	for _, sl := range slugs {
		fmt.Fprintf(&buf, "  <url><loc>%s/product/%s</loc><lastmod>%s</lastmod></url>\n",
			s.webURL, sl.Slug, sl.UpdatedAt.Format("2006-01-02"))
	}

	buf.WriteString(`</urlset>` + "\n")
	out := append([]byte(nil), buf.Bytes()...)

	s.mu.Lock()
	s.xml = out
	s.builtAt = time.Now()
	s.mu.Unlock()

	// The Redis copy is for cross-replica warm starts. The in-process copy
	// already serves this replica, so a cache error here is genuinely
	// best-effort and is not logged as a failure.
	if s.cache != nil {
		_ = s.cache.Set(ctx, "sitemap.xml", out, sitemapTTL)
	}
	return out, nil
}

// RobotsTxt is the robots.txt body.
func (s *SeoService) RobotsTxt() []byte {
	return []byte("User-agent: *\nAllow: /\n\nSitemap: " + s.webURL + "/sitemap.xml\n")
}
