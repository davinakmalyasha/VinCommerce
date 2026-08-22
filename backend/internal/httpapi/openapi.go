package httpapi

import (
	"encoding/json"
	"net/http"
)

// SpecVersion and service metadata for the OpenAPI document.
const (
	specVersion = "1.0.0"
	apiTitle    = "VinCommerce API"
	apiDesc     = "Enterprise-grade multi-vendor e-commerce marketplace API. Covers identity, catalog, cart & checkout, escrow payments, seller tools, support and analytics."
)

// OpenAPIDocument is a minimal OpenAPI 3.0 document.
type OpenAPIDocument struct {
	OpenAPI    string                 `json:"openapi"`
	Info       OpenAPIInfo            `json:"info"`
	Servers    []OpenAPIServer        `json:"servers"`
	Tags       []OpenAPITag           `json:"tags"`
	Paths      map[string]OpenAPIPath `json:"paths"`
	Components OpenAPIComponents      `json:"components"`
}

type OpenAPIInfo struct {
	Title       string `json:"title"`
	Description string `json:"description"`
	Version     string `json:"version"`
}

type OpenAPIServer struct {
	URL string `json:"url"`
}

type OpenAPITag struct {
	Name        string `json:"name"`
	Description string `json:"description"`
}

type OpenAPIPath map[string]OpenAPIOperation

type OpenAPIOperation struct {
	Tags        []string                   `json:"tags,omitempty"`
	Summary     string                     `json:"summary,omitempty"`
	Security    []map[string][]string      `json:"security,omitempty"`
	Parameters  []OpenAPIParameter         `json:"parameters,omitempty"`
	RequestBody *OpenAPIRequestBody        `json:"requestBody,omitempty"`
	Responses   map[string]OpenAPIResponse `json:"responses"`
}

type OpenAPIParameter struct {
	Name        string        `json:"name"`
	In          string        `json:"in"`
	Required    bool          `json:"required,omitempty"`
	Description string        `json:"description,omitempty"`
	Schema      OpenAPISchema `json:"schema"`
}

type OpenAPIRequestBody struct {
	Required bool                    `json:"required,omitempty"`
	Content  map[string]OpenAPIMedia `json:"content"`
}

type OpenAPIMedia struct {
	Schema OpenAPISchema `json:"schema"`
}

type OpenAPIResponse struct {
	Description string                  `json:"description"`
	Content     map[string]OpenAPIMedia `json:"content,omitempty"`
}

type OpenAPISchema struct {
	Type       string                   `json:"type,omitempty"`
	Format     string                   `json:"format,omitempty"`
	Ref        string                   `json:"$ref,omitempty"`
	Properties map[string]OpenAPISchema `json:"properties,omitempty"`
	Items      *OpenAPISchema           `json:"items,omitempty"`
	Required   []string                 `json:"required,omitempty"`
	Enum       []string                 `json:"enum,omitempty"`
}

type OpenAPIComponents struct {
	SecuritySchemes map[string]OpenAPISecurityScheme `json:"securitySchemes"`
	Schemas         map[string]OpenAPISchema         `json:"schemas"`
}

type OpenAPISecurityScheme struct {
	Type   string `json:"type"`
	Scheme string `json:"scheme"`
	In     string `json:"in,omitempty"`
}

