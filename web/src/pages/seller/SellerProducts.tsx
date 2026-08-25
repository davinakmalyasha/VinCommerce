import { useState } from 'react'
import { useMutation, useQuery, useQueryClient } from '@tanstack/react-query'
import { api } from '../../lib/api'
import type { Product, Category } from '../../types'
import { formatIDR } from '../../lib/format'
import { FileUpload } from '../../components/FileUpload'
import { SellerBundles } from './SellerBundles'

interface VariantRow {
  sku: string
  name: string
  price: string
  compare_at_price: string
  stock: string
  weight_grams: string
}

export function SellerProducts() {
  const queryClient = useQueryClient()
  const [editing, setEditing] = useState<Product | null>(null)
  const [showForm, setShowForm] = useState(false)
  const [stockFor, setStockFor] = useState<Product | null>(null)
  const [page, setPage] = useState(1)
  const [q, setQ] = useState('')
  const [search, setSearch] = useState('')

  const { data } = useQuery({
    queryKey: ['seller-products', page],
    queryFn: async () =>
      (await api.get<{ products: Product[]; total: number }>('/seller/products', { params: { page, page_size: 20 } })).data,
  })

  const visible = (data?.products ?? []).filter((p) =>
    search ? p.name.toLowerCase().includes(search.toLowerCase()) : true,
  )

  const { data: categories } = useQuery({
    queryKey: ['categories'],
    queryFn: async () => (await api.get<{ categories: Category[] }>('/catalog/categories')).data.categories,
  })

  const setStatus = useMutation({
    mutationFn: async ({ id, status }: { id: string; status: string }) =>
      api.post(`/seller/products/${id}/status`, { status }),
    onSuccess: () => queryClient.invalidateQueries({ queryKey: ['seller-products'] }),
  })

  return (
    <div className="space-y-4">
      <div className="flex items-center justify-between">
        <h1 className="text-xl font-bold">Produk Saya ({data?.total ?? 0})</h1>
        <div className="flex gap-2">
          <input
            value={q}
            onChange={(e) => setQ(e.target.value)}
            onKeyDown={(e) => e.key === 'Enter' && setSearch(q)}
            placeholder="Cari produk..."
            className="px-3 py-2 border rounded-lg text-sm outline-none w-44"
          />
          <label className="px-4 py-2 rounded-lg border border-gray-300 text-sm cursor-pointer hover:bg-gray-50 dark:hover:bg-gray-800">
            ⬆ Import CSV
            <input
              type="file"
              accept=".csv"
              className="hidden"
              onChange={async (e) => {
                const file = e.target.files?.[0]
                if (!file) return
                const form = new FormData()
                form.append('file', file)
                const res = await api.post<{ created: number; failed: number; errors: { row: number; error: string }[] }>(
                  '/seller/products/import',
                  form,
                  { headers: { 'Content-Type': 'multipart/form-data' } },
                )
                alert(
                  `Import selesai: ${res.data.created} dibuat, ${res.data.failed} gagal.` +
                    (res.data.errors.length ? `\nContoh error: ${res.data.errors[0].error}` : ''),
                )
                queryClient.invalidateQueries({ queryKey: ['seller-products'] })
                e.target.value = ''
              }}
            />
          </label>
          <button
            onClick={() => {
              setEditing(null)
              setShowForm(true)
            }}
            className="px-4 py-2 rounded-lg bg-amber-500 text-white text-sm font-medium hover:bg-amber-600"
          >
            + Tambah Produk
          </button>
        </div>
      </div>

      <p className="text-xs text-gray-400">
        Format CSV: name, category_slug, sku, price, stock, weight_grams (baris pertama = header)
      </p>

      <div className="bg-white border border-gray-200 rounded-xl overflow-hidden">
        <table className="w-full text-sm">
          <thead className="bg-gray-50 text-left text-xs text-gray-500">
            <tr>
              <th className="px-4 py-3">Produk</th>
              <th className="px-4 py-3">Harga</th>
              <th className="px-4 py-3">Stok</th>
              <th className="px-4 py-3">Terjual</th>
              <th className="px-4 py-3">Status</th>
              <th className="px-4 py-3">Aksi</th>
            </tr>
          </thead>
          <tbody className="divide-y divide-gray-100">
            {visible.map((p) => (
              <tr key={p.id}>
                <td className="px-4 py-3">
                  <p className="font-medium line-clamp-1 max-w-56">{p.name}</p>
                  <p className="text-xs text-gray-400">{p.variants?.length ?? 0} varian</p>
                </td>
                <td className="px-4 py-3">{formatIDR(p.variants?.[0]?.price ?? 0)}</td>
                <td className="px-4 py-3">{p.variants?.[0]?.stock ?? 0}</td>
                <td className="px-4 py-3">{p.sold_count}</td>
                <td className="px-4 py-3">
                  <span className={`px-2 py-0.5 rounded-full text-xs ${p.status === 'active' ? 'bg-green-100 text-green-700' : 'bg-gray-100 text-gray-600'}`}>
                    {p.status}
                  </span>
                </td>
                <td className="px-4 py-3 flex gap-2">
                  <button
                    onClick={() => {
                      setEditing(p)
                      setShowForm(true)
                    }}
                    className="text-xs text-blue-600 hover:underline"
                  >
                    Edit
                  </button>
                  <button
                    onClick={() => setStockFor(stockFor?.id === p.id ? null : p)}
                    className="text-xs text-indigo-600 hover:underline"
                  >
                    📦 Stok
                  </button>
                  <button
                    onClick={() => setStatus.mutate({ id: p.id, status: p.status === 'active' ? 'inactive' : 'active' })}
                    disabled={setStatus.isPending}
                    className="text-xs text-amber-600 hover:underline disabled:opacity-50"
                  >
                    {p.status === 'active' ? 'Nonaktifkan' : 'Aktifkan'}
                  </button>
                </td>
              </tr>
            ))}
          </tbody>
        </table>
      </div>

      {(data?.total ?? 0) > 20 && (
        <div className="flex items-center justify-center gap-3">
          <button onClick={() => setPage((p) => Math.max(1, p - 1))} disabled={page <= 1} className="px-3 py-1.5 rounded-lg border text-sm disabled:opacity-40">
            ← Sebelumnya
          </button>
          <span className="text-sm text-gray-500">Halaman {page}</span>
          <button
            onClick={() => setPage((p) => (data && page * 20 < data.total ? p + 1 : p))}
            disabled={!data || page * 20 >= data.total}
            className="px-3 py-1.5 rounded-lg border text-sm disabled:opacity-40"
          >
            Berikutnya →
          </button>
        </div>
      )}

      <SellerBundles />

      {stockFor && <StockAdjustPanel product={stockFor} onClose={() => setStockFor(null)} />}

      {showForm && (
        <ProductForm
          product={editing}
          categories={categories ?? []}
          onClose={() => setShowForm(false)}
          onSaved={() => {
            setShowForm(false)
            setEditing(null)
            queryClient.invalidateQueries({ queryKey: ['seller-products'] })
          }}
        />
      )}

      <ReviewsInbox />
    </div>
  )
}

