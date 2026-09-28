import { useState } from 'react'
import { useMutation, useQuery, useQueryClient } from '@tanstack/react-query'
import { api } from '../../lib/api'
import type { Order, Product, Variant } from '../../types'
import { formatIDR, orderStatusColors, orderStatusLabels } from '../../lib/format'


export function AdminOrders() {
  const [q, setQ] = useState('')
  const [status, setStatus] = useState('')
  const [search, setSearch] = useState('')

  const { data } = useQuery({
    queryKey: ['admin-orders', search, status],
    queryFn: async () =>
      (await api.get<{ orders: Order[]; total: number }>(`/admin/orders?q=${encodeURIComponent(search)}&status=${status}`))
        .data,
  })

  return (
    <div className="space-y-4">
      <div className="flex items-center justify-between">
        <h1 className="text-xl font-bold">Pesanan Platform ({data?.total ?? 0})</h1>
        <div className="flex gap-2">
          <label htmlFor="admin-order-search" className="sr-only">Cari pesanan</label>
          <input
            id="admin-order-search"
            value={q}
            onChange={(e) => setQ(e.target.value)}
            onKeyDown={(e) => e.key === 'Enter' && setSearch(q)}
            placeholder="Cari no. pesanan / email / penjual"
            className="px-3 py-2 border rounded-lg text-sm outline-none w-64 max-w-full dark:bg-gray-800"
          />
          <label htmlFor="admin-order-status" className="sr-only">Status</label>
          <select
            id="admin-order-status"
            value={status}
            onChange={(e) => setStatus(e.target.value)}
            className="px-3 py-2 border rounded-lg text-sm outline-none dark:bg-gray-800"
          >
            <option value="">Semua status</option>
            <option value="pending">Pending</option>
            <option value="paid">Paid</option>
            <option value="shipped">Shipped</option>
            <option value="delivered">Delivered</option>
            <option value="completed">Completed</option>
            <option value="cancelled">Cancelled</option>
          </select>
        </div>

      </div>
      <div className="bg-white dark:bg-gray-900 border border-gray-200 dark:border-gray-700 rounded-xl overflow-x-auto">
        <table className="w-full min-w-[48rem] text-sm">
          <thead className="bg-gray-50 dark:bg-gray-800 text-left text-xs text-gray-500">
            <tr>
              <th className="px-4 py-3">Pesanan</th>

              <th className="px-4 py-3">Pembeli</th>
              <th className="px-4 py-3">Penjual</th>
              <th className="px-4 py-3">Total</th>
              <th className="px-4 py-3">Status</th>
              <th className="px-4 py-3">Pembayaran</th>
            </tr>
          </thead>
          <tbody className="divide-y divide-gray-100 dark:divide-gray-700">
            {data?.orders.map((o) => (
              <tr key={o.id}>
                <td className="px-4 py-3 font-mono text-xs">{o.order_number}</td>
                <td className="px-4 py-3">{o.buyer_name || '—'}</td>
                <td className="px-4 py-3">{o.seller_name || '—'}</td>
                <td className="px-4 py-3 font-medium">{formatIDR(o.total_amount)}</td>
                <td className="px-4 py-3">
                  <span className={`px-2 py-0.5 rounded-full text-xs ${orderStatusColors[o.status]}`}>
                    {orderStatusLabels[o.status]}
                  </span>
                </td>
                <td className="px-4 py-3 text-xs">{o.payment_status}</td>
              </tr>
            ))}
          </tbody>
        </table>
      </div>
    </div>
  )
}

