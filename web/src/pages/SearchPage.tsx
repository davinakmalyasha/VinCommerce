import { useMemo } from 'react'
import { useQuery } from '@tanstack/react-query'
import { useSearchParams } from 'react-router-dom'
import { api } from '../lib/api'
import type { Product } from '../types'
import { ProductCard } from '../components/ProductCard'
import { Seo } from '../components/Seo'

const SORTS = [
  { value: 'relevance', label: 'Relevansi' },
  { value: 'price_asc', label: 'Harga Terendah' },
  { value: 'price_desc', label: 'Harga Tertinggi' },
  { value: 'newest', label: 'Terbaru' },
  { value: 'bestseller', label: 'Terlaris' },
  { value: 'rating', label: 'Rating' },
]

const PAGE_SIZE = 24

export function SearchPage() {
  const [params, setParams] = useSearchParams()
  const q = params.get('q') ?? ''
  const category = params.get('category') ?? ''
  const sort = params.get('sort') ?? 'relevance'
  const page = Math.max(1, Number(params.get('page') ?? 1))
  const priceMin = params.get('min_price') ?? ''
  const priceMax = params.get('max_price') ?? ''
  const brands = (params.get('brands') ?? '').split(',').filter(Boolean)
  const minRating = params.get('rating') ?? ''

  const selectedAttrs = useMemo(() => {
    const out: Record<string, string> = {}
    params.forEach((v, k) => {
      if (k.startsWith('attr_') && v) out[k.slice(5)] = v
    })
    return out
  }, [params])

  const setParam = (key: string, value: string) => {
    const next = new URLSearchParams(params)
    if (value) next.set(key, value)
    else next.delete(key)
    next.delete('page')
    setParams(next)
  }

  const queryString = useMemo(() => {
    const sp = new URLSearchParams()
    if (q) sp.set('q', q)
    if (category) sp.set('category', category)
    if (sort) sp.set('sort', sort)
    if (priceMin) sp.set('min_price', priceMin)
    if (priceMax) sp.set('max_price', priceMax)
    if (brands.length) sp.set('brands', brands.join(','))
    if (minRating) sp.set('rating', minRating)
    Object.entries(selectedAttrs).forEach(([k, v]) => sp.set(`attr_${k}`, v))
    sp.set('page', String(page))
    sp.set('page_size', String(PAGE_SIZE))
    return sp.toString()
  }, [q, category, sort, priceMin, priceMax, brands, minRating, selectedAttrs, page])

  const { data, isLoading } = useQuery({
    queryKey: ['search', queryString],
    queryFn: async () =>
      (await api.get<{ items: Product[]; total: number }>(`/products?${queryString}`)).data,
  })

  const { data: brandList } = useQuery({
    queryKey: ['brands'],
    queryFn: async () =>
      (await api.get<{ brands: { id: string; name: string }[] }>('/catalog/brands')).data.brands,
  })

  const { data: attrs } = useQuery({
    queryKey: ['attributes'],
    queryFn: async () =>
      (
        await api.get<{
          attributes: { id: string; name: string; slug: string; values: { id: string; value: string; slug: string }[] }[]
        }>('/catalog/attributes')
      ).data.attributes,
  })

  const setSort = (v: string) => {
    const next = new URLSearchParams(params)
    next.set('sort', v)
    setParams(next)
  }

  const goPage = (p: number) => {
    const next = new URLSearchParams(params)
    next.set('page', String(p))
    setParams(next)
    window.scrollTo({ top: 0 })
  }

  const totalPages = Math.max(1, Math.ceil((data?.total ?? 0) / PAGE_SIZE))

  const saveSearch = () => {
    if (!q) return
    try {
      const list = JSON.parse(localStorage.getItem('vc_saved_search') ?? '[]') as string[]
      if (!list.includes(q)) localStorage.setItem('vc_saved_search', JSON.stringify([q, ...list].slice(0, 8)))
    } catch {
      // ignore
    }
  }

  const hasActiveFilter = priceMin || priceMax || brands.length > 0 || minRating || Object.keys(selectedAttrs).length > 0

  return (
    <>
      <Seo
        title={q ? `Hasil untuk "${q}" — VinCommerce` : category ? `Kategori ${category} — VinCommerce` : 'Semua Produk — VinCommerce'}
        description={`Belanja ${q || category || 'semua produk'} online dengan aman di VinCommerce.`}
        path={window.location.pathname + window.location.search}
      />
      <div className="mx-auto max-w-7xl px-4 py-6 grid grid-cols-1 md:grid-cols-4 gap-6">
      <aside className="md:col-span-1 space-y-4">
        <div className="bg-white dark:bg-gray-900 border border-gray-200 dark:border-gray-700 rounded-xl p-4">
          <h3 className="font-semibold text-sm mb-2">Brand</h3>
          <div className="space-y-1.5 max-h-48 overflow-y-auto">
            {brandList?.map((b) => {
              const active = brands.includes(b.id)
              return (
                <label key={b.id} className="flex items-center gap-2 text-sm cursor-pointer">
                  <input
                    type="checkbox"
                    checked={active}
                    onChange={() => {
                      const next = active ? brands.filter((x) => x !== b.id) : [...brands, b.id]
                      setParam('brands', next.join(','))
                    }}
                  />
                  {b.name}
                </label>
              )
            })}
          </div>
        </div>

        <div className="bg-white dark:bg-gray-900 border border-gray-200 dark:border-gray-700 rounded-xl p-4">
          <h3 className="font-semibold text-sm mb-2">Rating</h3>
          <div className="flex flex-wrap gap-1.5">
            {[4, 3, 2].map((n) => (
              <button
                key={n}
                onClick={() => setParam('rating', minRating === String(n) ? '' : String(n))}
                className={`px-2.5 py-1 rounded-lg text-xs border ${
                  minRating === String(n)
                    ? 'bg-amber-500 text-white border-amber-500'
                    : 'border-gray-200 hover:border-amber-400'
                }`}
              >
                ★ {n}+ ke atas
              </button>
            ))}
          </div>
        </div>

        <div className="bg-white dark:bg-gray-900 border border-gray-200 dark:border-gray-700 rounded-xl p-4 space-y-4">
          <h3 className="font-semibold text-sm">Harga</h3>
          <div className="flex gap-2">
            <input
              type="number"
              placeholder="Min"
              value={priceMin}
              onChange={(e) => setParam('min_price', e.target.value)}
              className="w-full px-3 py-2 border rounded-lg text-sm outline-none focus:border-amber-400"
            />
            <input
              type="number"
              placeholder="Max"
              value={priceMax}
              onChange={(e) => setParam('max_price', e.target.value)}
              className="w-full px-3 py-2 border rounded-lg text-sm outline-none focus:border-amber-400"
            />
          </div>
          {(hasActiveFilter || category) && (
            <button
              onClick={() => {
                const next = new URLSearchParams()
                if (q) next.set('q', q)
                if (category) next.set('category', category)
                setParams(next)
              }}
              className="w-full text-xs text-red-500 hover:underline"
            >
              Reset filter
            </button>
          )}
        </div>

        {attrs?.map((attr) => (
          <div key={attr.id} className="bg-white border border-gray-200 rounded-xl p-4">
            <h3 className="font-semibold text-sm mb-2">{attr.name}</h3>
            <div className="flex flex-wrap gap-1.5">
              {attr.values.slice(0, 8).map((v) => {
                const active = selectedAttrs[attr.slug] === v.slug
                return (
                  <button
                    key={v.id}
                    onClick={() => setParam(`attr_${attr.slug}`, active ? '' : v.slug)}
                    className={`px-2.5 py-1 rounded-lg text-xs border ${
                      active ? 'bg-amber-500 text-white border-amber-500' : 'border-gray-200 hover:border-amber-400'
                    }`}
                  >
                    {v.value}
                  </button>
                )
              })}
            </div>
          </div>
        ))}
      </aside>

      <div className="md:col-span-3">
        <div className="flex items-center justify-between mb-4">
          <h1 className="font-bold text-lg">
            {q ? <>Hasil untuk "{q}"</> : category ? <>Kategori: {category}</> : 'Semua Produk'}
            <span className="text-gray-400 font-normal text-sm ml-2">({data?.total ?? 0} produk)</span>
          </h1>
          <div className="flex items-center gap-2">
            {q && (
              <button
                onClick={saveSearch}
                className="px-3 py-2 rounded-lg border text-xs hover:border-amber-400 hover:text-amber-600"
                title="Simpan pencarian"
              >
                ☆ Simpan
              </button>
            )}
            <select
              value={sort}
              onChange={(e) => setSort(e.target.value)}
              className="px-3 py-2 border rounded-lg text-sm outline-none"
            >
              {SORTS.map((s) => (
                <option key={s.value} value={s.value}>
                  {s.label}
                </option>
              ))}
            </select>
          </div>
        </div>

        {isLoading ? (
          <div className="grid grid-cols-2 lg:grid-cols-3 gap-4">
            {Array.from({ length: 6 }).map((_, i) => (
              <div key={i} className="bg-white dark:bg-gray-900 border border-gray-200 dark:border-gray-700 rounded-xl p-3 animate-pulse">
                <div className="aspect-square bg-gray-100 dark:bg-gray-800 rounded-lg" />
                <div className="h-4 bg-gray-100 dark:bg-gray-800 rounded mt-3 w-3/4" />
                <div className="h-3 bg-gray-100 dark:bg-gray-800 rounded mt-2 w-1/2" />
                <div className="h-5 bg-amber-100 dark:bg-amber-900/30 rounded mt-3 w-1/3" />
              </div>
            ))}
          </div>
        ) : (
          <>
            <div className="grid grid-cols-2 lg:grid-cols-3 gap-4">
              {data?.items.map((p) => <ProductCard key={p.id} product={p} />)}
            </div>
            {data?.items.length === 0 && (
              <p className="text-gray-500 text-sm py-10 text-center">Tidak ada produk yang cocok.</p>
            )}
            {totalPages > 1 && (
              <div className="flex items-center justify-center gap-2 mt-8">
                <button
                  onClick={() => goPage(page - 1)}
                  disabled={page <= 1}
                  className="px-3 py-2 rounded-lg border text-sm disabled:opacity-30 hover:bg-gray-50 dark:hover:bg-gray-800"
                >
                  ←
                </button>
                {Array.from({ length: totalPages }, (_, i) => i + 1)
                  .filter((p) => p === 1 || p === totalPages || Math.abs(p - page) <= 2)
                  .map((p) => (
                    <button
                      key={p}
                      onClick={() => goPage(p)}
                      className={`px-3.5 py-2 rounded-lg text-sm ${
                        p === page ? 'bg-amber-500 text-white' : 'border hover:bg-gray-50 dark:hover:bg-gray-800'
                      }`}
                    >
                      {p}
                    </button>
                  ))}
                <button
                  onClick={() => goPage(page + 1)}
                  disabled={page >= totalPages}
                  className="px-3 py-2 rounded-lg border text-sm disabled:opacity-30 hover:bg-gray-50 dark:hover:bg-gray-800"
                >
                  →
                </button>
              </div>
            )}
          </>
        )}
      </div>
    </div>
    </>
  )
}
