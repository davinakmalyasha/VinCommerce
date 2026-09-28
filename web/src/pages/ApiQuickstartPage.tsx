import { Link } from 'react-router-dom'

const BASE = `${import.meta.env.VITE_API_URL ?? ''}/api/v1`
const DOCS = `${import.meta.env.VITE_API_DOCS_URL ?? 'http://localhost:8080/docs'}`

/**
 * A real API quickstart page.
 *
 * This route previously rendered `HelpArticlePage`, which reads a `:slug`
 * param the route does not provide, so the "API Quickstart" link on the docs
 * page requested `/help/articles/undefined` and 404'd.
 */
export function ApiQuickstartPage() {
  return (
    <div className="mx-auto max-w-3xl px-4 py-10">
      <nav aria-label="Breadcrumb" className="mb-4 text-sm text-gray-500">
        <Link to="/docs" className="hover:underline">
          Dokumentasi
        </Link>
        <span className="mx-2" aria-hidden="true">
          /
        </span>
        <span className="text-gray-700 dark:text-gray-300">API Quickstart</span>
      </nav>

      <h1 className="text-2xl font-bold">API Quickstart</h1>
      <p className="mt-2 text-gray-600 dark:text-gray-400">
        Semua endpoint berada di bawah prefix <code className="rounded bg-gray-100 px-1 dark:bg-gray-800">/api/v1</code>.
        Referensi lengkap tersedia di Swagger UI.
      </p>

      <section className="mt-8">
        <h2 className="text-lg font-semibold">1. Login dan ambil token</h2>
        <p className="mt-1 text-sm text-gray-600 dark:text-gray-400">
          Token akses disimpan di memori; token refresh dikirim sebagai cookie{' '}
          <code className="rounded bg-gray-100 px-1 dark:bg-gray-800">httpOnly</code>.
        </p>
        <pre className="mt-3 overflow-x-auto rounded-lg bg-gray-900 p-4 text-xs text-gray-100">
{`curl -sS -X POST ${BASE}/auth/login \\
  -H 'Content-Type: application/json' \\
  -d '{"email":"buyer.sample@vincommerce.com","password":"BuyerPass123!"}'`}
        </pre>
        <p className="mt-2 text-sm text-gray-600 dark:text-gray-400">
          Balasannya berisi <code className="rounded bg-gray-100 px-1 dark:bg-gray-800">access_token</code>.
          Kirim sebagai <code className="rounded bg-gray-100 px-1 dark:bg-gray-800">Authorization: Bearer &lt;token&gt;</code>.
        </p>
      </section>

      <section className="mt-8">
        <h2 className="text-lg font-semibold">2. Jelajahi katalog publik</h2>
        <p className="mt-1 text-sm text-gray-600 dark:text-gray-400">
          Endpoint baca katalog tidak memerlukan autentikasi.
        </p>
        <pre className="mt-3 overflow-x-auto rounded-lg bg-gray-900 p-4 text-xs text-gray-100">
{`curl -sS '${BASE}/products?q=handphone&sort=relevance&page=1'

# filter_attribute[]=color:red&filter_attribute[]=size:L
# filter_min_price=500000&filter_max_price=2000000`}
        </pre>
      </section>

      <section className="mt-8">
        <h2 className="text-lg font-semibold">3. Tambah ke keranjang &amp; checkout</h2>
        <pre className="mt-3 overflow-x-auto rounded-lg bg-gray-900 p-4 text-xs text-gray-100">
{`curl -sS -X POST ${BASE}/cart/items \\
  -H "Authorization: Bearer $TOKEN" -H 'Content-Type: application/json' \\
  -d '{"variant_id":"<uuid>","quantity":1}'

# Cuplikan harga (dihitung server, selalu otoritatif):
curl -sS -X POST ${BASE}/checkout/quote \\
  -H "Authorization: Bearer $TOKEN" -H 'Content-Type: application/json' \\
  -d '{"coupon_code":"WELCOME10","shipping_method_code":"jne_reg"}'

# Buat pesanan — WAJIB Idempotency-Key agar retry tidak menggandakan pesanan:
curl -sS -X POST ${BASE}/checkout/place \\
  -H "Authorization: Bearer $TOKEN" \\
  -H 'X-Idempotency-Key: <uuid-per-attempt>' \\
  -H 'Content-Type: application/json' \\
  -d '{"shipping_address":{...}}'`}
        </pre>
      </section>

      <section className="mt-8">
        <h2 className="text-lg font-semibold">4. Rate limit</h2>
        <p className="mt-1 text-sm text-gray-600 dark:text-gray-400">
          Endpoint sensitif dibatasi per IP: <code className="rounded bg-gray-100 px-1 dark:bg-gray-800">/auth/login</code> dan{' '}
          <code className="rounded bg-gray-100 px-1 dark:bg-gray-800">/auth/register</code> 10/menit,{' '}
          <code className="rounded bg-gray-100 px-1 dark:bg-gray-800">/auth/refresh</code> 30/menit,{' '}
          <code className="rounded bg-gray-100 px-1 dark:bg-gray-800">/ai/ask</code> 20/menit. Balas{' '}
          <code className="rounded bg-gray-100 px-1 dark:bg-gray-800">429</code> dengan header{' '}
          <code className="rounded bg-gray-100 px-1 dark:bg-gray-800">Retry-After</code>.
        </p>
      </section>

      <section className="mt-8">
        <h2 className="text-lg font-semibold">Referensi lengkap</h2>
        <p className="mt-1 text-sm text-gray-600 dark:text-gray-400">
          Seluruh endpoint, skema request/response, dan definisi error ada di Swagger UI.
        </p>
        <a
          href={DOCS}
          target="_blank"
          rel="noopener noreferrer"
          className="mt-3 inline-flex items-center gap-2 rounded-lg bg-amber-500 px-4 py-2 text-sm font-semibold text-gray-900 hover:bg-amber-400 focus-visible:outline focus-visible:outline-2 focus-visible:outline-offset-2 focus-visible:outline-blue-600"
        >
          Buka Swagger UI
        </a>
      </section>
    </div>
  )
}

export default ApiQuickstartPage
