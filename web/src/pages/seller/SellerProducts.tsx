import { useState } from 'react'
import { useMutation, useQuery, useQueryClient } from '@tanstack/react-query'
import { api } from '../../lib/api'
import type { Product, Category } from '../../types'
import { formatIDR } from '../../lib/format'
import { FileUpload } from '../../components/FileUpload'

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

  const { data } = useQuery({
    queryKey: ['seller-products'],
    queryFn: async () => (await api.get<{ products: Product[]; total: number }>('/seller/products')).data,
  })

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
            {data?.products.map((p) => (
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
                    onClick={() => setStatus.mutate({ id: p.id, status: p.status === 'active' ? 'inactive' : 'active' })}
                    className="text-xs text-amber-600 hover:underline"
                  >
                    {p.status === 'active' ? 'Nonaktifkan' : 'Aktifkan'}
                  </button>
                </td>
              </tr>
            ))}
          </tbody>
        </table>
      </div>

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
            <input
              value={name}
              onChange={(e) => setName(e.target.value)}
              className="w-full px-4 py-3 border rounded-xl text-sm outline-none focus:border-amber-400"
            />
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
