package main

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"os"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/vincommerce/backend/internal/config"
	"github.com/vincommerce/backend/internal/db"
	"github.com/vincommerce/backend/internal/domain"
	"github.com/vincommerce/backend/internal/repository"
	"github.com/vincommerce/backend/internal/service"
)

func main() {
	logger := slog.New(slog.NewTextHandler(os.Stdout, nil))
	slog.SetDefault(logger)

	if err := run(logger); err != nil {
		logger.Error("seed failed", "error", err)
		os.Exit(1)
	}
}

func run(logger *slog.Logger) error {
	cfg, err := config.Load()
	if err != nil {
		return err
	}

	// Safety: seeding inserts demo accounts with well-known credentials.
	// Never allow it against a production database unless explicitly forced.
	if cfg.Environment == "production" && os.Getenv("SEED_FORCE") != "yes" {
		return fmt.Errorf("refusing to seed a production environment (known-credential demo accounts); set SEED_FORCE=yes to override")
	}

	ctx := context.Background()
	pool, err := db.Connect(ctx, cfg.Database)
	if err != nil {
		return err
	}
	defer pool.Close()

	if err := db.Migrate(ctx, cfg.Database); err != nil {
		return err
	}

	users := repository.NewUserRepository(pool)
	categories := repository.NewCategoryRepository(pool)
	products := repository.NewProductRepository(pool)
	reviews := repository.NewReviewRepository(pool)

	password := service.NewPassword(cfg.Auth.Argon2Memory, cfg.Auth.Argon2Iterations, cfg.Auth.Argon2Parallelism, cfg.Auth.Argon2SaltLength)
	catalogSvc := service.NewCatalogService(categories)

	s := &seeder{
		ctx: ctx, users: users, categories: categories, products: products, reviews: reviews,
		catalog: catalogSvc, password: password,
	}

	seeded := 0
	seeded += s.seedUsers()
	seeded += s.seedCategories()
	seeded += s.seedAttributes()
	seeded += s.seedBrandsAndProducts()
	seeded += s.seedHelpContent()
	seeded += s.seedFlags()
	seeded += s.seedCoupons()
	seeded += s.seedDemoOrders()

	logger.Info("seed complete", "items", seeded)
	return nil
}

// seedDemoOrders creates a realistic 30-day order history so platform and
// seller analytics render populated charts out of the box.
func (s *seeder) seedDemoOrders() int {
	var buyerID string
	if err := s.products.Pool().QueryRow(s.ctx,
		`SELECT id FROM users WHERE email = 'buyer.sample@vincommerce.com'`).Scan(&buyerID); err != nil {
		fmt.Println("seed demo orders: no demo buyer:", err)
		return 0
	}

	type vp struct {
		id, productName, vname, sku, sellerID string
		price                                 float64
	}
	rows, err := s.products.Pool().Query(s.ctx, `
		SELECT v.id, p.name, v.name, v.sku, v.price, p.seller_id
		FROM product_variants v
		JOIN products p ON p.id = v.product_id
		WHERE p.status = 'active'
		ORDER BY p.sold_count DESC
		LIMIT 24`)
	if err != nil {
		fmt.Println("seed demo orders: variant pool:", err)
		return 0
	}
	defer rows.Close()
	pool := []vp{}
	for rows.Next() {
		var v vp
		if err := rows.Scan(&v.id, &v.productName, &v.vname, &v.sku, &v.price, &v.sellerID); err != nil {
			continue
		}
		pool = append(pool, v)
	}
	if len(pool) == 0 {
		return 0
	}

	type plan struct {
		status string
		pay    string
	}
	plans := []plan{
		{"completed", "paid"}, {"completed", "paid"}, {"completed", "paid"},
		{"delivered", "paid"}, {"delivered", "paid"}, {"shipped", "paid"},
		{"packed", "paid"}, {"paid", "paid"}, {"cancelled", "refunded"},
	}

	addr := map[string]any{
		"recipient": "Sample Buyer", "phone": "081200000006",
		"address_line1": "Jl. Mawar No. 12", "city": "Jakarta Selatan",
		"province": "DKI Jakarta", "postal_code": "12440",
	}
	addrJSON, _ := json.Marshal(addr)

	created := 0
	now := time.Now().UTC()
	for i := 0; i < 42; i++ {
		p := plans[i%len(plans)]
		daysAgo := 29 - (i % 30)
		placed := now.AddDate(0, 0, -daysAgo).Add(time.Duration(i%20) * time.Hour)

		v1 := pool[(i*7)%len(pool)]
		qty1 := 1 + i%2
		subtotal := v1.price * float64(qty1)
		shipping := 12000.0
		total := subtotal + shipping

		var seq int64
		if err := s.products.Pool().QueryRow(s.ctx, `SELECT nextval('order_number_seq')`).Scan(&seq); err != nil {
			continue
		}
		orderNumber := fmt.Sprintf("VC-%s-%04d", placed.Format("20060102"), seq)

		var paidAt, shippedAt, deliveredAt, completedAt, cancelledAt any
		switch p.status {
		case "paid":
			paidAt = placed.Add(30 * time.Minute)
		case "packed", "shipped", "delivered", "completed":
			paidAt = placed.Add(25 * time.Minute)
			shippedAt = placed.Add(24 * time.Hour)
			deliveredAt = placed.Add(72 * time.Hour)
			completedAt = placed.Add(5 * 24 * time.Hour)
		case "cancelled":
			cancelledAt = placed.Add(45 * time.Minute)
		}

		var orderID string
		err := s.products.Pool().QueryRow(s.ctx, `
			INSERT INTO orders (id, order_number, buyer_id, seller_id, status, currency,
			                    subtotal, discount_amount, shipping_fee, total_amount, payment_status,
			                    shipping_address, shipping_method, notes, placed_at,
			                    paid_at, shipped_at, delivered_at, completed_at, cancelled_at,
			                    created_at, updated_at)
			VALUES (gen_random_uuid(), $1, $2, $3, $4, 'IDR',
			        $5, 0, $6, $7, $8,
			        $9, 'Regular (Standard)', NULL, $10,
			        $11, $12, $13, $14, $15,
			        $10, now())
			RETURNING id`,
			orderNumber, buyerID, v1.sellerID, p.status, subtotal, shipping, total, p.pay,
			addrJSON, placed, paidAt, shippedAt, deliveredAt, completedAt, cancelledAt).
			Scan(&orderID)
		if err != nil {
			continue
		}

		if _, err := s.products.Pool().Exec(s.ctx, `
			INSERT INTO order_items (id, order_id, product_id, variant_id, seller_id, product_name,
			                         variant_name, sku, unit_price, quantity, weight_grams, total, image_url, status)
			SELECT gen_random_uuid(), $1, v.product_id, v.id, v.seller_id, $2, $3, v.sku, v.price, $4,
			       COALESCE(v.weight_grams, 500), v.price*$4, COALESCE(v.image_url,''), $5
			FROM product_variants v WHERE v.id = $6`,
			orderID, v1.productName, v1.vname, qty1, itemStatus(p.status, placed), v1.id); err != nil {
			continue
		}
		created++
	}
	return created
}