export function AdminAudit() {
  const { data } = useQuery({
    queryKey: ['admin-audit'],
    queryFn: async () => (await api.get<{ entries: { id: number; actor_name: string; action: string; entity_type: string; entity_id: string; ip_address: string; created_at: string }[] }>('/admin/audit?limit=100')).data.entries,
  })

  return (
    <div className="space-y-4">
      <h1 className="text-xl font-bold">Audit Log</h1>
      <div className="bg-white dark:bg-gray-900 border border-gray-200 dark:border-gray-700 rounded-xl overflow-x-auto">
        <table className="w-full min-w-[44rem] text-sm">
          <thead className="bg-gray-50 dark:bg-gray-800 text-left text-xs text-gray-500">
            <tr>
              <th className="px-4 py-3">Waktu</th>

              <th className="px-4 py-3">Aktor</th>
              <th className="px-4 py-3">Aksi</th>
              <th className="px-4 py-3">Entitas</th>
              <th className="px-4 py-3">IP</th>
            </tr>
          </thead>
          <tbody className="divide-y divide-gray-100 dark:divide-gray-700">
            {data?.map((e) => (
              <tr key={e.id}>
                <td className="px-4 py-3 text-xs text-gray-400">{e.created_at.slice(0, 19).replace('T', ' ')}</td>
                <td className="px-4 py-3">{e.actor_name || '—'}</td>
                <td className="px-4 py-3 font-mono text-xs">{e.action}</td>
                <td className="px-4 py-3 text-xs">{e.entity_type} {e.entity_id && `#${e.entity_id.slice(0, 8)}`}</td>
                <td className="px-4 py-3 text-xs text-gray-400">{e.ip_address}</td>
              </tr>
            ))}
          </tbody>
        </table>
      </div>
    </div>
  )
}

export function AdminCommission() {
  const queryClient = useQueryClient()
  const [pct, setPct] = useState('')
  const [fixed, setFixed] = useState('')
  const [toast, setToast] = useState<{ tone: 'ok' | 'err'; text: string } | null>(null)

  const { data } = useQuery({
    queryKey: ['admin-commission'],
    queryFn: async () => (await api.get<{ fee: { pct: number; fixed: number } }>('/admin/commission')).data.fee,
  })

  // Money-affecting setting: a mutation gives the busy guard, the error
  // surface and the invalidation, instead of three hand-rolled async lines
  // whose rejection escaped as an unhandled promise.
  const save = useMutation({
    mutationFn: async () => {
      const nextPct = pct !== '' ? Number(pct) : (data?.pct ?? 0)
      const nextFixed = fixed !== '' ? Number(fixed) : (data?.fixed ?? 0)
      if (!Number.isFinite(nextPct) || nextPct < 0 || nextPct > 100) {
        throw new Error('Persen komisi harus antara 0 dan 100')
      }
      if (!Number.isFinite(nextFixed) || nextFixed < 0) {
        throw new Error('Biaya tetap tidak boleh negatif')
      }
      await api.put('/admin/commission', { pct: nextPct, fixed: nextFixed })
    },
    onSuccess: () => {
      setToast({ tone: 'ok', text: 'Komisi diperbarui' })
      queryClient.invalidateQueries({ queryKey: ['admin-commission'] })
    },
    onError: (e: Error) => setToast({ tone: 'err', text: e.message || 'Gagal menyimpan komisi' }),
  })

  return (
    <div className="space-y-4 max-w-lg">
      <h1 className="text-xl font-bold">Komisi Platform</h1>
      <div className="bg-white dark:bg-gray-900 border border-gray-200 dark:border-gray-700 rounded-xl p-6 space-y-3">
        <p className="text-sm text-gray-500">
          Komisi dipotong dari escrow saat pesanan selesai: penjual menerima (total − komisi), sisanya masuk dompet platform.
        </p>
        <div className="grid grid-cols-2 gap-3">
          <div>
            <label htmlFor="commission-pct" className="text-sm font-medium block mb-1">Persen (%)</label>
            <input
              id="commission-pct"
              type="number"
              min={0}
              max={100}
              value={pct || data?.pct}
              onChange={(e) => setPct(e.target.value)}
              className="w-full px-4 py-3 border rounded-xl text-sm outline-none dark:bg-gray-800"
            />
          </div>
          <div>
            <label htmlFor="commission-fixed" className="text-sm font-medium block mb-1">Biaya tetap (Rp)</label>
            <input
              id="commission-fixed"
              type="number"
              min={0}
              value={fixed || data?.fixed}
              onChange={(e) => setFixed(e.target.value)}
              className="w-full px-4 py-3 border rounded-xl text-sm outline-none dark:bg-gray-800"
            />
          </div>
        </div>
        <button
          type="button"
          onClick={() => save.mutate()}
          disabled={save.isPending}
          className="px-6 py-3 rounded-xl bg-gray-900 dark:bg-gray-100 dark:text-gray-900 text-white text-sm font-medium disabled:opacity-50"
        >
          {save.isPending ? 'Menyimpan...' : 'Simpan'}
        </button>
        {toast && (
          <p
            role="status"
            className={`rounded-lg p-2.5 text-sm ${
              toast.tone === 'ok' ? 'bg-green-50 dark:bg-green-900/30 text-green-700' : 'bg-red-50 dark:bg-red-950/40 text-red-700'
            }`}
          >
            {toast.text}
            <button type="button" onClick={() => setToast(null)} className="ml-2 underline">Tutup</button>
          </p>
        )}
        <p className="text-xs text-gray-400">
          Saat ini: {data?.pct}% + Rp {data?.fixed} per transaksi
        </p>
      </div>
    </div>
  )
}