// BuildOpenAPI generates the live API specification.
func BuildOpenAPI(baseURL string) *OpenAPIDocument {
	doc := &OpenAPIDocument{
		OpenAPI: "3.0.3",
		Info: OpenAPIInfo{
			Title: apiTitle, Description: apiDesc, Version: specVersion,
		},
		Servers: []OpenAPIServer{{URL: baseURL}},
		Tags: []OpenAPITag{
			{Name: "Auth", Description: "Registration, login, 2FA, sessions"},
			{Name: "Catalog", Description: "Categories, search, products, reviews"},
			{Name: "Cart", Description: "Cart and guest cart"},
			{Name: "Checkout", Description: "Quote and place orders"},
			{Name: "Orders", Description: "Order lifecycle and tracking"},
			{Name: "Payments", Description: "Escrow intents, wallets, webhooks"},
			{Name: "Seller", Description: "Stores, products, fulfillment"},
			{Name: "Support", Description: "Help center and tickets"},
		},
		Paths: map[string]OpenAPIPath{},
		Components: OpenAPIComponents{
			SecuritySchemes: map[string]OpenAPISecurityScheme{
				"bearerAuth": {Type: "http", Scheme: "bearer", In: "header"},
			},
			Schemas: schemas(),
		},
	}

	jsonResp := func(desc string) map[string]OpenAPIResponse {
		return map[string]OpenAPIResponse{
			"200": {Description: desc, Content: map[string]OpenAPIMedia{"application/json": {Schema: OpenAPISchema{Type: "object"}}}},
			"400": {Description: "Invalid request"},
			"401": {Description: "Unauthenticated"},
		}
	}
	auth := []map[string][]string{{"bearerAuth": {}}}
	strParam := func(name, in, desc string, required bool) OpenAPIParameter {
		return OpenAPIParameter{Name: name, In: in, Required: required, Description: desc, Schema: OpenAPISchema{Type: "string"}}
	}

	add := func(path, method, tag, summary string, security bool, params []OpenAPIParameter, responses map[string]OpenAPIResponse) {
		op := OpenAPIOperation{
			Tags: []string{tag}, Summary: summary, Parameters: params, Responses: responses,
		}
		if security {
			op.Security = auth
		}
		if doc.Paths[path] == nil {
			doc.Paths[path] = OpenAPIPath{}
		}
		doc.Paths[path][method] = op
	}

	// Auth
	add("/auth/register", "post", "Auth", "Register a buyer account", false, nil, jsonResp("Created"))
	add("/auth/login", "post", "Auth", "Login with email/password (optional TOTP)", false, nil, jsonResp("Tokens"))
	add("/auth/refresh", "post", "Auth", "Rotate a refresh token", false, nil, jsonResp("Tokens"))
	add("/auth/me", "get", "Auth", "Current user profile", true, nil, jsonResp("User"))
	add("/auth/sessions", "get", "Auth", "List active sessions", true, nil, jsonResp("Sessions"))
	add("/auth/2fa/setup", "post", "Auth", "Start TOTP enrollment", true, nil, jsonResp("Secret"))
	add("/auth/2fa/confirm", "post", "Auth", "Confirm TOTP and enable 2FA (returns one-time backup codes)", true, nil, jsonResp("Enabled+backup_codes"))
	add("/auth/2fa/disable", "post", "Auth", "Disable 2FA with a valid code", true, nil, jsonResp("Disabled"))
	add("/auth/2fa/backup-codes", "get", "Auth", "Backup-code count (total/remaining)", true, nil, jsonResp("Counts"))
	add("/auth/2fa/backup-codes/regenerate", "post", "Auth", "Replace backup codes, returns new set once", true, nil, jsonResp("BackupCodes"))

	// Catalog
	add("/catalog/categories", "get", "Catalog", "Category tree", false, nil, jsonResp("Categories"))
	add("/catalog/brands", "get", "Catalog", "Active brands", false, nil, jsonResp("Brands"))
	add("/catalog/attributes", "get", "Catalog", "Filterable attributes", false, nil, jsonResp("Attributes"))
	add("/products", "get", "Catalog", "Faceted product search", false,
		[]OpenAPIParameter{
			strParam("q", "query", "search text", false),
			strParam("category", "query", "category slug", false),
			strParam("brands", "query", "comma-separated brand ids", false),
			strParam("min_price", "query", "minimum price", false),
			strParam("max_price", "query", "maximum price", false),
			strParam("rating", "query", "minimum rating 1-5", false),
			strParam("sort", "query", "relevance|price_asc|price_desc|newest|bestseller|rating", false),
			strParam("page", "query", "page number", false),
			strParam("page_size", "query", "page size", false),
		}, jsonResp("SearchResult"))
	add("/products/{slug}", "get", "Catalog", "Product detail", false,
		[]OpenAPIParameter{strParam("slug", "path", "product slug", true)}, jsonResp("Product"))
	add("/products/{id}/reviews", "get", "Catalog", "Approved reviews", false,
		[]OpenAPIParameter{strParam("id", "path", "product id", true)}, jsonResp("Reviews"))
	add("/products/{id}/reviews", "post", "Catalog", "Submit a review", true,
		[]OpenAPIParameter{strParam("id", "path", "product id", true)}, jsonResp("Review"))
	add("/products/{id}/view", "post", "Catalog", "Analytics view beacon (optional auth)", false,
		[]OpenAPIParameter{strParam("id", "path", "product id", true)}, jsonResp("Ok"))
	add("/products/{id}/report", "post", "Catalog", "Report a product for moderation", true,
		[]OpenAPIParameter{strParam("id", "path", "product id", true)}, jsonResp("Reported"))
	add("/products/{id}/qa", "post", "Catalog", "Ask a question (buyer)", true,
		[]OpenAPIParameter{strParam("id", "path", "product id", true)}, jsonResp("Question"))
	add("/products/{id}/qa", "get", "Catalog", "Questions on a product", false,
		[]OpenAPIParameter{strParam("id", "path", "product id", true)}, jsonResp("Questions"))

	// Cart
	add("/cart", "get", "Cart", "Get cart (auth or guest)", false, nil, jsonResp("Cart"))
	add("/cart/items", "post", "Cart", "Add item to cart", false, nil, jsonResp("Cart"))
	add("/cart/items", "put", "Cart", "Update item quantity", false, nil, jsonResp("Cart"))
	add("/cart/items/{variantId}", "delete", "Cart", "Remove item", false,
		[]OpenAPIParameter{strParam("variantId", "path", "variant id", true)}, jsonResp("Cart"))
	add("/cart/bulk-remove", "post", "Cart", "Remove multiple items", false, nil, jsonResp("Cart"))
	add("/cart/bulk-move", "post", "Cart", "Move multiple items to wishlist (auth)", true, nil, jsonResp("Cart"))
	add("/cart/merge", "post", "Cart", "Merge guest cart into user cart", true, nil, jsonResp("Cart"))

	// Checkout
	add("/checkout/quote", "post", "Checkout", "Compute pricing breakdown", true, nil, jsonResp("Quote"))
	add("/checkout/place", "post", "Checkout", "Place orders (split by seller)", true, nil, jsonResp("PlacedOrder"))

	// Orders
	add("/orders", "get", "Orders", "List my orders", true, nil, jsonResp("Orders"))
	add("/orders/{id}", "get", "Orders", "Order detail with items", true,
		[]OpenAPIParameter{strParam("id", "path", "order id", true)}, jsonResp("Order"))
	add("/orders/{id}/cancel", "post", "Orders", "Cancel a pending order", true,
		[]OpenAPIParameter{strParam("id", "path", "order id", true)}, jsonResp("Cancelled"))
	add("/orders/{id}/confirm-delivery", "post", "Orders", "Confirm delivery (buyer)", true,
		[]OpenAPIParameter{strParam("id", "path", "order id", true)}, jsonResp("Delivered"))
	add("/orders/{id}/complete", "post", "Orders", "Complete order, release escrow", true,
		[]OpenAPIParameter{strParam("id", "path", "order id", true)}, jsonResp("Completed"))
	add("/orders/{id}/external-payment", "post", "Orders", "Record an off-platform payment (buyer pays outside the app)", true,
		[]OpenAPIParameter{strParam("id", "path", "order id", true)}, jsonResp("Confirmed"))
	add("/orders/tracking/{number}", "get", "Orders", "Track by order number", false,
		[]OpenAPIParameter{strParam("number", "path", "order number", true)}, jsonResp("Order+events"))

	// Payments
	add("/payments/orders/{orderId}/intent", "post", "Payments", "Create payment intent (idempotent). Methods: bank_transfer, e_wallet, midtrans_snap, wallet, cod. Returns snap_token/payment_url for Midtrans.", true,
		[]OpenAPIParameter{strParam("orderId", "path", "order id", true)}, jsonResp("Intent"))
	add("/payments/webhook/{gateway}", "post", "Payments", "Gateway webhook — sandbox HMAC (X-Webhook-Signature) or Midtrans SHA512 signature_key in body", false,
		[]OpenAPIParameter{strParam("gateway", "path", "gateway name (sandbox|midtrans)", true)}, jsonResp("Received"))
	add("/wallet", "get", "Payments", "Wallet balance + ledger", true, nil, jsonResp("Wallet"))
	add("/wallet/payouts", "post", "Payments", "Request a payout", true, nil, jsonResp("Payout"))
	// Seller
	add("/seller/store", "get", "Seller", "My store", true, nil, jsonResp("Store"))
	add("/seller/store", "post", "Seller", "Open a store", true, nil, jsonResp("Store"))
	add("/seller/kyc", "post", "Seller", "Submit KYC", true, nil, jsonResp("KYC"))
	add("/seller/products", "get", "Seller", "My products", true, nil, jsonResp("Products"))
	add("/seller/products", "post", "Seller", "Create product (draft)", true, nil, jsonResp("Product"))
	add("/seller/products/{id}", "put", "Seller", "Update product + variants", true,
		[]OpenAPIParameter{strParam("id", "path", "product id", true)}, jsonResp("Product"))
	add("/seller/products/{id}/status", "post", "Seller", "Activate/deactivate product", true,
		[]OpenAPIParameter{strParam("id", "path", "product id", true)}, jsonResp("Updated"))
	add("/seller/orders/{id}/transition", "post", "Seller", "Pack or ship an order", true,
		[]OpenAPIParameter{strParam("id", "path", "order id", true)}, jsonResp("Transitioned"))
	add("/seller/analytics", "get", "Seller", "Seller sales analytics", true, nil, jsonResp("Report"))
	add("/seller/returns", "get", "Seller", "Incoming return requests", true, nil, jsonResp("Returns"))
	add("/seller/returns/{id}/decide", "post", "Seller", "Approve/reject a return", true,
		[]OpenAPIParameter{strParam("id", "path", "return id", true)}, jsonResp("Decided"))
	add("/seller/bundles", "post", "Seller", "Create a bundle", true, nil, jsonResp("Bundle"))
	add("/seller/coupons", "post", "Seller", "Create a store coupon", true, nil, jsonResp("Coupon"))

	// Marketplace & engagement
	add("/stores/{slug}", "get", "Catalog", "Public store profile + products", false,
		[]OpenAPIParameter{strParam("slug", "path", "store slug", true)}, jsonResp("Store+products"))
	add("/stores/{id}/follow", "post", "Seller", "Follow a store", true,
		[]OpenAPIParameter{strParam("id", "path", "store id", true)}, jsonResp("Following"))
	add("/stores/{id}/follow", "delete", "Seller", "Unfollow a store", true,
		[]OpenAPIParameter{strParam("id", "path", "store id", true)}, jsonResp("Unfollowed"))
	add("/followed-stores", "get", "Seller", "Stores I follow", true, nil, jsonResp("Stores"))
	add("/followed-stores/feed", "get", "Seller", "Newest products from followed stores", true, nil, jsonResp("Products"))
	add("/price-alerts", "post", "Seller", "Watch a price drop", true, nil, jsonResp("Alert"))
	add("/price-alerts", "get", "Seller", "My price alerts", true, nil, jsonResp("Alerts"))
	add("/back-in-stock", "post", "Seller", "Watch for restock", true, nil, jsonResp("Alert"))
	add("/back-in-stock", "get", "Seller", "My restock watches", true, nil, jsonResp("Alerts"))
	add("/back-in-stock/{id}", "delete", "Seller", "Cancel a restock watch", true,
		[]OpenAPIParameter{strParam("id", "path", "alert id", true)}, jsonResp("Cancelled"))
	add("/wishlist", "get", "Catalog", "My wishlist", true, nil, jsonResp("Products"))
	add("/loyalty", "get", "Seller", "Loyalty points + ledger", true, nil, jsonResp("Balance+ledger"))
	add("/referral/code", "get", "Seller", "My referral code", true, nil, jsonResp("Code"))
	add("/referral/redeem", "post", "Seller", "Redeem a referral code", true, nil, jsonResp("Redeemed"))
	add("/admin/reports", "get", "Seller", "Product reports queue (admin)", true, nil, jsonResp("Reports"))
	add("/admin/reports/{id}/resolve", "post", "Seller", "Resolve a report, optionally takedown (admin)", true,
		[]OpenAPIParameter{strParam("id", "path", "report id", true)}, jsonResp("Resolved"))
	add("/admin/analytics", "get", "Seller", "Platform analytics incl. commission earned (admin)", true, nil, jsonResp("Report"))

	// Support
	add("/help/categories", "get", "Support", "Help categories", false, nil, jsonResp("Categories"))
	add("/help/articles", "get", "Support", "Search published articles", false,
		[]OpenAPIParameter{
			strParam("section", "query", "help|docs|legal", false),
			strParam("q", "query", "search text", false),
		}, jsonResp("Articles"))
	add("/help/articles/{slug}", "get", "Support", "Article detail", false,
		[]OpenAPIParameter{strParam("slug", "path", "article slug", true)}, jsonResp("Article"))
	add("/tickets", "post", "Support", "Open a support ticket", true, nil, jsonResp("Ticket"))
	add("/tickets", "get", "Support", "My tickets", true, nil, jsonResp("Tickets"))
	add("/tickets/{id}", "get", "Support", "Ticket + thread", true,
		[]OpenAPIParameter{strParam("id", "path", "ticket id", true)}, jsonResp("Ticket+Messages"))
	add("/tickets/{id}/messages", "post", "Support", "Reply on a ticket", true,
		[]OpenAPIParameter{strParam("id", "path", "ticket id", true)}, jsonResp("Message"))
	add("/notifications", "get", "Support", "Notification feed", true, nil, jsonResp("Notifications"))
	add("/notifications/unread-count", "get", "Support", "Unread badge count", true, nil, jsonResp("Count"))
	add("/notifications/read", "post", "Support", "Mark notifications read", true, nil, jsonResp("Read"))

	return doc
}