// itemStatus mirrors the parent order status onto items.
func itemStatus(order string, placed time.Time) string {
	switch order {
	case "completed":
		return "completed"
	case "cancelled":
		return "cancelled"
	default:
		return order
	}
}

// seedCoupons creates the demo coupons advertised in the README plus
// spin-the-wheel prize coupons.
func (s *seeder) seedCoupons() int {
	md := 15000.0
	coupons := []struct {
		code        string
		typ         string
		value       float64
		minSubtotal float64
		maxDiscount *float64
		prize       bool
	}{
		{"WELCOME10", "percent", 10, 50000, nil, false},
		{"FLAT50K", "fixed", 50000, 200000, nil, false},
		{"GRATIS15", "percent", 5, 100000, &md, false},
		{"LUCKY20K", "fixed", 20000, 75000, nil, true},
		{"SPIN10", "percent", 8, 0, nil, true},
	}
	for _, c := range coupons {
		if _, err := s.products.Pool().Exec(s.ctx, `
			INSERT INTO coupons (code, type, value, min_subtotal, max_discount, per_user_limit, is_active, is_prize)
			VALUES ($1, $2, $3, $4, $5, 1, TRUE, $6)
			ON CONFLICT (code) DO UPDATE SET is_active = TRUE, is_prize = EXCLUDED.is_prize`,
			c.code, c.typ, c.value, c.minSubtotal, c.maxDiscount, c.prize); err != nil {
			fmt.Println("seed coupon:", err)
			continue
		}
	}
	return len(coupons)
}

// seedFlags ensures feature flags exist (enabled) so the flag-gated routes work out of the box.
func (s *seeder) seedFlags() int {
	flags := []struct{ key, desc string }{
		{"flash_sales", "Flash sale engine on storefront"},
		{"referrals", "Referral program"},
		{"loyalty_points", "Loyalty points earn & ledger"},
		{"ai_assistant", "AI assistant widget"},
		{"games", "Check-in streaks & spin-the-wheel"},
		{"live_commerce", "Livestream selling"},
	}
	for _, f := range flags {
		if _, err := s.products.Pool().Exec(s.ctx, `
			INSERT INTO feature_flags (id, key, description, enabled)
			VALUES (gen_random_uuid(), $1, $2, TRUE)
			ON CONFLICT (key) DO NOTHING`, f.key, f.desc); err != nil {
			fmt.Println("seed flag:", err)
			continue
		}
	}
	return len(flags)
}

