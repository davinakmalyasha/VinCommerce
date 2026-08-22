package ai

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"math"
	"net/http"
	"sort"
	"strings"
	"time"
)

// Config selects the assistant backend. Set AI_API_KEY to enable LLM mode.
type Config struct {
	BaseURL string // OpenAI-compatible base URL, e.g. https://api.openai.com/v1
	APIKey  string // empty => offline retrieval mode
	Model   string // e.g. gpt-4o-mini
}

// Document is a retrievable knowledge chunk.
type Document struct {
	ID      string `json:"id"`
	Title   string `json:"title"`
	Content string `json:"content"`
	Source  string `json:"source"`
}

// Assistant answers questions using offline retrieval, optionally backed by an LLM (RAG).
type Assistant struct {
	cfg    Config
	docs   []Document
	client *http.Client
	logger *slog.Logger
}

// NewAssistant builds the assistant with a knowledge base.
func NewAssistant(cfg Config, docs []Document, logger *slog.Logger) *Assistant {
	return &Assistant{
		cfg:    cfg,
		docs:   docs,
		client: &http.Client{Timeout: 30 * time.Second},
		logger: logger,
	}
}

// LLMEnabled reports whether the API key is configured.
func (a *Assistant) LLMEnabled() bool { return a.cfg.APIKey != "" }

// Answer is the assistant response.
type Answer struct {
	Answer  string     `json:"answer"`
	Sources []Document `json:"sources,omitempty"`
	Mode    string     `json:"mode"` // offline | llm
}

// Ask retrieves the best-matching articles and answers.
func (a *Assistant) Ask(ctx context.Context, question string) (*Answer, error) {
	if strings.TrimSpace(question) == "" {
		return nil, fmt.Errorf("empty question")
	}
	top := a.retrieve(question, 3)
	sources := []Document{}
	for _, hit := range top {
		sources = append(sources, a.docs[hit.idx])
	}

	if a.LLMEnabled() {
		ans, err := a.askLLM(ctx, question, sources)
		if err == nil {
			return &Answer{Answer: ans, Sources: sources, Mode: "llm"}, nil
		}
		a.logger.Warn("llm fallback to offline", "error", err)
	}

	return &Answer{Answer: a.offlineAnswer(question, top), Sources: sources, Mode: "offline"}, nil
}

// DescribeProduct generates a product description (LLM or template).
func (a *Assistant) DescribeProduct(ctx context.Context, name, category string, attrs map[string]string) (string, error) {
	if a.LLMEnabled() {
		prompt := fmt.Sprintf("Tulis deskripsi produk e-commerce yang menarik dalam Bahasa Indonesia untuk '%s' (kategori: %s). Atribut: %v. Maksimal 3 kalimat, profesional, tanpa klaim berlebihan.",
			name, category, attrs)
		if ans, err := a.askLLM(ctx, prompt, nil); err == nil {
			return ans, nil
		}
	}
	return templateDescription(name, category, attrs), nil
}

// --- offline retrieval (TF-IDF cosine) ---

type hit struct {
	idx   int
	score float64
}

func (a *Assistant) retrieve(query string, k int) []hit {
	q := tokenize(query)
	if len(q) == 0 {
		return nil
	}

	// TF-IDF scoring over title (weighted) + content.
	scores := make([]float64, len(a.docs))
	df := map[string]int{}
	tf := make([]map[string]int, len(a.docs))

	for i, d := range a.docs {
		terms := tokenize(d.Title + " " + d.Title + " " + d.Content) // title x2 weight
		m := map[string]int{}
		for _, t := range terms {
			m[t]++
		}
		tf[i] = m
		for t := range m {
			df[t]++
		}
	}

	n := float64(len(a.docs))
	for i, m := range tf {
		for _, t := range q {
			if count, ok := m[t]; ok {
				idf := math.Log(1 + n/(1+float64(df[t])))
				scores[i] += float64(count) * idf
			}
		}
	}

	hits := make([]hit, 0, len(a.docs))
	for i, s := range scores {
		if s > 0 {
			hits = append(hits, hit{idx: i, score: s})
		}
	}
	sort.Slice(hits, func(i, j int) bool { return hits[i].score > hits[j].score })
	if len(hits) > k {
		hits = hits[:k]
	}
	return hits
}