export function AdminCatalog() {
  const queryClient = useQueryClient()
  const [brandName, setBrandName] = useState('')
  const [attrName, setAttrName] = useState('')
  const [attrValues, setAttrValues] = useState('')
  const [toast, setToast] = useState<{ tone: 'ok' | 'err'; text: string } | null>(null)

  const { data: brands } = useQuery({
    queryKey: ['brands'],
    queryFn: async () => (await api.get<{ brands: { id: string; name: string; is_active: boolean }[] }>('/catalog/brands')).data.brands,
  })

  const { refetch: refetchAttributes } = useQuery({
    queryKey: ['catalog-attributes'],
    queryFn: async () =>
      (await api.get<{ attributes: { id: string }[] }>('/catalog/attributes')).data.attributes,
  })

  // These three used to be bare `async` click handlers: a 4xx became an
  // unhandled promise rejection (silent to the user) and the form state was
  // cleared before the request had succeeded.
  const addBrand = useMutation({
    mutationFn: async (name: string) => api.post('/catalog/brands', { name }),
    onSuccess: () => {
      setBrandName('')
      setToast({ tone: 'ok', text: 'Brand ditambahkan' })
      queryClient.invalidateQueries({ queryKey: ['brands'] })
    },
    onError: (e: Error) => setToast({ tone: 'err', text: e.message || 'Gagal menambahkan brand' }),
  })

  const toggleBrand = useMutation({
    mutationFn: async ({ id, active }: { id: string; active: boolean }) =>
      api.post(`/catalog/brands/${id}/toggle`, { active }),
    onSuccess: () => queryClient.invalidateQueries({ queryKey: ['brands'] }),
    onError: (e: Error) => setToast({ tone: 'err', text: e.message || 'Gagal mengubah status brand' }),
  })

  const addAttribute = useMutation({
    mutationFn: async () =>
      api.post('/catalog/attributes', {
        name: attrName,
        filterable: true,
        values: attrValues
          .split(',')
          .map((v) => ({ value: v.trim() }))
          .filter((v) => v.value),
      }),
    onSuccess: () => {
      setAttrName('')
      setAttrValues('')
      setToast({ tone: 'ok', text: 'Atribut ditambahkan' })
      void refetchAttributes()
      queryClient.invalidateQueries({ queryKey: ['attributes'] })
    },
    onError: (e: Error) => setToast({ tone: 'err', text: e.message || 'Gagal menambahkan atribut' }),
  })

  return (
    <div className="space-y-6">
      <h1 className="text-xl font-bold">Kelola Katalog</h1>
      {toast && (
        <p
          role="status"
          className={`rounded-lg p-2.5 text-sm ${
            toast.tone === 'ok' ? 'bg-green-50 dark:bg-green-900/30 text-green-700' : 'bg-red-50 dark:bg-red-950/40 text-red-700'
          }`}
        >
          {toast.text}
          <button type="button" onClick={() => setToast(null)} className="ml-2 underline">Tutup</button>
        </p>
      )}
      <div className="bg-white dark:bg-gray-900 border border-gray-200 dark:border-gray-700 rounded-xl p-5 space-y-3">
        <h2 className="font-bold text-sm">Brand</h2>
        <div className="flex gap-2">
          <label htmlFor="brand-name" className="sr-only">Nama brand</label>
          <input
            id="brand-name"
            value={brandName}
            onChange={(e) => setBrandName(e.target.value)}
            placeholder="Nama brand"
            className="flex-1 min-w-0 px-3 py-2 border rounded-lg text-sm outline-none dark:bg-gray-800"
          />
          <button
            type="button"
            onClick={() => addBrand.mutate(brandName.trim())}
            disabled={!brandName.trim() || addBrand.isPending}
            className="px-4 py-2 rounded-lg bg-amber-500 text-white text-sm disabled:opacity-50 shrink-0"
          >
            Tambah
          </button>
        </div>
        <div className="flex flex-wrap gap-2">
          {brands?.map((b) => (
            <span key={b.id} className="px-3 py-1.5 rounded-full border text-sm flex items-center gap-2">
              {b.name}
              <button
                type="button"
                onClick={() => toggleBrand.mutate({ id: b.id, active: !b.is_active })}
                disabled={toggleBrand.isPending}
                className="text-xs text-amber-600 disabled:opacity-50"
              >
                {b.is_active ? 'nonaktif' : 'aktif'}
              </button>
            </span>
          ))}
        </div>
      </div>
      <div className="bg-white dark:bg-gray-900 border border-gray-200 dark:border-gray-700 rounded-xl p-5 space-y-3">
        <h2 className="font-bold text-sm">Atribut Filter</h2>
        <div className="flex flex-wrap gap-2">
          <label htmlFor="attr-name" className="sr-only">Nama atribut</label>
          <input
            id="attr-name"
            value={attrName}
            onChange={(e) => setAttrName(e.target.value)}
            placeholder="Nama atribut (mis. Warna)"
            className="flex-1 min-w-40 px-3 py-2 border rounded-lg text-sm outline-none dark:bg-gray-800"
          />
          <label htmlFor="attr-values" className="sr-only">Nilai atribut</label>
          <input
            id="attr-values"
            value={attrValues}
            onChange={(e) => setAttrValues(e.target.value)}
            placeholder="Nilai, dipisah koma"
            className="flex-1 min-w-40 px-3 py-2 border rounded-lg text-sm outline-none dark:bg-gray-800"
          />
          <button
            type="button"
            onClick={() => addAttribute.mutate()}
            disabled={!attrName.trim() || addAttribute.isPending}
            className="px-4 py-2 rounded-lg bg-amber-500 text-white text-sm disabled:opacity-50 shrink-0"
          >
            Tambah
          </button>
        </div>
      </div>
    </div>
  )
}