func (s *seeder) seedHelpContent() int {
	cats := []struct {
		name, slug string
	}{
		{"Akun & Keamanan", "account"},
		{"Pesanan", "orders"},
		{"Pembayaran", "payments"},
		{"Pengiriman", "shipping"},
		{"Retur & Refund", "returns"},
	}
	created := 0
	catIDs := map[string]string{}
	for _, c := range cats {
		var id string
		err := s.products.Pool().QueryRow(s.ctx, `
			INSERT INTO help_categories (id, name, slug, position) VALUES (gen_random_uuid(), $1, $2, $3)
			ON CONFLICT (slug) DO UPDATE SET name = EXCLUDED.name
			RETURNING id`, c.name, c.slug, created).Scan(&id)
		if err != nil {
			fmt.Println("seed help category:", err)
			continue
		}
		catIDs[c.slug] = id
		created++
	}

	type article struct {
		title, excerpt, content, section, cat string
	}
	articles := []article{
		{"Bagaimana cara membuat akun?", "Langkah-langkah mendaftar akun pembeli di VinCommerce.",
			"<p>1. Buka halaman <b>Daftar</b>.<br>2. Isi nama lengkap, email, dan password (minimal 8 karakter).<br>3. Klik <b>Daftar</b> — akun langsung aktif.<br>4. Verifikasi email opsional via link yang dikirim ke inbox.</p>", "help", "account"},
		{"Cara login dan mengamankan akun dengan 2FA", "Aktifkan verifikasi dua langkah untuk keamanan maksimal.",
			"<p>Login dengan email dan password. Untuk keamanan tambahan, aktifkan <b>2FA</b> di menu akun:<br>1. Buka Profil → keamanan.<br>2. Scan kode QR dengan aplikasi autentikator (Google Authenticator).<br>3. Masukkan kode 6 digit untuk konfirmasi.</p>", "help", "account"},
		{"Bagaimana cara memesan produk?", "Dari keranjang hingga pesanan berhasil dibuat.",
			"<p>1. Cari produk lalu klik <b>+ Keranjang</b>.<br>2. Buka keranjang dan klik <b>Checkout</b>.<br>3. Isi alamat pengiriman, pilih kurir, dan masukkan kupon jika ada.<br>4. Klik <b>Buat Pesanan</b> lalu selesaikan pembayaran.</p>", "help", "orders"},
		{"Bagaimana cara melacak pesanan?", "Lacak status pesananmu secara real-time.",
			"<p>Buka <b>Pesanan Saya</b> lalu pilih pesanan. Di sana kamu bisa melihat:<br>- Status terbaru (dibayar, dikemas, dikirim, terkirim)<br>- Nomor resi dan kurir<br>- Riwayat lengkap peristiwa pesanan<br>Status berubah secara real-time lewat notifikasi.</p>", "help", "orders"},
		{"Bagaimana cara kerja pembayaran escrow?", "Dana pembeli dilindungi sampai pesanan selesai.",
			"<p>VinCommerce memegang pembayaran di escrow:<br>1. Pembeli membayar → dana ditahan platform.<br>2. Penjual mengemas dan mengirim.<br>3. Pembeli menerima dan mengonfirmasi.<br>4. Dana dilepas ke penjual.<br>Jika terjadi masalah, pembeli dapat mengajukan retur sebelum dana dilepas.</p>", "docs", "payments"},
		{"Metode pembayaran apa saja yang tersedia?", "Transfer bank dan simulasi gateway.",
			"<p>Platform mendukung transfer bank melalui gateway pembayaran (sandbox di lingkungan pengembangan). Setiap pembayaran menghasilkan <b>payment intent</b> dengan status escrow yang dapat dilacak.</p>", "help", "payments"},
		{"Bagaimana cara mengajukan retur?", "Retur dalam 7 hari setelah barang diterima.",
			"<p>1. Buka pesanan berstatus <b>Terkirim</b>.<br>2. Klik <b>Tulis ulasan</b>... maaf, klik menu retur.<br>3. Pilih alasan dan lampirkan foto bukti.<br>4. Penjual menyetujui/menolak; jika disetujui, admin memproses refund ke dompetmu.</p>", "help", "returns"},
		{"Kebijakan refund VinCommerce", "Ketentuan pengembalian dana.",
			"<p>Refund diproses setelah pengembalian disetujui. Dana dikembalikan ke <b>dompet</b> pembeli (saldo dapat digunakan untuk belanja atau ditarik). Refund penuh mencakup nilai item; ongkir tidak termasuk kecuali kesalahan penjual.</p>", "legal", "returns"},
		{"Panduan Penjual: buka toko", "Dari daftar akun hingga toko aktif.",
			"<p>1. Daftar sebagai pembeli.<br>2. Buka <b>Seller Center</b> → <b>Buka Toko</b>.<br>3. Isi nama toko.<br>4. Kirim KYC (identitas + rekening) — disetujui admin.<br>5. Tambah produk → aktifkan → mulai jualan!</p>", "docs", "account"},
		{"Panduan Penjual: kelola pesanan", "Kemas, kirim, dan lacak pesanan.",
			"<p>Di Seller Center → Pesanan:<br>- <b>Kemas</b>: pesanan berstatus dibayar.<br>- <b>Kirim</b>: isi kurir dan nomor resi.<br>Setelah pembeli mengonfirmasi diterima, dana escrow otomatis masuk ke dompetmu.</p>", "docs", "orders"},
		{"Panduan Penjual: dompet dan pencairan", "Kelola saldo penjualanmu.",
			"<p>Dana dari escrow masuk ke <b>Dompet</b> saat pesanan selesai. Kamu bisa:<br>- Melihat riwayat transaksi lengkap<br>- Menarik dana ke rekening (payout)<br>KYC harus disetujui sebelum pencairan pertama.</p>", "docs", "payments"},
		{"Syarat & Ketentuan", "Ketentuan penggunaan platform VinCommerce.",
			"<p><b>1. Layanan</b>: VinCommerce menghubungkan pembeli dan penjual; transaksi dilindungi escrow.<br><b>2. Akun</b>: pengguna bertanggung jawab atas keamanan kredensial.<br><b>3. Penjualan</b>: penjual wajib mengirim sesuai deskripsi produk.<br><b>4. Retur</b>: sesuai kebijakan refund yang berlaku.<br><b>5. Larangan</b>: dilarang menjual barang ilegal atau menipu pembeli.</p>", "legal", "account"},
		{"Kebijakan Privasi", "Bagaimana kami melindungi data pribadimu.",
			"<p>Kami mengumpulkan data yang diperlukan untuk transaksi (nama, email, alamat). Data tidak dijual ke pihak ketiga. Password dienkripsi dengan Argon2id. Kamu dapat meminta penghapusan akun kapan saja.</p>", "legal", "account"},
		{"Kebijakan Pengiriman", "Estimasi waktu dan biaya pengiriman.",
			"<p>Ongkir dihitung per penjual berdasarkan berat paket. Estimasi: Regular 3-7 hari, Express 1-2 hari. Nomor resi diberikan saat penjual mengirim.</p>", "legal", "shipping"},
		{"API Quickstart", "Mulai integrasi dengan VinCommerce API dalam 5 menit.",
			"<p><code>1. Daftar: POST /api/v1/auth/register</code><br><code>2. Login: POST /api/v1/auth/login</code><br><code>3. Katalog: GET /api/v1/products?q=phone</code><br><code>4. Keranjang: POST /api/v1/cart/items</code><br><code>5. Checkout: POST /api/v1/checkout/place</code><br>Dokumentasi lengkap: <b>/docs</b> (Swagger UI).</p>", "docs", "account"},
	}

	for _, a := range articles {
		cat := catIDs[a.cat]
		_, err := s.products.Pool().Exec(s.ctx, `
			INSERT INTO help_articles (id, category_id, title, slug, excerpt, content, section, is_published, published_at)
			VALUES (gen_random_uuid(), $1, $2, $3, $4, $5, $6, TRUE, now())
			ON CONFLICT (slug) DO UPDATE SET title = EXCLUDED.title, content = EXCLUDED.content,
				excerpt = EXCLUDED.excerpt, section = EXCLUDED.section`,
			cat, a.title, slugify(a.title), a.excerpt, a.content, a.section)
		if err != nil {
			fmt.Println("seed help article:", err)
			continue
		}
		created++
	}
	return created
}