func (a *Assistant) offlineAnswer(question string, hits []hit) string {
	if len(hits) == 0 {
		return "Maaf, saya belum menemukan artikel yang cocok di pusat bantuan. " +
			"Kamu bisa menghubungi tim dukungan lewat tiket atau chat dengan agen."
	}
	top := a.docs[hits[0].idx]
	// Extract a readable snippet from the article content (strip HTML tags).
	plain := stripHTML(top.Content)
	if len(plain) > 400 {
		plain = plain[:400] + "…"
	}
	return fmt.Sprintf("Berdasarkan artikel \"%s\": %s", top.Title, plain)
}

// --- LLM mode (OpenAI-compatible chat completions) ---

type chatRequest struct {
	Model     string        `json:"model"`
	Messages  []chatMessage `json:"messages"`
	MaxTokens int           `json:"max_tokens,omitempty"`
}

type chatMessage struct {
	Role    string `json:"role"`
	Content string `json:"content"`
}

type chatResponse struct {
	Choices []struct {
		Message chatMessage `json:"message"`
	} `json:"choices"`
}

func (a *Assistant) askLLM(ctx context.Context, question string, sources []Document) (string, error) {
	system := "Kamu adalah asisten dukungan VinCommerce, platform e-commerce marketplace Indonesia. " +
		"Jawab dengan ringkas dan ramah dalam Bahasa Indonesia. Gunakan hanya konteks yang diberikan; jika tidak ada konteks yang relevan, arahkan ke pusat bantuan atau tiket dukungan."
	if len(sources) > 0 {
		var b strings.Builder
		b.WriteString("Konteks dari pusat bantuan:\n")
		for _, s := range sources {
			b.WriteString(fmt.Sprintf("- [%s] %s\n", s.Title, stripHTML(s.Content)))
		}
		system += "\n" + b.String()
	}

	body := chatRequest{
		Model: a.cfg.Model,
		Messages: []chatMessage{
			{Role: "system", Content: system},
			{Role: "user", Content: question},
		},
		MaxTokens: 500,
	}
	payload, _ := json.Marshal(body)

	req, err := http.NewRequestWithContext(ctx, http.MethodPost,
		strings.TrimSuffix(a.cfg.BaseURL, "/")+"/chat/completions", bytes.NewReader(payload))
	if err != nil {
		return "", err
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+a.cfg.APIKey)

	resp, err := a.client.Do(req)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()

	raw, err := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if err != nil {
		return "", err
	}
	if resp.StatusCode != http.StatusOK {
		return "", fmt.Errorf("llm status %d: %s", resp.StatusCode, strings.TrimSpace(string(raw)))
	}
	var cr chatResponse
	if err := json.Unmarshal(raw, &cr); err != nil {
		return "", err
	}
	if len(cr.Choices) == 0 {
		return "", fmt.Errorf("llm empty response")
	}
	return strings.TrimSpace(cr.Choices[0].Message.Content), nil
}

// --- text helpers ---

func tokenize(s string) []string {
	s = strings.ToLower(s)
	s = stripHTML(s)
	replacer := strings.NewReplacer(
		".", " ", ",", " ", "!", " ", "?", " ", ":", " ", ";", " ", "(", " ", ")", " ",
		"-", " ", "_", " ", "/", " ", "\\", " ", "'", " ", "\"", " ", "&", " ",
	)
	s = replacer.Replace(s)
	return strings.Fields(s)
}

func stripHTML(s string) string {
	var b strings.Builder
	inTag := false
	for _, r := range s {
		switch {
		case r == '<':
			inTag = true
		case r == '>':
			inTag = false
		case !inTag:
			b.WriteRune(r)
		}
	}
	return strings.Join(strings.Fields(b.String()), " ")
}

func templateDescription(name, category string, attrs map[string]string) string {
	attrList := []string{}
	for k, v := range attrs {
		attrList = append(attrList, fmt.Sprintf("%s %s", k, v))
	}
	desc := fmt.Sprintf("%s adalah produk pilihan dalam kategori %s. ", name, category)
	if len(attrList) > 0 {
		desc += "Spesifikasi utama: " + strings.Join(attrList, ", ") + ". "
	}
	desc += "Produk ini dijual oleh penjual terverifikasi dan dilindungi pembayaran escrow VinCommerce."
	return desc
}
