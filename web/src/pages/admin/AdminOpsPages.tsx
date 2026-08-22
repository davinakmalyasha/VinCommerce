import { useState } from 'react'
import { useQuery } from '@tanstack/react-query'
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
          <input
            value={q}
            onChange={(e) => setQ(e.target.value)}
            onKeyDown={(e) => e.key === 'Enter' && setSearch(q)}
            placeholder="Cari no. pesanan / email / penjual"
            className="px-3 py-2 border rounded-lg text-sm outline-none w-64 dark:bg-gray-800"
          />
          <select value={status} onChange={(e) => setStatus(e.target.value)} className="px-3 py-2 border rounded-lg text-sm outline-none dark:bg-gray-800">
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
      <div className="bg-white dark:bg-gray-900 border border-gray-200 dark:border-gray-700 rounded-xl overflow-hidden">
        <table className="w-full text-sm">
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
      <div className="bg-white dark:bg-gray-900 border border-gray-200 dark:border-gray-700 rounded-xl overflow-hidden">
        <table className="w-full text-sm">
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
  const [pct, setPct] = useState('')
  const [fixed, setFixed] = useState('')

  const { data } = useQuery({
    queryKey: ['admin-commission'],
    queryFn: async () => (await api.get<{ fee: { pct: number; fixed: number } }>('/admin/commission')).data.fee,
  })

  const save = async () => {
    await api.put('/admin/commission', { pct: Number(pct || data?.pct), fixed: Number(fixed || data?.fixed || 0) })
    alert('Komisi diperbarui')
  }

  return (
    <div className="space-y-4 max-w-lg">
      <h1 className="text-xl font-bold">Komisi Platform</h1>
      <div className="bg-white dark:bg-gray-900 border border-gray-200 dark:border-gray-700 rounded-xl p-6 space-y-3">
        <p className="text-sm text-gray-500">
          Komisi dipotong dari escrow saat pesanan selesai: penjual menerima (total âˆ’ komisi), sisanya masuk dompet platform.
        </p>
        <div className="grid grid-cols-2 gap-3">
          <div>
            <label className="text-sm font-medium block mb-1">Persen (%)</label>
            <input type="number" value={pct || data?.pct} onChange={(e) => setPct(e.target.value)} className="w-full px-4 py-3 border rounded-xl text-sm outline-none dark:bg-gray-800" />
          </div>
          <div>
            <label className="text-sm font-medium block mb-1">Biaya tetap (Rp)</label>
            <input type="number" value={fixed || data?.fixed} onChange={(e) => setFixed(e.target.value)} className="w-full px-4 py-3 border rounded-xl text-sm outline-none dark:bg-gray-800" />
          </div>
        </div>
        <button onClick={save} className="px-6 py-3 rounded-xl bg-gray-900 dark:bg-gray-100 dark:text-gray-900 text-white text-sm font-medium">
          Simpan
        </button>
        <p className="text-xs text-gray-400">
          Saat ini: {data?.pct}% + Rp {data?.fixed} per transaksi
        </p>
      </div>
    </div>
  )
}

