package service

import (
	"context"
	"fmt"
	"math"
	"strings"

	"github.com/vincommerce/backend/internal/ai"
	"github.com/vincommerce/backend/internal/repository"
)

// AIService exposes the assistant and search suggestions.
type AIService struct {
	assistant  *ai.Assistant
	products   *repository.ProductRepository
	categories *repository.CategoryRepository
	orders     *repository.OrderRepository
	reviews    *repository.ReviewRepository
}

// NewAIService creates an AIService.
func NewAIService(assistant *ai.Assistant, products *repository.ProductRepository, categories *repository.CategoryRepository) *AIService {
	return &AIService{assistant: assistant, products: products, categories: categories}
}

// SetContext enables order-aware answers and review summaries.
func (s *AIService) SetContext(orders *repository.OrderRepository, reviews *repository.ReviewRepository) {
	s.orders = orders
	s.reviews = reviews
}

// Ask queries the assistant (offline or LLM).
func (s *AIService) Ask(ctx context.Context, question string) (*ai.Answer, error) {
	return s.assistant.Ask(ctx, question)
}

// AskForUser answers with the user's recent order context (RAG + personalization).
func (s *AIService) AskForUser(ctx context.Context, userID, question string) (*ai.Answer, error) {
	context := ""
	if s.orders != nil {
		orders, total, err := s.orders.ListByBuyer(ctx, userID, 1, 5)
		if err == nil && total > 0 {
			context = "Konteks pesanan pengguna ini:\n"
			for _, o := range orders {
				context += fmt.Sprintf("- %s: status %s, total Rp %.0f, kurir %s\n",
					o.OrderNumber, o.Status, o.TotalAmount, o.ShippingMethod)
			}
		}
	}
	if context != "" {
		question = context + "\nPertanyaan pengguna: " + question
	}
	return s.assistant.Ask(ctx, question)
}

// ReviewSummary aggregates ratings and summarizes sentiment.
func (s *AIService) ReviewSummary(ctx context.Context, productID string) (map[string]any, error) {
	reviews, err := s.reviews.ListAllApproved(ctx, productID, 200)
	if err != nil {
		return nil, err
	}
	dist := map[int]int{1: 0, 2: 0, 3: 0, 4: 0, 5: 0}
	total := 0.0
	for _, r := range reviews {
		dist[r.Rating]++
		total += float64(r.Rating)
	}
	avg := 0.0
	if len(reviews) > 0 {
		avg = total / float64(len(reviews))
	}

	summary := ""
	if len(reviews) >= 3 {
		pos := dist[5] + dist[4]
		neg := dist[1] + dist[2]
		switch {
		case pos >= len(reviews)*2/3:
			summary = "Mayoritas pembeli merasa puas dengan produk ini."
		case neg >= len(reviews)/3:
			summary = "Sebagian pembeli melaporkan masalah; cek ulasan negatif sebelum membeli."
		default:
			summary = "Tanggapan pembeli beragam; sebagian besar netral hingga positif."
		}
	} else if len(reviews) > 0 {
		summary = "Masih sedikit ulasan, namun pembeli yang sudah menulis umumnya " + tern(avg >= 4, "puas.", "cukup.")
	} else {
		summary = "Belum ada ulasan untuk produk ini."
	}

	return map[string]any{
		"average":      math.Round(avg*10) / 10,
		"total":        len(reviews),
		"distribution": dist,
		"summary":      summary,
	}, nil
}

// TitleSuggestions proposes product title variants.
func (s *AIService) TitleSuggestions(ctx context.Context, name string) ([]string, error) {
	if s.assistant.LLMEnabled() {
		prompt := "Buat 3 variasi judul produk e-commerce dalam Bahasa Indonesia untuk: '" + name + "'. Format: satu judul per baris, maksimal 70 karakter, tanpa nomor."
		if ans, err := s.assistant.Ask(ctx, prompt); err == nil {
			lines := strings.Split(ans.Answer, "\n")
			out := make([]string, 0, 3)
			for _, l := range lines {
				l = strings.TrimSpace(strings.TrimLeft(l, "-•·0123456789. "))
				if l != "" {
					out = append(out, l)
				}
			}
			if len(out) >= 2 {
				return out[:3], nil
			}
		}
	}
	base := strings.TrimSpace(name)
	return []string{
		base,
		base + " — Kualitas Premium",
		base + " | Harga Terbaik",
	}, nil
}

func tern(cond bool, a, b string) string {
	if cond {
		return a
	}
	return b
}

// DescribeProduct generates a seller-facing description.
func (s *AIService) DescribeProduct(ctx context.Context, name, category string, attrs map[string]string) (string, error) {
	return s.assistant.DescribeProduct(ctx, name, category, attrs)
}

// Suggestion is one autocomplete entry.
type Suggestion struct {
	Name string `json:"name"`
	Slug string `json:"slug"`
}

// Suggest returns search autocompletions (products + categories + brands).
func (s *AIService) Suggest(ctx context.Context, q string, limit int) (map[string][]*Suggestion, error) {
	if limit < 1 || limit > 10 {
		limit = 6
	}
	q = strings.TrimSpace(q)
	out := map[string][]*Suggestion{"products": {}, "categories": {}, "brands": {}}

	if q == "" {
		return out, nil
	}

	if names, err := s.products.ProductSuggestions(ctx, q, limit); err == nil {
		for _, n := range names {
			out["products"] = append(out["products"], &Suggestion{Name: n.Name, Slug: n.Slug})
		}
	}
	if cats, err := s.categories.CategorySuggestions(ctx, q, limit); err == nil {
		for _, c := range cats {
			out["categories"] = append(out["categories"], &Suggestion{Name: c.Name, Slug: c.Slug})
		}
	}
	if brands, err := s.categories.BrandSuggestions(ctx, q, limit); err == nil {
		for _, b := range brands {
			out["brands"] = append(out["brands"], &Suggestion{Name: b.Name, Slug: b.Slug})
		}
	}
	return out, nil
}