export function AdminFlashSales() {
  const queryClient = useQueryClient()
  const [name, setName] = useState('')
  const [startsAt, setStartsAt] = useState('')
  const [endsAt, setEndsAt] = useState('')
  const [openSale, setOpenSale] = useState<string | null>(null)
  const [toast, setToast] = useState<{ tone: 'ok' | 'err'; text: string } | null>(null)

  const { data } = useQuery({
    queryKey: ['admin-flash-sales'],
    queryFn: async () => (await api.get<{ sales: { id: string; name: string; starts_at: string; ends_at: string; is_active: boolean }[] }>('/admin/flash-sales')).data.sales,
  })

  // `toggle` had NO try/catch at all, so a failed toggle produced an
  // unhandled rejection and the UI silently did nothing.
  const create = useMutation({
    mutationFn: async () => {
      if (!name.trim()) throw new Error('Nama sale wajib diisi')
      if (!startsAt || !endsAt) throw new Error('Tanggal mulai dan selesai wajib diisi')
      const start = new Date(startsAt).getTime()
      const end = new Date(endsAt).getTime()
      if (Number.isNaN(start) || Number.isNaN(end)) throw new Error('Tanggal tidak valid')
      if (end <= start) throw new Error('Tanggal selesai harus setelah tanggal mulai')
      await api.post('/admin/flash-sales', {
        name: name.trim(),
        starts_at: new Date(start).toISOString(),
        ends_at: new Date(end).toISOString(),
      })
    },
    onSuccess: () => {
      setName('')
      setStartsAt('')
      setEndsAt('')
      setToast({ tone: 'ok', text: 'Flash sale dibuat' })
      queryClient.invalidateQueries({ queryKey: ['admin-flash-sales'] })
    },
    onError: (e: Error) => setToast({ tone: 'err', text: e.message }),
  })

  const toggle = useMutation({
    mutationFn: async ({ id, active }: { id: string; active: boolean }) =>
      api.post(`/admin/flash-sales/${id}/toggle`, { active }),
    onSuccess: () => queryClient.invalidateQueries({ queryKey: ['admin-flash-sales'] }),
    onError: (e: Error) => setToast({ tone: 'err', text: e.message || 'Gagal mengubah status sale' }),
  })

  return (
    <div className="space-y-4">
      <h1 className="text-xl font-bold">Flash Sale</h1>
      {toast && (
        <p
          role="status"
          className={`rounded-lg p-2.5 text-sm ${
            toast.tone === 'ok' ? 'bg-green-50 dark:bg-green-900/30 text-green-700' : 'bg-red-50 dark:bg-red-950/40 text-red-700'
          }`}
        >
          {toast.text}
          <button type="button" onClick={() => setToast(null)} className="ml-2 underline">Tutup</button>
        </p>
      )}
      <div className="bg-white dark:bg-gray-900 border border-gray-200 dark:border-gray-700 rounded-xl p-5 grid grid-cols-1 sm:grid-cols-4 gap-3">
        <div className="sm:col-span-4">
          <label htmlFor="fs-name" className="sr-only">Nama sale</label>
          <input
            id="fs-name"
            value={name}
            onChange={(e) => setName(e.target.value)}
            placeholder="Nama (mis. Akhir Pekan Sale)"
            className="w-full px-3 py-2 border rounded-lg text-sm outline-none dark:bg-gray-800"
          />
        </div>
        <div>
          <label htmlFor="fs-start" className="sr-only">Mulai</label>
          <input
            id="fs-start"
            type="datetime-local"
            value={startsAt}
            onChange={(e) => setStartsAt(e.target.value)}
            className="w-full px-3 py-2 border rounded-lg text-sm outline-none dark:bg-gray-800"
          />
        </div>
        <div>
          <label htmlFor="fs-end" className="sr-only">Selesai</label>
          <input
            id="fs-end"
            type="datetime-local"
            value={endsAt}
            onChange={(e) => setEndsAt(e.target.value)}
            className="w-full px-3 py-2 border rounded-lg text-sm outline-none dark:bg-gray-800"
          />
        </div>
        <button
          type="button"
          onClick={() => create.mutate()}
          disabled={create.isPending || !name.trim() || !startsAt || !endsAt}
          className="sm:col-span-2 px-4 py-2 rounded-lg bg-amber-500 text-white text-sm disabled:opacity-50"
        >
          {create.isPending ? 'Membuat...' : 'Buat Sale'}
        </button>
      </div>
      <div className="space-y-2">
        {data?.map((s) => (
          <div key={s.id} className="bg-white dark:bg-gray-900 border border-gray-200 dark:border-gray-700 rounded-xl p-4">
            <div className="flex items-center justify-between">
              <div>
                <p className="font-medium text-sm">{s.name}</p>
                <p className="text-xs text-gray-500">
                  {s.starts_at.slice(0, 16).replace('T', ' ')} → {s.ends_at.slice(0, 16).replace('T', ' ')}
                </p>
              </div>
              <div className="flex gap-3">
                <button
                  type="button"
                  onClick={() => setOpenSale(openSale === s.id ? null : s.id)}
                  aria-expanded={openSale === s.id}
                  className="text-xs text-blue-600 hover:underline"
                >
                  {openSale === s.id ? 'Tutup' : 'Kelola Item'}
                </button>
                <button
                  type="button"
                  onClick={() => toggle.mutate({ id: s.id, active: !s.is_active })}
                  disabled={toggle.isPending}
                  className="text-xs text-amber-600 hover:underline disabled:opacity-50"
                >
                  {s.is_active ? 'Nonaktifkan' : 'Aktifkan'}
                </button>
              </div>
            </div>
            {openSale === s.id && (
              <FlashSaleItemManager saleId={s.id} />
            )}
          </div>
        ))}
      </div>
    </div>
  )
}