type seeder struct {
	ctx        context.Context
	users      *repository.UserRepository
	categories *repository.CategoryRepository
	products   *repository.ProductRepository
	reviews    *repository.ReviewRepository
	catalog    *service.CatalogService
	password   *service.Password
	brandIDs   map[string]string
}

func (s *seeder) seedUsers() int {
	users := []struct {
		email, name, password, phone string
		roles                        []string
	}{
		{"admin@vincommerce.com", "Platform Admin", "AdminPass123!", "081200000001", []string{domain.RoleAdmin, domain.RoleBuyer}},
		{"support@vincommerce.com", "Support Agent", "SupportPass123!", "081200000007", []string{domain.RoleSupport, domain.RoleBuyer}},
		{"seller.elektro@vincommerce.com", "ElektroStore", "SellerPass123!", "081200000002", []string{domain.RoleSeller, domain.RoleBuyer}},
		{"seller.fashion@vincommerce.com", "FashionHub ID", "SellerPass123!", "081200000003", []string{domain.RoleSeller, domain.RoleBuyer}},
		{"seller.rumah@vincommerce.com", "Rumah Tangga Official", "SellerPass123!", "081200000004", []string{domain.RoleSeller, domain.RoleBuyer}},
		{"seller.gadget@vincommerce.com", "GadgetPro Store", "SellerPass123!", "081200000005", []string{domain.RoleSeller, domain.RoleBuyer}},
		{"buyer.sample@vincommerce.com", "Sample Buyer", "BuyerPass123!", "081200000006", []string{domain.RoleBuyer}},
	}
	for _, u := range users {
		hash, err := s.password.Hash(u.password)
		if err != nil {
			fmt.Println("seed user hash:", err)
			continue
		}
		now := time.Now().UTC()
		_, err = s.products.Pool().Exec(s.ctx, `
			INSERT INTO users (id, email, phone, full_name, password_hash, roles, email_verified_at, last_login_at)
			VALUES ($1, $2, NULLIF($3, ''), $4, $5, $6, $7, $8)
			ON CONFLICT (email) DO NOTHING`,
			uuid.NewString(), u.email, u.phone, u.name, hash, u.roles, now, now)
		if err != nil {
			fmt.Println("seed user:", err)
		}
	}
	return len(users)
}

