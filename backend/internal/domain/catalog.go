package domain

import "time"

// Product statuses.
const (
	ProductDraft    = "draft"
	ProductActive   = "active"
	ProductInactive = "inactive"
	ProductRejected = "rejected"
)

// Review statuses.
const (
	ReviewPending  = "pending"
	ReviewApproved = "approved"
	ReviewRejected = "rejected"
)

// Category is a node in the category tree.
type Category struct {
	ID       string      `json:"id"`
	ParentID *string     `json:"parent_id,omitempty"`
	Name     string      `json:"name"`
	Slug     string      `json:"slug"`
	Path     string      `json:"path"`
	Depth    int         `json:"depth"`
	Position int         `json:"position"`
	IsActive bool        `json:"is_active"`
	Children []*Category `json:"children,omitempty"`
}

// Brand is a product manufacturer.
type Brand struct {
	ID       string `json:"id"`
	Name     string `json:"name"`
	Slug     string `json:"slug"`
	LogoURL  string `json:"logo_url,omitempty"`
	IsActive bool   `json:"is_active"`
}

// Attribute is a filterable spec dimension.
type Attribute struct {
	ID         string            `json:"id"`
	Name       string            `json:"name"`
	Slug       string            `json:"slug"`
	Filterable bool              `json:"filterable"`
	Searchable bool              `json:"searchable"`
	Position   int               `json:"position"`
	Values     []*AttributeValue `json:"values,omitempty"`
}

// AttributeValue is one option of an attribute.
type AttributeValue struct {
	ID    string `json:"id"`
	Value string `json:"value"`
	Slug  string `json:"slug"`
}

// Product is the sellable aggregate.
type Product struct {
	ID             string            `json:"id"`
	SellerID       string            `json:"seller_id"`
	CategoryID     *string           `json:"category_id,omitempty"`
	BrandID        *string           `json:"brand_id,omitempty"`
	Name           string            `json:"name"`
	Slug           string            `json:"slug"`
	Description    string            `json:"description,omitempty"`
	Status         string            `json:"status"`
	Attributes     map[string]string `json:"attributes,omitempty"`
	SeoTitle       string            `json:"seo_title,omitempty"`
	SeoDescription string            `json:"seo_description,omitempty"`
	AvgRating      float64           `json:"avg_rating"`
	RatingCount    int               `json:"rating_count"`
	SoldCount      int               `json:"sold_count"`
	PublishedAt    *time.Time        `json:"published_at,omitempty"`
	CreatedAt      time.Time         `json:"created_at"`
	UpdatedAt      time.Time         `json:"updated_at"`

	Category *Category         `json:"category,omitempty"`
	Brand    *Brand            `json:"brand,omitempty"`
	Variants []*ProductVariant `json:"variants,omitempty"`
	Images   []*ProductImage   `json:"images,omitempty"`
	Seller   *StoreSummary     `json:"seller,omitempty"`
}

// ProductVariant is a purchasable SKU.
type ProductVariant struct {
	ID             string            `json:"id"`
	ProductID      string            `json:"product_id"`
	SKU            string            `json:"sku"`
	Name           string            `json:"name"`
	Price          float64           `json:"price"`
	CompareAtPrice *float64          `json:"compare_at_price,omitempty"`
	Stock          int               `json:"stock"`
	WeightGrams    int               `json:"weight_grams"`
	ImageURL       string            `json:"image_url,omitempty"`
	Attributes     map[string]string `json:"attributes,omitempty"`
	IsActive       bool              `json:"is_active"`
}

// ProductImage is a gallery entry.
type ProductImage struct {
	ID        string `json:"id"`
	ProductID string `json:"product_id"`
	URL       string `json:"url"`
	Position  int    `json:"position"`
	IsPrimary bool   `json:"is_primary"`
}

// ProductReview is a buyer review.
type ProductReview struct {
	ID           string    `json:"id"`
	ProductID    string    `json:"product_id"`
	UserID       string    `json:"user_id"`
	OrderItemID  *string   `json:"order_item_id,omitempty"`
	Rating       int       `json:"rating"`
	Title        string    `json:"title,omitempty"`
	Content      string    `json:"content"`
	Images       []string  `json:"images,omitempty"`
	Status       string    `json:"status"`
	HelpfulCount int       `json:"helpful_count"`
	CreatedAt    time.Time `json:"created_at"`

	UserName          string `json:"user_name,omitempty"`
	VariantName       string `json:"variant_name,omitempty"`
	IsVerifiedPurchase bool  `json:"is_verified_purchase,omitempty"`
}

// RatingCount is one star bucket of the rating histogram.
type RatingCount struct {
	Rating int `json:"rating"`
	Count  int `json:"count"`
}

// NameSlug pairs a label with its URL slug (autocomplete suggestions).
type NameSlug struct {
	Name string `json:"name"`
	Slug string `json:"slug"`
}