export function AdminCatalog() {
  const [brandName, setBrandName] = useState('')
  const [attrName, setAttrName] = useState('')
  const [attrValues, setAttrValues] = useState('')

  const { data: brands, refetch: refetchBrands } = useQuery({
    queryKey: ['brands'],
    queryFn: async () => (await api.get<{ brands: { id: string; name: string; is_active: boolean }[] }>('/catalog/brands')).data.brands,
  })

  const addBrand = async () => {
    await api.post('/catalog/brands', { name: brandName })
    setBrandName('')
    refetchBrands()
  }

  const toggleBrand = async (id: string, active: boolean) => {
    await api.post(`/catalog/brands/${id}/toggle`, { active })
    refetchBrands()
  }

  const addAttribute = async () => {
    await api.post('/catalog/attributes', {
      name: attrName,
      filterable: true,
      values: attrValues.split(',').map((v) => ({ value: v.trim() })).filter((v) => v.value),
    })
    setAttrName('')
    setAttrValues('')
    alert('Atribut ditambahkan')
  }

  return (
    <div className="space-y-6">
      <h1 className="text-xl font-bold">Kelola Katalog</h1>
      <div className="bg-white dark:bg-gray-900 border border-gray-200 dark:border-gray-700 rounded-xl p-5 space-y-3">
        <h2 className="font-bold text-sm">Brand</h2>
        <div className="flex gap-2">
          <input value={brandName} onChange={(e) => setBrandName(e.target.value)} placeholder="Nama brand" className="flex-1 px-3 py-2 border rounded-lg text-sm outline-none dark:bg-gray-800" />
          <button onClick={addBrand} className="px-4 py-2 rounded-lg bg-amber-500 text-white text-sm">Tambah</button>
        </div>
        <div className="flex flex-wrap gap-2">
          {brands?.map((b) => (
            <span key={b.id} className="px-3 py-1.5 rounded-full border text-sm flex items-center gap-2">
              {b.name}
              <button onClick={() => toggleBrand(b.id, !b.is_active)} className="text-xs text-amber-600">
                {b.is_active ? 'nonaktif' : 'aktif'}
              </button>
            </span>
          ))}
        </div>
      </div>
      <div className="bg-white dark:bg-gray-900 border border-gray-200 dark:border-gray-700 rounded-xl p-5 space-y-3">
        <h2 className="font-bold text-sm">Atribut Filter</h2>
        <div className="flex gap-2">
          <input value={attrName} onChange={(e) => setAttrName(e.target.value)} placeholder="Nama atribut (mis. Warna)" className="flex-1 px-3 py-2 border rounded-lg text-sm outline-none dark:bg-gray-800" />
          <input value={attrValues} onChange={(e) => setAttrValues(e.target.value)} placeholder="Nilai, dipisah koma" className="flex-1 px-3 py-2 border rounded-lg text-sm outline-none dark:bg-gray-800" />
          <button onClick={addAttribute} className="px-4 py-2 rounded-lg bg-amber-500 text-white text-sm">Tambah</button>
        </div>
      </div>
    </div>
  )
}