interface SellerReview {
  id: string
  product_name: string
  user_name: string
  rating: number
  title?: string
  content: string
  images?: string[]
  reply?: string
  created_at: string
}

export function ReviewsInbox() {
  const queryClient = useQueryClient()
  const [replyFor, setReplyFor] = useState<string | null>(null)
  const [reply, setReply] = useState('')

  const { data } = useQuery({
    queryKey: ['seller-reviews'],
    queryFn: async () => (await api.get<{ reviews: SellerReview[] }>('/seller/reviews')).data.reviews,
  })

  const sendReply = useMutation({
    mutationFn: async ({ id, content }: { id: string; content: string }) =>
      api.post(`/seller/reviews/${id}/reply`, { content }),
    onSuccess: () => {
      setReplyFor(null)
      setReply('')
      queryClient.invalidateQueries({ queryKey: ['seller-reviews'] })
    },
    onError: (e: Error) => alert(e.message),
  })

  return (
    <div className="bg-white border border-gray-200 rounded-xl p-5 space-y-3">
      <h2 className="font-bold text-sm">Ulasan Pembeli ({data?.length ?? 0})</h2>
      {data?.length === 0 && <p className="text-sm text-gray-500">Belum ada ulasan untuk produkmu.</p>}
      <div className="space-y-3">
        {data?.map((r) => (
          <div key={r.id} className="border rounded-xl p-4 space-y-1.5">
            <div className="flex items-center justify-between">
              <p className="text-xs font-semibold text-amber-600">{'★'.repeat(r.rating)}{'☆'.repeat(5 - r.rating)}</p>
              <p className="text-xs text-gray-400">{r.product_name}</p>
            </div>
            {r.title && <p className="text-sm font-medium">{r.title}</p>}
            <p className="text-sm text-gray-600">{r.content}</p>
            <p className="text-xs text-gray-400">oleh {r.user_name}</p>
            {r.reply ? (
              <div className="bg-amber-50 dark:bg-amber-900/20 border border-amber-100 rounded-lg p-2.5 mt-1">
                <p className="text-xs font-semibold text-amber-700 mb-0.5">Balasan toko</p>
                <p className="text-sm">{r.reply}</p>
              </div>
            ) : (
              replyFor !== r.id && (
                <button onClick={() => setReplyFor(r.id)} className="text-xs text-blue-600 hover:underline">
                  ↩ Balas ulasan
                </button>
              )
            )}
            {replyFor === r.id && (
              <div className="space-y-2 pt-1">
                <textarea
                  value={reply}
                  onChange={(e) => setReply(e.target.value)}
                  rows={2}
                  placeholder="Balas ulasan ini secara profesional..."
                  className="w-full px-3 py-2 border rounded-lg text-sm outline-none"
                />
                <div className="flex gap-2">
                  <button
                    onClick={() => sendReply.mutate({ id: r.id, content: reply })}
                    disabled={sendReply.isPending || !reply.trim()}
                    className="px-3 py-1.5 rounded-lg bg-blue-600 text-white text-xs disabled:opacity-50"
                  >
                    Kirim Balasan
                  </button>
                  <button onClick={() => setReplyFor(null)} className="px-3 py-1.5 rounded-lg border text-xs">
                    Batal
                  </button>
                </div>
              </div>
            )}
          </div>
        ))}
      </div>
    </div>
  )
}