// ProductFilter is the faceted search criteria.
type ProductFilter struct {
	Query        string
	CategoryID   string
	CategorySlug string
	BrandIDs     []string
	AttrFilters  map[string][]string
	MinPrice     *float64
	MaxPrice     *float64
	Rating       *int
	Sort         string // relevance | price_asc | price_desc | newest | bestseller | rating
	Page         int
	PageSize     int
	Fuzzy        bool // typo-tolerant fallback (pg_trgm similarity)
}

// StoreSummary is a lightweight seller row for product cards.
type StoreSummary struct {
	ID   string `json:"id"`
	Name string `json:"name"`
}

// SearchResult is a paged product listing.
type SearchResult struct {
	Items    []*Product `json:"items"`
	Total    int64      `json:"total"`
	Page     int        `json:"page"`
	PageSize int        `json:"page_size"`
	Facets   *Facets    `json:"facets,omitempty"`
}

// Facets are the available filter dimensions and their counts.
type Facets struct {
	Brands []*FacetCount `json:"brands,omitempty"`
	Attrs  []*FacetAttr  `json:"attributes,omitempty"`
}

// FacetCount is one filter option with a hit count.
type FacetCount struct {
	ID    string `json:"id"`
	Label string `json:"label"`
	Count int64  `json:"count"`
}

// FacetAttr is an attribute with its value counts.
type FacetAttr struct {
	Attribute *Attribute    `json:"attribute"`
	Values    []*FacetCount `json:"values"`
}

// FlashSale is a time-boxed discount campaign.
type FlashSale struct {
	ID          string    `json:"id"`
	Name        string    `json:"name"`
	Description string    `json:"description"`
	StartsAt    time.Time `json:"starts_at"`
	EndsAt      time.Time `json:"ends_at"`
	IsActive    bool      `json:"is_active"`
}

// FlashSaleItem is a variant discounted during a flash sale.
type FlashSaleItem struct {
	ID           string  `json:"id"`
	FlashSaleID  string  `json:"flash_sale_id"`
	VariantID    string  `json:"variant_id"`
	ProductID    string  `json:"product_id"`
	ProductName  string  `json:"product_name"`
	ProductSlug  string  `json:"product_slug"`
	VariantName  string  `json:"variant_name"`
	SKU          string  `json:"sku"`
	ImageURL     string  `json:"image_url,omitempty"`
	RegularPrice float64 `json:"regular_price"`
	SalePrice    float64 `json:"sale_price"`
	InitialStock int     `json:"initial_stock"`
	SoldCount    int     `json:"sold_count"`
	Stock        int     `json:"stock"`
}

// ProductQA is a buyer question with an optional seller answer.
type ProductQA struct {
	ID          string     `json:"id"`
	ProductID   string     `json:"product_id"`
	UserID      string     `json:"user_id"`
	Question    string     `json:"question"`
	Answer      string     `json:"answer,omitempty"`
	AnsweredAt  *time.Time `json:"answered_at,omitempty"`
	CreatedAt   time.Time  `json:"created_at"`
	AskUserName string     `json:"ask_user_name,omitempty"`
}

// Bundle is a discounted combo sold by a seller.
type Bundle struct {
	ID          string       `json:"id"`
	SellerID    string       `json:"seller_id"`
	Name        string       `json:"name"`
	Description string       `json:"description"`
	Price       float64      `json:"price"`
	IsActive    bool         `json:"is_active"`
	CreatedAt   time.Time    `json:"created_at"`
	Items       []BundleItem `json:"items,omitempty"`
}

// BundleItem links a variant to a bundle.
type BundleItem struct {
	VariantID string `json:"variant_id"`
	Quantity  int    `json:"quantity"`
}

// PriceAlert watches a variant for a target price.
type PriceAlert struct {
	ID          string    `json:"id"`
	UserID      string    `json:"user_id"`
	VariantID   string    `json:"variant_id"`
	TargetPrice float64   `json:"target_price"`
	Status      string    `json:"status"`
	CreatedAt   time.Time `json:"created_at"`

	CurrentPrice float64 `json:"current_price,omitempty"`
	ProductName  string  `json:"product_name,omitempty"`
	ProductSlug  string  `json:"product_slug,omitempty"`
	VariantName  string  `json:"variant_name,omitempty"`
}

// BackInStockAlert notifies a user when an out-of-stock variant is restocked.
type BackInStockAlert struct {
	ID          string     `json:"id"`
	UserID      string     `json:"user_id"`
	VariantID   string     `json:"variant_id"`
	Status      string     `json:"status"`
	CreatedAt   time.Time  `json:"created_at"`
	TriggeredAt *time.Time `json:"triggered_at,omitempty"`

	ProductName string `json:"product_name,omitempty"`
	ProductSlug string `json:"product_slug,omitempty"`
	VariantName string `json:"variant_name,omitempty"`
}