export function AdminFlashSales() {
  const [name, setName] = useState('')
  const [startsAt, setStartsAt] = useState('')
  const [endsAt, setEndsAt] = useState('')
  const [openSale, setOpenSale] = useState<string | null>(null)

  const { data, refetch } = useQuery({
    queryKey: ['admin-flash-sales'],
    queryFn: async () => (await api.get<{ sales: { id: string; name: string; starts_at: string; ends_at: string; is_active: boolean }[] }>('/admin/flash-sales')).data.sales,
  })

  const create = async () => {
    try {
      await api.post('/admin/flash-sales', {
        name,
        starts_at: new Date(startsAt).toISOString(),
        ends_at: new Date(endsAt).toISOString(),
      })
      setName('')
      refetch()
    } catch (e) {
      alert((e as Error).message)
    }
  }

  const toggle = async (id: string, active: boolean) => {
    await api.post(`/admin/flash-sales/${id}/toggle`, { active })
    refetch()
  }

  return (
    <div className="space-y-4">
      <h1 className="text-xl font-bold">Flash Sale</h1>
      <div className="bg-white dark:bg-gray-900 border border-gray-200 dark:border-gray-700 rounded-xl p-5 grid grid-cols-4 gap-3">
        <input value={name} onChange={(e) => setName(e.target.value)} placeholder="Nama (mis. Akhir Pekan Sale)" className="col-span-4 px-3 py-2 border rounded-lg text-sm outline-none dark:bg-gray-800" />
        <input type="datetime-local" value={startsAt} onChange={(e) => setStartsAt(e.target.value)} className="px-3 py-2 border rounded-lg text-sm outline-none dark:bg-gray-800" />
        <input type="datetime-local" value={endsAt} onChange={(e) => setEndsAt(e.target.value)} className="px-3 py-2 border rounded-lg text-sm outline-none dark:bg-gray-800" />
        <button onClick={create} disabled={!name || !startsAt || !endsAt} className="col-span-2 px-4 py-2 rounded-lg bg-amber-500 text-white text-sm disabled:opacity-50">
          Buat Sale
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
                <button onClick={() => setOpenSale(openSale === s.id ? null : s.id)} className="text-xs text-blue-600 hover:underline">
                  {openSale === s.id ? 'Tutup' : 'Kelola Item'}
                </button>
                <button onClick={() => toggle(s.id, !s.is_active)} className="text-xs text-amber-600 hover:underline">
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
  const [q, setQ] = useState('')
  const [search, setSearch] = useState('')
  const [drafts, setDrafts] = useState<FlashSaleItemDraft[]>([])
  const [adding, setAdding] = useState(false)

  const { data } = useQuery({
    queryKey: ['fs-products', search],
    queryFn: async () => (await api.get<{ items: Product[] }>(`/products?q=${encodeURIComponent(search)}&page_size=8`)).data.items,
    enabled: !!search,
  })

  const pickVariant = (p: Product, v: Variant) => {
    if (drafts.some((d) => d.variant_id === v.id)) return
    setDrafts([
      ...drafts,
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
    ])
  }

  const saveItems = async () => {
    if (drafts.length === 0) return
    setAdding(true)
    try {
      await api.post(`/admin/flash-sales/${saleId}/items`, { items: drafts })
      setDrafts([])
    } catch (e) {
      alert((e as Error).message)
    } finally {
      setAdding(false)
    }
  }

  return (
    <div className="mt-4 border-t pt-4 space-y-3">
      <div className="flex gap-2">
        <input
          value={q}
          onChange={(e) => setQ(e.target.value)}
          onKeyDown={(e) => e.key === 'Enter' && setSearch(q)}
          placeholder="Cari produk untuk ditambahkan..."
          className="flex-1 px-3 py-2 border rounded-lg text-sm outline-none dark:bg-gray-800"
        />
        <button onClick={() => setSearch(q)} disabled={!q.trim()} className="px-4 py-2 rounded-lg bg-gray-900 text-white text-sm disabled:opacity-50">
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
              <input
                type="number"
                value={d.sale_price}
                min={0}
                onChange={(e) =>
                  setDrafts(drafts.map((x) => (x.variant_id === d.variant_id ? { ...x, sale_price: Number(e.target.value) } : x)))
                }
                className="w-28 px-2 py-1 border rounded"
              />
              <button
                onClick={() => setDrafts(drafts.filter((x) => x.variant_id !== d.variant_id))}
                className="text-red-500 hover:underline"
              >
                Hapus
              </button>
            </div>
          ))}
          <button onClick={saveItems} disabled={adding} className="px-4 py-2 rounded-lg bg-amber-500 text-white text-sm disabled:opacity-50">
            {adding ? 'Menyimpan...' : `Simpan ${drafts.length} item`}
          </button>
        </div>
      )}
    </div>
  )
}

export function AdminDisputes() {
  const { data, refetch } = useQuery({
    queryKey: ['admin-disputes'],
    queryFn: async () => (await api.get<{ disputes: { id: string; subject: string; description: string; status: string; buyer_name: string; seller_name: string; created_at: string }[] }>('/admin/disputes')).data.disputes,
  })
  const [busyId, setBusyId] = useState<string | null>(null)

  const resolve = async (id: string, decision: string) => {
    setBusyId(id)
    try {
      await api.post(`/admin/disputes/${id}/resolve`, { decision })
      refetch()
    } catch (e) {
      alert((e as Error).message)
    } finally {
      setBusyId(null)
    }
  }

  return (
    <div className="space-y-4">
      <h1 className="text-xl font-bold">Sengketa ({data?.length ?? 0})</h1>
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
              <div className="flex gap-2 mt-3">
                {[
                  { value: 'buyer', label: 'Menangkan Pembeli' },
                  { value: 'seller', label: 'Menangkan Penjual' },
                  { value: 'split', label: 'Split 50/50' },
                  { value: 'none', label: 'Tolak' },
                ].map((opt) => (
                  <button
                    key={opt.value}
                    onClick={() => resolve(d.id, opt.value)}
                    disabled={busyId === d.id}
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