func (s *seeder) seedCategories() int {
	tree := []struct {
		name     string
		children []string
	}{
		{"Electronics", []string{"Phones & Tablets", "Laptops & Computers", "Audio & Wearables", "Cameras"}},
		{"Fashion", []string{"Men's Apparel", "Women's Apparel", "Shoes", "Bags & Accessories"}},
		{"Home & Living", []string{"Furniture", "Kitchen & Dining", "Decor", "Appliances"}},
		{"Sports & Outdoor", []string{"Fitness", "Camping", "Cycling"}},
		{"Beauty & Health", []string{"Skincare", "Makeup", "Personal Care"}},
	}

	created := 0
	for _, parent := range tree {
		p, err := s.catalog.CreateCategory(s.ctx, nil, parent.name, created)
		if err != nil && !domain.Is(err, domain.KindConflict, "SLUG_TAKEN") {
			fmt.Println("seed category:", err)
			continue
		}
		if err == nil {
			created++
		} else {
			p, err = s.categories.BySlug(s.ctx, slugify(parent.name))
			if err != nil {
				fmt.Println("seed category lookup:", err)
				continue
			}
		}
		for i, child := range parent.children {
			_, err := s.catalog.CreateCategory(s.ctx, &p.ID, child, i)
			if err != nil && !domain.Is(err, domain.KindConflict, "SLUG_TAKEN") {
				fmt.Println("seed category child:", err)
			}
		}
	}
	return created
}

func (s *seeder) seedAttributes() int {
	attrs := []struct {
		name       string
		values     []string
		filterable bool
	}{
		{"Color", []string{"Black", "White", "Red", "Blue", "Green", "Yellow", "Pink", "Silver", "Gold"}, true},
		{"Size", []string{"S", "M", "L", "XL", "XXL"}, true},
		{"Storage", []string{"64GB", "128GB", "256GB", "512GB", "1TB"}, true},
		{"Connectivity", []string{"Bluetooth", "Wi-Fi", "5G", "LTE"}, true},
		{"Material", []string{"Cotton", "Polyester", "Leather", "Stainless Steel", "Wood", "Plastic"}, true},
	}
	created := 0
	for _, a := range attrs {
		if err := s.ensureAttribute(a.name, a.values, a.filterable); err != nil {
			fmt.Println("seed attribute:", err)
		} else {
			created++
		}
	}
	return created
}

func (s *seeder) ensureAttribute(name string, values []string, filterable bool) error {
	existing, err := s.catalog.Attributes(s.ctx)
	if err != nil {
		return err
	}
	for _, a := range existing {
		if a.Name == name {
			return nil
		}
	}
	_ = filterable
	// Direct insert is simpler here since the service has no attribute writer yet.
	_, err = s.products.Pool().Exec(s.ctx, `
		WITH attr AS (
			INSERT INTO attributes (id, name, slug, filterable) VALUES ($1, $2, $3, $4) RETURNING id
		)
		INSERT INTO attribute_values (id, attribute_id, value, slug, position)
		SELECT gen_random_uuid(), id, v.value, v.slug, v.pos
		FROM attr, unnest($5::text[], $6::text[], $7::int[]) AS v(value, slug, pos)`,
		uuid.NewString(), name, slugify(name), filterable, values, values, rangeOf(len(values)))
	return err
}