interface FlashSaleItemDraft {
  variant_id: string
  product_id: string
  product_name: string
  product_slug: string
  variant_name: string
  sku: string
  image_url: string
  regular_price: number
  sale_price: number
  initial_stock: number
}

function FlashSaleItemManager({ saleId }: { saleId: string }) {
  const queryClient = useQueryClient()
  const [q, setQ] = useState('')
  const [search, setSearch] = useState('')
  const [drafts, setDrafts] = useState<FlashSaleItemDraft[]>([])
  const [error, setError] = useState('')

  const { data } = useQuery({
    queryKey: ['fs-products', search],
    queryFn: async () => (await api.get<{ items: Product[] }>(`/products?q=${encodeURIComponent(search)}&page_size=8`)).data.items,
    enabled: !!search,
  })

  const pickVariant = (p: Product, v: Variant) => {
    setDrafts((prev) => {
      if (prev.some((d) => d.variant_id === v.id)) return prev
      return [
        ...prev,
        {
          variant_id: v.id,
          product_id: p.id,
          product_name: p.name,
          product_slug: p.slug,
          variant_name: v.name,
          sku: v.sku,
          image_url: v.image_url ?? p.images?.[0]?.url ?? '',
          regular_price: v.price,
          sale_price: Math.round(v.price * 0.8),
          initial_stock: v.stock,
        },
      ]
    })
  }

  const saveItems = useMutation({
    mutationFn: async (items: FlashSaleItemDraft[]) =>
      api.post(`/admin/flash-sales/${saleId}/items`, { items }),
    onSuccess: () => {
      setDrafts([])
      setError('')
      queryClient.invalidateQueries({ queryKey: ['admin-flash-sales'] })
    },
    onError: (e: Error) => setError(e.message || 'Gagal menyimpan item sale'),
  })

  return (
    <div className="mt-4 border-t pt-4 space-y-3">
      <div className="flex gap-2">
        <label htmlFor={`fs-search-${saleId}`} className="sr-only">Cari produk</label>
        <input
          id={`fs-search-${saleId}`}
          value={q}
          onChange={(e) => setQ(e.target.value)}
          onKeyDown={(e) => e.key === 'Enter' && setSearch(q)}
          placeholder="Cari produk untuk ditambahkan..."
          className="flex-1 min-w-0 px-3 py-2 border rounded-lg text-sm outline-none dark:bg-gray-800"
        />
        <button
          type="button"
          onClick={() => setSearch(q)}
          disabled={!q.trim()}
          className="px-4 py-2 rounded-lg bg-gray-900 text-white text-sm disabled:opacity-50 shrink-0"
        >
          Cari
        </button>
      </div>
      {data && data.length > 0 && (
        <div className="space-y-2 max-h-64 overflow-y-auto">
          {data.map((p) => (
            <div key={p.id} className="border rounded-lg p-2.5">
              <p className="text-sm font-medium">{p.name}</p>
              <div className="flex flex-wrap gap-1.5 mt-1.5">
                {(p.variants ?? []).map((v) => (
                  <button
                    key={v.id}
                    type="button"
                    onClick={() => pickVariant(p, v)}
                    disabled={drafts.some((d) => d.variant_id === v.id)}
                    className="px-2.5 py-1 rounded-full border text-xs hover:border-amber-400 disabled:opacity-40"
                  >
                    {v.name} · {formatIDR(v.price)}
                  </button>
                ))}
              </div>
            </div>
          ))}
        </div>
      )}
      {drafts.length > 0 && (
        <div className="space-y-2">
          {drafts.map((d) => (
            <div key={d.variant_id} className="flex items-center gap-2 text-xs bg-gray-50 dark:bg-gray-800 rounded-lg p-2">
              <span className="flex-1 truncate">{d.product_name} — {d.variant_name}</span>
              <span className="text-gray-400 line-through">{formatIDR(d.regular_price)}</span>
              <label htmlFor={`fs-price-${d.variant_id}`} className="sr-only">Harga sale {d.variant_name}</label>
              <input
                id={`fs-price-${d.variant_id}`}
                type="number"
                min={0}
                value={d.sale_price}
                onChange={(e) =>
                  setDrafts((prev) =>
                    prev.map((x) =>
                      x.variant_id === d.variant_id ? { ...x, sale_price: Number(e.target.value) } : x,
                    ),
                  )
                }
                className="w-28 px-2 py-1 border rounded"
              />
              <button
                type="button"
                onClick={() => setDrafts((prev) => prev.filter((x) => x.variant_id !== d.variant_id))}
                className="text-red-500 hover:underline"
              >
                Hapus
              </button>
            </div>
          ))}
          {error && <p role="alert" className="text-xs text-red-600">{error}</p>}
          <button
            type="button"
            onClick={() => saveItems.mutate(drafts)}
            disabled={saveItems.isPending}
            className="px-4 py-2 rounded-lg bg-amber-500 text-white text-sm disabled:opacity-50"
          >
            {saveItems.isPending ? 'Menyimpan...' : `Simpan ${drafts.length} item`}
          </button>
        </div>
      )}
    </div>
  )
}