function StockAdjustPanel({ product, onClose }: { product: Product; onClose: () => void }) {
  const queryClient = useQueryClient()
  const [deltas, setDeltas] = useState<Record<string, number>>({})
  const [msg, setMsg] = useState('')

  const adjust = useMutation({
    mutationFn: async ({ variantId, delta }: { variantId: string; delta: number }) =>
      api.post(`/seller/stock/${variantId}/adjust`, { delta }),
    onSuccess: () => {
      setMsg('Stok diperbarui.')
      setDeltas({})
      queryClient.invalidateQueries({ queryKey: ['seller-products'] })
    },
    onError: (e: Error) => setMsg(e.message),
  })

  const apply = (variantId: string) => {
    const delta = deltas[variantId] ?? 0
    if (delta !== 0) adjust.mutate({ variantId, delta })
  }

  return (
    <div className="bg-white border border-indigo-200 rounded-xl p-5 space-y-3">
      <div className="flex items-center justify-between">
        <h2 className="font-bold text-sm">Atur Stok — {product.name}</h2>
        <button onClick={onClose} className="text-xs text-gray-400 hover:text-gray-600">
          Tutup ✕
        </button>
      </div>
      <p className="text-xs text-gray-400">Masukkan perubahan (mis. -3 untuk retur rusak, +10 untuk restock).</p>
      <div className="space-y-2">
        {(product.variants ?? []).map((v) => (
          <div key={v.id} className="flex items-center gap-3 text-sm">
            <span className="w-32 truncate">{v.name}</span>
            <span className="text-gray-400 w-20">stok: {v.stock}</span>
            <input
              type="number"
              value={deltas[v.id] ?? ''}
              onChange={(e) => setDeltas({ ...deltas, [v.id]: Number(e.target.value) })}
              placeholder="±delta"
              className="w-24 px-2 py-1.5 border rounded-lg outline-none"
            />
            <button
              onClick={() => apply(v.id)}
              disabled={adjust.isPending || !(deltas[v.id] ?? 0)}
              className="px-3 py-1.5 rounded-lg bg-indigo-600 text-white text-xs disabled:opacity-50"
            >
              Terapkan
            </button>
          </div>
        ))}
      </div>
      {msg && <p className="text-xs text-green-700">{msg}</p>}
    </div>
  )
}