func rangeOf(n int) []int {
	out := make([]int, n)
	for i := range out {
		out[i] = i
	}
	return out
}

func (s *seeder) seedBrandsAndProducts() int {
	// (name, slug, description, categorySlug, brand, price, compareAt, color, storage/size, soldBias)
	type seedProduct struct {
		name, desc, catSlug, brand string
		price, compareAt           float64
		attrs                      map[string]string
		variants                   []string
		stock                      int
	}

	specs := []seedProduct{
		{"Smartphone Aurora X5 Pro", "Flagship phone with 6.7\" AMOLED, 5G, 200MP camera.", "phones-tablets", "Aurora",
			4999, 5999, map[string]string{"Storage": "256GB", "Connectivity": "5G"}, []string{"Black", "Silver", "Blue"}, 250},
		{"Smartphone Nova Lite", "Budget-friendly daily driver with 90Hz display.", "phones-tablets", "NovaMobile",
			1899, 2199, map[string]string{"Storage": "128GB", "Connectivity": "4G"}, []string{"Black", "White", "Green"}, 500},
		{"Tablet Pro 11", "11-inch tablet for work and play.", "phones-tablets", "Aurora",
			3599, 3999, map[string]string{"Storage": "256GB", "Connectivity": "Wi-Fi"}, []string{"Silver", "Black"}, 120},
		{"Ultrabook Air 14", "1.2kg ultrabook with 16h battery.", "laptops-computers", "TechMax",
			9999, 11999, map[string]string{"Storage": "512GB"}, []string{"Silver", "Gold"}, 80},
		{"Gaming Laptop GT-16", "RTX-grade gaming laptop with 165Hz panel.", "laptops-computers", "TechMax",
			15999, 17999, map[string]string{"Storage": "1TB"}, []string{"Black"}, 45},
		{"Wireless Headphones AirPods Max Style", "ANC over-ear headphones, 40h battery.", "audio-wearables", "SoundWave",
			2499, 2999, map[string]string{"Connectivity": "Bluetooth"}, []string{"Black", "White", "Pink"}, 400},
		{"True Wireless Earbuds Mini", "Compact earbuds with charging case.", "audio-wearables", "SoundWave",
			599, 799, map[string]string{"Connectivity": "Bluetooth"}, []string{"White", "Black"}, 800},
		{"Smart Watch Fit S2", "Fitness tracking, GPS, 7-day battery.", "audio-wearables", "WearTech",
			1299, 1599, map[string]string{"Connectivity": "Bluetooth"}, []string{"Black", "Silver", "Pink"}, 350},
		{"Mirrorless Camera X-T20", "24MP APS-C mirrorless with kit lens.", "cameras", "Fotograf",
			8999, 9999, map[string]string{"Storage": "128GB"}, []string{"Black", "Silver"}, 30},
		{"Action Camera 4K", "Waterproof 4K action cam with stabilization.", "cameras", "Fotograf",
			1899, 2299, map[string]string{"Storage": "128GB"}, []string{"Black"}, 150},
		{"Classic Oxford Shirt", "Premium cotton oxford, wrinkle-resistant.", "mens-apparel", "FashionHub",
			249, 349, map[string]string{"Material": "Cotton", "Color": "Blue", "Size": "M"}, []string{"M", "L", "XL"}, 600},
		{"Slim Fit Chino Pants", "Stretch chinos for everyday wear.", "mens-apparel", "FashionHub",
			299, 399, map[string]string{"Material": "Cotton", "Color": "Black", "Size": "M"}, []string{"S", "M", "L", "XL"}, 450},
		{"Floral Summer Dress", "Lightweight floral dress, breezy fit.", "womens-apparel", "FashionHub",
			349, 449, map[string]string{"Material": "Polyester", "Color": "Pink", "Size": "M"}, []string{"S", "M", "L"}, 520},
		{"Running Sneakers Cloud", "Cushioned road running shoes.", "shoes", "StepOne",
			799, 999, map[string]string{"Material": "Leather", "Color": "White", "Size": "M"}, []string{"40", "41", "42", "43"}, 380},
		{"Leather Weekender Bag", "40L weekender with shoe compartment.", "bags-accessories", "LeatherWorks",
			699, 899, map[string]string{"Material": "Leather", "Color": "Brown"}, []string{"Brown", "Black"}, 140},
		{"Sofa 3-Seater Comfort", "Linen fabric sofa, solid teak frame.", "furniture", "RumahTangga",
			3499, 4299, map[string]string{"Material": "Wood", "Color": "Grey"}, []string{"Grey", "Blue"}, 25},
		{"Wooden Dining Table 6-Seat", "Solid acacia dining table.", "furniture", "RumahTangga",
			2799, 3299, map[string]string{"Material": "Wood"}, []string{"Natural"}, 18},
		{"Non-Stick Frying Pan 28cm", "3-layer non-stick, induction ready.", "kitchen-dining", "RumahTangga",
			189, 249, map[string]string{"Material": "Stainless Steel"}, []string{"Black"}, 900},
		{"Ceramic Mug Set of 4", "Matte ceramic mugs, 350ml.", "kitchen-dining", "RumahTangga",
			129, 169, map[string]string{"Material": "Ceramic", "Color": "White"}, []string{"White", "Black", "Green"}, 750},
		{"Throw Pillow 45x45", "Soft velvet cover with insert.", "decor", "RumahTangga",
			89, 119, map[string]string{"Material": "Polyester", "Color": "Yellow"}, []string{"Yellow", "Grey", "Pink"}, 1100},
		{"Robot Vacuum S8", "Laser mapping, self-empty dock.", "appliances", "HomeTech",
			2999, 3999, map[string]string{"Connectivity": "Wi-Fi"}, []string{"White"}, 60},
		{"Air Fryer 5L Digital", "Digital touchscreen air fryer.", "appliances", "HomeTech",
			899, 1099, map[string]string{"Material": "Plastic"}, []string{"Black"}, 320},
		{"Yoga Mat Premium 6mm", "TPE non-slip yoga mat with strap.", "fitness", "FitLife",
			149, 199, map[string]string{"Material": "Plastic", "Color": "Blue"}, []string{"Blue", "Pink", "Green"}, 680},
		{"Adjustable Dumbbell 20kg", "Space-saving adjustable pair.", "fitness", "FitLife",
			1299, 1599, map[string]string{"Material": "Stainless Steel"}, []string{"Black"}, 90},
		{"Camping Tent 4-Person", "Waterproof dome tent, 5-min setup.", "camping", "OutdoorPro",
			899, 1199, map[string]string{"Material": "Polyester"}, []string{"Green", "Blue"}, 75},
		{"Hiking Backpack 50L", "Water-resistant trekking pack.", "camping", "OutdoorPro",
			459, 599, map[string]string{"Material": "Polyester", "Color": "Black"}, []string{"Black", "Red"}, 130},
		{"Mountain Bike X-26", "26-inch 21-speed MTB.", "cycling", "OutdoorPro",
			2499, 2999, map[string]string{"Material": "Steel"}, []string{"Black", "Blue"}, 40},
		{"Vitamin C Serum 30ml", "Brightening 10% vitamin C serum.", "skincare", "GlowLab",
			159, 199, map[string]string{"Size": "30ml"}, []string{"30ml"}, 1500},
		{"Sunscreen SPF50 PA++++", "Water-resistant daily sunscreen.", "skincare", "GlowLab",
			89, 119, map[string]string{"Size": "50ml"}, []string{"50ml"}, 2100},
		{"Matte Lipstick Set", "Set of 5 long-wear mattes.", "makeup", "GlowLab",
			199, 299, map[string]string{"Color": "Red"}, []string{"Red", "Pink"}, 890},
	}

	// brands
	s.brandIDs = map[string]string{}
	brands := map[string]string{}
	for _, p := range specs {
		brands[p.brand] = slugify(p.brand)
	}
	for name, slug := range brands {
		var id string
		err := s.products.Pool().QueryRow(s.ctx, `
			INSERT INTO brands (id, name, slug) VALUES (gen_random_uuid(), $1, $2)
			ON CONFLICT (slug) DO UPDATE SET slug = EXCLUDED.slug
			RETURNING id`, name, slug).Scan(&id)
		if err != nil {
			fmt.Println("seed brand:", err)
			continue
		}
		s.brandIDs[name] = id
	}

	// seller ids
	sellers, err := s.users.ByRole(s.ctx, domain.RoleSeller)
	if err != nil || len(sellers) == 0 {
		fmt.Println("seed: no sellers found")
		return 0
	}

	created := 0
	idx := 0
	for _, sp := range specs {
		seller := sellers[idx%len(sellers)]
		idx++

		cat, err := s.categories.BySlug(s.ctx, sp.catSlug)
		if err != nil {
			fmt.Println("seed category lookup:", sp.catSlug, err)
			continue
		}

		productID := uuid.NewString()
		slug := slugify(sp.name)
		attrsJSON := map[string]string{}
		for k, v := range sp.attrs {
			attrsJSON[slugify(k)] = v
		}
		attrsJSON["color"] = sp.variants[0]

		var inserted bool
		err = s.products.Pool().QueryRow(s.ctx, `
			INSERT INTO products (id, seller_id, category_id, brand_id, name, slug, description, status, attributes, published_at, sold_count)
			VALUES ($1, $2, $3, $4, $5, $6, $7, 'active', $8, now(), $9)
			ON CONFLICT (slug) DO UPDATE SET sold_count = GREATEST(products.sold_count, EXCLUDED.sold_count)
			RETURNING (xmax = 0)`,
			productID, seller.ID, cat.ID, s.brandIDs[sp.brand], sp.name, slug, sp.desc, attrsJSON, 10+idx*7).Scan(&inserted)
		if err != nil {
			fmt.Println("seed product:", err)
			continue
		}
		if !inserted {
			continue
		}

		for i, vname := range sp.variants {
			price := sp.price
			if i == 0 && sp.compareAt > 0 {
				// first variant carries compare-at discount
				_ = price
			}
			_ = sp.compareAt
			sku := strings.ToUpper(slug)[:min(12, len(strings.ToUpper(slug)))] + "-" + strings.ToUpper(vname)[:min(6, len(vname))]
			attrs := map[string]string{"Color": vname}
			for k, v := range sp.attrs {
				attrs[k] = v
			}
			if _, err := s.products.Pool().Exec(s.ctx, `
				INSERT INTO product_variants (id, product_id, sku, name, price, compare_at_price, stock, weight_grams, attributes)
				VALUES (gen_random_uuid(), $1, $2, $3, $4, $5, $6, $7, $8)`,
				productID, sku, vname, price, optionalPrice(sp.compareAt), sp.stock/(len(sp.variants)+1), 500+idx*30, attrs); err != nil {
				fmt.Println("seed variant:", err)
			}
		}

		// placeholder image per category
		if _, err := s.products.Pool().Exec(s.ctx, `
			INSERT INTO product_images (id, product_id, url, position, is_primary)
			VALUES (gen_random_uuid(), $1, $2, 0, TRUE)`,
			productID, fmt.Sprintf("https://placehold.co/600x600/%s/white?text=%s",
				randomColor(idx), urlEscape(sp.name))); err != nil {
			fmt.Println("seed image:", err)
		}

		// 2-3 approved reviews per product (skip if user already reviewed this product)
		for ri := 0; ri < 2+idx%2; ri++ {
			var exists bool
			if err := s.products.Pool().QueryRow(s.ctx,
				`SELECT EXISTS(SELECT 1 FROM product_reviews WHERE product_id = $1 AND user_id = $2)`,
				productID, sellers[ri%len(sellers)].ID).Scan(&exists); err != nil || exists {
				continue
			}
			rating := 3 + (idx+ri)%3
			if err := s.reviews.Create(s.ctx, &domain.ProductReview{
				ID:        uuid.NewString(),
				ProductID: productID,
				UserID:    sellers[ri%len(sellers)].ID,
				Rating:    rating,
				Title:     reviewTitles[idx%len(reviewTitles)],
				Content:   reviewContents[idx%len(reviewContents)],
				Status:    domain.ReviewApproved,
			}); err != nil {
				fmt.Println("seed review:", err)
			}
		}
		created++
	}
	return created
}