func schemas() map[string]OpenAPISchema {
	str := func(format string) OpenAPISchema { return OpenAPISchema{Type: "string", Format: format} }
	num := func() OpenAPISchema { return OpenAPISchema{Type: "number", Format: "double"} }
	obj := func(props map[string]OpenAPISchema) OpenAPISchema {
		return OpenAPISchema{Type: "object", Properties: props}
	}
	arr := func(items OpenAPISchema) OpenAPISchema { return OpenAPISchema{Type: "array", Items: &items} }

	return map[string]OpenAPISchema{
		"User": obj(map[string]OpenAPISchema{
			"id": str("uuid"), "email": str("email"), "full_name": str(""), "roles": arr(str("")),
			"status": str(""), "two_factor_enabled": {Type: "boolean"},
		}),
		"Product": obj(map[string]OpenAPISchema{
			"id": str("uuid"), "name": str(""), "slug": str(""), "description": str(""),
			"avg_rating": num(), "rating_count": {Type: "integer"}, "sold_count": {Type: "integer"},
			"variants": arr(OpenAPISchema{Ref: "#/components/schemas/Variant"}),
		}),
		"Variant": obj(map[string]OpenAPISchema{
			"id": str("uuid"), "sku": str(""), "name": str(""), "price": num(),
			"compare_at_price": num(), "stock": {Type: "integer"}, "is_active": {Type: "boolean"},
		}),
		"Order": obj(map[string]OpenAPISchema{
			"id": str("uuid"), "order_number": str(""), "status": str(""),
			"subtotal": num(), "discount_amount": num(), "shipping_fee": num(), "total_amount": num(),
			"payment_status": str(""), "placed_at": str("date-time"),
			"items": arr(OpenAPISchema{Ref: "#/components/schemas/OrderItem"}),
		}),
		"OrderItem": obj(map[string]OpenAPISchema{
			"product_name": str(""), "variant_name": str(""), "sku": str(""),
			"unit_price": num(), "quantity": {Type: "integer"}, "total": num(),
		}),
		"CartLine": obj(map[string]OpenAPISchema{
			"variant_id": str("uuid"), "product_name": str(""), "variant_name": str(""),
			"price": num(), "subtotal": num(), "quantity": {Type: "integer"}, "stock": {Type: "integer"},
		}),
		"Quote": obj(map[string]OpenAPISchema{
			"subtotal": num(), "discount_amount": num(), "shipping": arr(OpenAPISchema{Type: "object"}),
			"total": num(),
		}),
		"PaymentIntent": obj(map[string]OpenAPISchema{
			"id": str("uuid"), "order_id": str("uuid"), "amount": num(), "status": str(""),
			"gateway": str(""), "gateway_ref": str(""),
		}),
		"Ticket": obj(map[string]OpenAPISchema{
			"id": str("uuid"), "ticket_number": str(""), "subject": str(""), "category": str(""),
			"priority": str(""), "status": str(""), "created_at": str("date-time"),
		}),
		"Notification": obj(map[string]OpenAPISchema{
			"id": {Type: "integer"}, "type": str(""), "title": str(""), "body": str(""),
			"read_at": str("date-time"), "created_at": str("date-time"),
		}),
		"HelpArticle": obj(map[string]OpenAPISchema{
			"id": str("uuid"), "title": str(""), "slug": str(""), "excerpt": str(""),
			"content": str(""), "section": str(""), "is_published": {Type: "boolean"},
		}),
	}
}

// OpenAPIHandler serves the JSON specification.
func OpenAPIHandler(baseURL string) http.HandlerFunc {
	doc := BuildOpenAPI(baseURL)
	return func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.Header().Set("Access-Control-Allow-Origin", "*")
		_ = json.NewEncoder(w).Encode(doc)
	}
}