export function AdminDisputes() {
  const queryClient = useQueryClient()
  const { data } = useQuery({
    queryKey: ['admin-disputes'],
    queryFn: async () => (await api.get<{ disputes: { id: string; subject: string; description: string; status: string; buyer_name: string; seller_name: string; created_at: string }[] }>('/admin/disputes')).data.disputes,
  })

  const resolve = useMutation({
    mutationFn: async ({ id, decision }: { id: string; decision: string }) =>
      api.post(`/admin/disputes/${id}/resolve`, { decision }),
    onSuccess: () => queryClient.invalidateQueries({ queryKey: ['admin-disputes'] }),
    onError: (e: Error) => setError(e.message || 'Gagal menyelesaikan sengketa'),
  })
  const [error, setError] = useState('')
  const busy = resolve.isPending

  return (
    <div className="space-y-4">
      <h1 className="text-xl font-bold">Sengketa ({data?.length ?? 0})</h1>
      {error && (
        <p role="alert" className="text-sm text-red-600 bg-red-50 dark:bg-red-950/40 rounded-lg p-2.5">{error}</p>
      )}
      <div className="space-y-3">
        {data?.length === 0 && <p className="text-gray-500 text-sm">Tidak ada sengketa terbuka.</p>}
        {data?.map((d) => (
          <div key={d.id} className="bg-white dark:bg-gray-900 border border-gray-200 dark:border-gray-700 rounded-xl p-5">
            <div className="flex items-center justify-between">
              <p className="font-medium text-sm">{d.subject}</p>
              <span className="px-2.5 py-0.5 rounded-full bg-amber-100 text-amber-700 text-xs">{d.status}</span>
            </div>
            <p className="text-sm text-gray-600 dark:text-gray-300 mt-2">{d.description}</p>
            <p className="text-xs text-gray-400 mt-1">Pembeli: {d.buyer_name} · Penjual: {d.seller_name || '—'}</p>
            {d.status !== 'resolved' && (
              <div className="flex flex-wrap gap-2 mt-3">
                {[
                  { value: 'buyer', label: 'Menangkan Pembeli' },
                  { value: 'seller', label: 'Menangkan Penjual' },
                  { value: 'split', label: 'Split 50/50' },
                  { value: 'none', label: 'Tolak' },
                ].map((opt) => (
                  <button
                    key={opt.value}
                    type="button"
                    onClick={() => {
                      setError('')
                      resolve.mutate({ id: d.id, decision: opt.value })
                    }}
                    disabled={busy}
                    className="px-4 py-2 rounded-lg border text-sm hover:border-amber-400 disabled:opacity-50"
                  >
                    {opt.label}
                  </button>
                ))}
              </div>
            )}
          </div>
        ))}
      </div>
    </div>
  )
}