func optionalPrice(p float64) any {
	if p == 0 {
		return nil
	}
	return p
}

func min(a, b int) int {
	if a < b {
		return a
	}
	return b
}

func randomColor(i int) string {
	palette := []string{"1e3a8a", "0f766e", "b91c1c", "7c3aed", "be185d", "b45309", "15803d"}
	return palette[i%len(palette)]
}

func urlEscape(s string) string {
	return strings.ReplaceAll(s, " ", "+")
}

func slugify(s string) string {
	s = strings.ToLower(strings.TrimSpace(s))
	s = strings.ReplaceAll(s, "'", "")
	var b strings.Builder
	prevDash := false
	for _, r := range s {
		if (r >= 'a' && r <= 'z') || (r >= '0' && r <= '9') {
			b.WriteRune(r)
			prevDash = false
		} else {
			if !prevDash && b.Len() > 0 {
				b.WriteRune('-')
				prevDash = true
			}
		}
	}
	return strings.Trim(b.String(), "-")
}

var reviewTitles = []string{
	"Exceeded expectations", "Great quality", "Worth every penny",
	"Fast shipping, good product", "As described", "Highly recommended",
	"Solid buy", "Better than expected",
}

var reviewContents = []string{
	"Packaging was great and the product matches the photos exactly. Very satisfied with this purchase.",
	"Quality is solid for the price. Would buy again from this seller.",
	"Shipping was fast and the item arrived in perfect condition. The seller is responsive.",
	"Been using it for a week now, works perfectly. Highly recommend.",
	"Good value. There are minor flaws but acceptable at this price point.",
	"The product looks premium and functions as advertised. Five stars.",
}