function ProductForm({
  product,
  categories,
  onClose,
  onSaved,
}: {
  product: Product | null
  categories: Category[]
  onClose: () => void
  onSaved: () => void
}) {
  const [name, setName] = useState(product?.name ?? '')
  const [categoryId, setCategoryId] = useState(product?.category?.id ?? '')
  const [description, setDescription] = useState(product?.description ?? '')
  const [imageUrl, setImageUrl] = useState(product?.images?.[0]?.url ?? '')
  const [rows, setRows] = useState<VariantRow[]>(
    product?.variants?.map((v) => ({
      sku: v.sku,
      name: v.name,
      price: String(v.price),
      compare_at_price: v.compare_at_price ? String(v.compare_at_price) : '',
      stock: String(v.stock),
      weight_grams: String(v.weight_grams),
    })) ?? [{ sku: '', name: '', price: '', compare_at_price: '', stock: '', weight_grams: '' }],
  )
  const [error, setError] = useState('')
  const [generating, setGenerating] = useState(false)
  const [titles, setTitles] = useState<string[]>([])

  // ✨ AI title suggestions — click a chip to adopt it.
  const suggesting = useMutation({
    mutationFn: async () =>
      (await api.post<{ titles: string[] }>('/ai/title-suggest', { name })).data.titles,
    onSuccess: (t) => setTitles(t),
    onError: () => setTitles([]),
  })

  const suggestTitles = async () => {
    if (!name.trim()) return
    try {
      setTitles(await suggesting.mutateAsync())
    } catch {
      setTitles([])
    }
  }

  const generateDescription = useMutation({
    mutationFn: async () => {
      setGenerating(true)
      try {
        const res = await api.post<{ description: string }>('/ai/describe-product', {
          name,
          category: categoryId,
          attributes: {},
        })
        setDescription(res.data.description)
      } finally {
        setGenerating(false)
      }
    },
  })

  const save = useMutation({
    mutationFn: async () => {
      const variants = rows
        .filter((r) => r.name && r.price)
        .map((r) => ({
          sku: r.sku || r.name.toUpperCase().replace(/\s+/g, '-'),
          name: r.name,
          price: Number(r.price),
          compare_at_price: r.compare_at_price ? Number(r.compare_at_price) : undefined,
          stock: Number(r.stock || 0),
          weight_grams: Number(r.weight_grams || 0),
        }))
      const payload = {
        name,
        category_id: categoryId || undefined,
        description,
        variants,
        images: imageUrl ? [imageUrl] : [],
      }
      if (product) {
        await api.put(`/seller/products/${product.id}`, payload)
      } else {
        await api.post('/seller/products', payload)
      }
    },
    onSuccess: onSaved,
    onError: (e: Error) => setError(e.message),
  })

  const setRow = (i: number, field: keyof VariantRow, value: string) => {
    setRows((prev) => prev.map((r, idx) => (idx === i ? { ...r, [field]: value } : r)))
  }

  return (
    <div className="fixed inset-0 z-50 overflow-y-auto">
      <div className="absolute inset-0 bg-black/40" onClick={onClose} />
      <div className="relative mx-auto my-8 max-w-2xl bg-white rounded-2xl shadow-xl p-6">
        <div className="flex justify-between items-center mb-4">
          <h2 className="font-bold text-lg">{product ? `Edit: ${product.name}` : 'Tambah Produk'}</h2>
          <button onClick={onClose} className="text-gray-400 hover:text-gray-700">✕</button>
        </div>

        <div className="space-y-4">
          <div>
            <label className="text-sm font-medium block mb-1">Nama Produk</label>
            <div className="flex gap-2">
              <input
                value={name}
                onChange={(e) => setName(e.target.value)}
                className="w-full px-4 py-3 border rounded-xl text-sm outline-none focus:border-amber-400"
              />
              <button
                type="button"
                onClick={suggestTitles}
                disabled={suggesting.isPending || !name.trim()}
                title="Saran judul dengan AI"
                className="shrink-0 px-3 py-2 rounded-xl border border-amber-400 text-amber-600 text-xs font-medium hover:bg-amber-50 dark:hover:bg-amber-950/30 disabled:opacity-50"
              >
                {suggesting.isPending ? '...' : '✨ Saran'}
              </button>
            </div>
            {titles.length > 0 && (
              <div className="mt-2 flex flex-wrap gap-1.5">
                {titles.map((t) => (
                  <button
                    key={t}
                    type="button"
                    onClick={() => setName(t)}
                    className="px-2.5 py-1 rounded-full border border-gray-300 text-xs hover:border-amber-400 hover:text-amber-600"
                  >
                    {t}
                  </button>
                ))}
              </div>
            )}
          </div>

          <div className="grid grid-cols-2 gap-3">
            <div>
              <label className="text-sm font-medium block mb-1">Kategori</label>
              <select
                value={categoryId}
                onChange={(e) => setCategoryId(e.target.value)}
                className="w-full px-4 py-3 border rounded-xl text-sm outline-none focus:border-amber-400"
              >
                <option value="">Pilih kategori</option>
                {categories.map((c) => (
                  <optgroup key={c.id} label={c.name}>
                    {c.children?.map((child) => (
                      <option key={child.id} value={child.id}>
                        {c.name} / {child.name}
                      </option>
                    ))}
                  </optgroup>
                ))}
              </select>
            </div>
            <div>
              <label className="text-sm font-medium block mb-1">URL Gambar</label>
              <FileUpload value={imageUrl} onChange={setImageUrl} />
            </div>
          </div>

          <div>
            <div className="flex items-center justify-between mb-1">
              <label className="text-sm font-medium">Deskripsi</label>
              <button
                onClick={() => generateDescription.mutate()}
                disabled={generating || !name}
                className="text-xs text-amber-600 hover:underline disabled:opacity-40"
              >
                ✨ {generating ? 'Menulis...' : 'Buat dengan AI'}
              </button>
            </div>
            <textarea
              value={description}
              onChange={(e) => setDescription(e.target.value)}
              rows={3}
              className="w-full px-4 py-3 border rounded-xl text-sm outline-none focus:border-amber-400 dark:bg-gray-800"
            />
          </div>

          <div>
            <div className="flex items-center justify-between mb-2">
              <label className="text-sm font-medium">Varian (SKU)</label>
              <button
                onClick={() =>
                  setRows((prev) => [...prev, { sku: '', name: '', price: '', compare_at_price: '', stock: '', weight_grams: '' }])
                }
                className="text-xs text-amber-600 hover:underline"
              >
                + Tambah varian
              </button>
            </div>
            <div className="space-y-2">
              {rows.map((r, i) => (
                <div key={i} className="grid grid-cols-6 gap-2 items-center">
                  <input
                    placeholder="Nama"
                    value={r.name}
                    onChange={(e) => setRow(i, 'name', e.target.value)}
                    className="col-span-2 px-3 py-2 border rounded-lg text-sm outline-none"
                  />
                  <input
                    placeholder="SKU"
                    value={r.sku}
                    onChange={(e) => setRow(i, 'sku', e.target.value)}
                    className="px-3 py-2 border rounded-lg text-sm outline-none"
                  />
                  <input
                    placeholder="Harga"
                    type="number"
                    value={r.price}
                    onChange={(e) => setRow(i, 'price', e.target.value)}
                    className="px-3 py-2 border rounded-lg text-sm outline-none"
                  />
                  <input
                    placeholder="Stok"
                    type="number"
                    value={r.stock}
                    onChange={(e) => setRow(i, 'stock', e.target.value)}
                    className="px-3 py-2 border rounded-lg text-sm outline-none"
                  />
                  <button
                    onClick={() => setRows((prev) => prev.filter((_, idx) => idx !== i))}
                    disabled={rows.length === 1}
                    className="text-red-500 text-sm disabled:opacity-30"
                  >
                    ✕
                  </button>
                </div>
              ))}
            </div>
          </div>

          {error && <p className="text-sm text-red-600">{error}</p>}

          <div className="flex gap-2 pt-2">
            <button
              onClick={() => save.mutate()}
              disabled={save.isPending}
              className="flex-1 py-3 rounded-xl bg-amber-500 text-white font-semibold hover:bg-amber-600 disabled:opacity-50"
            >
              {save.isPending ? 'Menyimpan...' : product ? 'Simpan Perubahan' : 'Buat Produk (draft)'}
            </button>
            <button onClick={onClose} className="px-6 py-3 rounded-xl border text-sm">
              Batal
            </button>
          </div>
        </div>
      </div>
    </div>
  )
}
