import { useState } from 'react'
import { useMutation, useQuery, useQueryClient } from '@tanstack/react-query'
import { Link, useNavigate } from 'react-router-dom'
import { api } from '../lib/api'
import type { CartLine } from '../types'
import { formatIDR } from '../lib/format'
import { useSession } from '../stores/session'
import { QueryState } from '../components/QueryState'

interface FreeShippingInfo {
  seller_id: string
  name: string
  subtotal: number
  threshold?: number | null
}

interface CartPayload {
  lines: CartLine[]
  freeShip: FreeShippingInfo[]
}

const CART_KEY = ['cart'] as const

/**
 * Applies a quantity change to the cached cart so the stepper updates on the
 * same tick the button was pressed. Recomputing `subtotal` client-side is
 * safe because `subtotal` is exactly `price × quantity`; the server value
 * arrives in `onSettled` and overwrites it either way.
 */
function patchQuantity(cached: CartPayload | undefined, variantId: string, quantity: number): CartPayload | undefined {
  if (!cached) return cached
  return {
    ...cached,
    lines: cached.lines.map((l) =>
      l.variant_id === variantId ? { ...l, quantity, subtotal: l.price * quantity } : l,
    ),
  }
}

export function CartPage() {
  const queryClient = useQueryClient()
  const navigate = useNavigate()
  const { user } = useSession()
  const [selected, setSelected] = useState<Set<string>>(new Set())
  // Which rows have a request in flight. The old code used the mutation's
  // single `isPending`, so touching one row disabled every row's buttons.
  const [pendingRows, setPendingRows] = useState<Set<string>>(new Set())
  const [rowError, setRowError] = useState('')

  const cartQuery = useQuery({
    queryKey: CART_KEY,
    queryFn: async () => {
      const res = (await api.get<{ lines: CartLine[]; free_shipping?: FreeShippingInfo[] }>('/cart')).data
      return { lines: (res.lines ?? []) as CartLine[], freeShip: res.free_shipping ?? [] }
    },
  })
  const lines = cartQuery.data?.lines ?? []
  const freeShip = cartQuery.data?.freeShip

  const markRow = (variantId: string, on: boolean) =>
    setPendingRows((prev) => {
      if (prev.has(variantId) === on) return prev
      const next = new Set(prev)
      if (on) next.add(variantId)
      else next.delete(variantId)
      return next
    })

  const update = useMutation({
    mutationFn: async ({ variantId, quantity }: { variantId: string; quantity: number }) => {
      markRow(variantId, true)
      await api.put('/cart/items', { variant_id: variantId, quantity })
    },
    onMutate: async ({ variantId, quantity }) => {
      // Cancel any in-flight cart fetch first: a response that is already on
      // the wire carries the PRE-click quantity and would otherwise overwrite
      // the optimistic value a moment later.
      await queryClient.cancelQueries({ queryKey: CART_KEY })
      const previous = queryClient.getQueryData<CartPayload>(CART_KEY)
      queryClient.setQueryData<CartPayload>(CART_KEY, (cached) =>
        patchQuantity(cached, variantId, quantity),
      )
      setRowError('')
      return { previous, variantId }
    },
    onError: (e: Error, _vars, ctx) => {
      // Roll back to the snapshot; the optimistic number is a guess until the
      // server confirms it.
      if (ctx?.previous) queryClient.setQueryData<CartPayload>(CART_KEY, ctx.previous)
      setRowError(e.message || 'Gagal memperbarui jumlah.')
    },
    // Cleared in onSettled, not onSuccess: a failed request must release the
    // row too, or it stays disabled forever.
    onSettled: (_d, _e, vars) => {
      markRow(vars.variantId, false)
      queryClient.invalidateQueries({ queryKey: CART_KEY })
    },
  })

  const remove = useMutation({
    mutationFn: async (variantId: string) => {
      markRow(variantId, true)
      await api.delete(`/cart/items/${variantId}`)
    },
    onMutate: async (variantId) => {
      await queryClient.cancelQueries({ queryKey: CART_KEY })
      const previous = queryClient.getQueryData<CartPayload>(CART_KEY)
      queryClient.setQueryData<CartPayload>(CART_KEY, (cached) =>
        cached ? { ...cached, lines: cached.lines.filter((l) => l.variant_id !== variantId) } : cached,
      )
      setRowError('')
      return { previous, variantId }
    },
    onError: (e: Error, _v, ctx) => {
      if (ctx?.previous) queryClient.setQueryData<CartPayload>(CART_KEY, ctx.previous)
      setRowError(e.message || 'Gagal menghapus item.')
    },
    onSettled: (_d, _e, variantId) => {
      markRow(variantId, false)
      queryClient.invalidateQueries({ queryKey: CART_KEY })
    },
    onSuccess: (_d, variantId) => {
      setSelected((prev) => {
        const next = new Set(prev)
        next.delete(variantId)
        return next
      })
    },
  })

  const bulkRemove = useMutation({
    mutationFn: async (variantIds: string[]) => api.post('/cart/bulk-remove', { variant_ids: variantIds }),
    onSuccess: () => {
      setSelected(new Set())
      queryClient.invalidateQueries({ queryKey: CART_KEY })
    },
    onError: (e: Error) => setRowError(e.message || 'Gagal menghapus item.'),
  })

  const bulkMove = useMutation({
    mutationFn: async (variantIds: string[]) => api.post('/cart/bulk-move', { variant_ids: variantIds }),
    onSuccess: () => {
      setSelected(new Set())
      queryClient.invalidateQueries({ queryKey: CART_KEY })
      queryClient.invalidateQueries({ queryKey: ['wishlist'] })
    },
    onError: (e: Error) => setRowError(e.message || 'Gagal memindahkan item.'),
  })

  if (cartQuery.isLoading) {
    return <QueryState query={cartQuery} label="keranjang" />
  }

  if (cartQuery.isError) {
    return <QueryState query={cartQuery} label="keranjang" />
  }

  if (lines.length === 0) {
    return (
      <div className="mx-auto max-w-7xl px-4 py-20 text-center">
        <p className="text-gray-500 mb-4">Keranjangmu masih kosong.</p>
        <Link to="/search" className="px-6 py-3 rounded-xl bg-amber-500 text-white font-medium">
          Mulai Belanja
        </Link>
      </div>
    )
  }

  const allSelected = selected.size === lines.length
  const toggleAll = () => setSelected(allSelected ? new Set() : new Set(lines.map((l) => l.variant_id)))
  const toggle = (variantId: string) =>
    setSelected((prev) => {
      const next = new Set(prev)
      if (next.has(variantId)) next.delete(variantId)
      else next.add(variantId)
      return next
    })

  const selectedLines = lines.filter((l) => selected.has(l.variant_id))
  const total = lines.reduce((s, l) => s + l.subtotal, 0)
  const selectedCount = selectedLines.length

  const sellers = new Map<string, { name: string; items: CartLine[] }>()
  for (const l of lines) {
    const g = sellers.get(l.seller_id)
    if (g) g.items.push(l)
    else sellers.set(l.seller_id, { name: l.seller_name, items: [l] })
  }

  return (
    <div className="mx-auto max-w-7xl px-4 py-6 grid grid-cols-1 lg:grid-cols-3 gap-6">
      <div className="lg:col-span-2 space-y-3">
        <div className="flex items-center justify-between text-sm">
          <label className="flex items-center gap-2 cursor-pointer select-none">
            <input type="checkbox" checked={allSelected} onChange={toggleAll} className="accent-amber-500" />
            <span className="font-medium">Pilih semua ({lines.length})</span>
          </label>
          {selectedCount > 0 && (
            <div className="flex gap-4">
              {user && (
                <button
                  type="button"
                  onClick={() => bulkMove.mutate([...selected])}
                  disabled={bulkMove.isPending}
                  className="text-blue-600 hover:underline disabled:opacity-50"
                >
                  Pindah ke wishlist ({selectedCount})
                </button>
              )}
              <button
                type="button"
                onClick={() => bulkRemove.mutate([...selected])}
                disabled={bulkRemove.isPending}
                className="text-red-500 hover:underline disabled:opacity-50"
              >
                Hapus ({selectedCount})
              </button>
            </div>
          )}
        </div>

        {rowError && (
          <p role="alert" className="text-sm text-red-600 bg-red-50 dark:bg-red-950/40 rounded-lg p-2.5">
            {rowError}
          </p>
        )}

        {[...sellers.entries()].map(([sellerId, group]) => {
          const groupTotal = group.items.reduce((s, l) => s + l.subtotal, 0)
          const fs = freeShip?.find((f) => f.seller_id === sellerId)
          const fsPct = fs?.threshold ? Math.min(100, Math.round((fs.subtotal / fs.threshold) * 100)) : 100
          const fsRemaining = fs?.threshold ? Math.max(0, fs.threshold - fs.subtotal) : 0
          return (
            <div key={sellerId} className="bg-white border border-gray-200 rounded-xl overflow-hidden">
              <div className="px-4 py-2.5 bg-gray-50 dark:bg-gray-800 border-b border-gray-200 flex items-center justify-between">
                <p className="text-sm font-semibold">🏪 {group.name}</p>
                <p className="text-xs text-gray-500">Subtotal toko: <b>{formatIDR(groupTotal)}</b></p>
              </div>
              {fs?.threshold ? (
                <div className="px-4 py-2 bg-amber-50 dark:bg-amber-900/20">
                  <div className="h-1.5 rounded-full bg-amber-200 dark:bg-amber-800 overflow-hidden">
                    <div className={`h-full ${fsPct >= 100 ? 'bg-green-500' : 'bg-amber-500'}`} style={{ width: `${fsPct}%` }} />
                  </div>
                  <p className="text-xs mt-1 text-amber-700 dark:text-amber-300">
                    {fsRemaining > 0
                      ? <>🚚 Tambah <b>{formatIDR(fsRemaining)}</b> lagi dari toko ini untuk <b>gratis ongkir</b>!</>
                      : <>🎉 Selamat, pesanan dari toko ini <b>gratis ongkir</b>!</>}
                  </p>
                </div>
              ) : null}
              <div className="divide-y divide-gray-100 dark:divide-gray-800">
                {group.items.map((l) => (
                  <CartRow
                    key={l.variant_id}
                    line={l}
                    busy={pendingRows.has(l.variant_id)}
                    checked={selected.has(l.variant_id)}
                    onToggle={() => toggle(l.variant_id)}
                    onIncrease={() => update.mutate({ variantId: l.variant_id, quantity: l.quantity + 1 })}
                    onDecrease={() => {
                      // At quantity 1 the minus button removes the line.
                      // Sending quantity: 0 was a 400 from the cart service
                      // and left the item stuck at 1.
                      if (l.quantity <= 1) remove.mutate(l.variant_id)
                      else update.mutate({ variantId: l.variant_id, quantity: l.quantity - 1 })
                    }}
                    onRemove={() => remove.mutate(l.variant_id)}
                  />
                ))}
              </div>
            </div>
          )
        })}
      </div>

      <aside className="h-fit bg-white border border-gray-200 rounded-xl p-5 space-y-3">
        <h2 className="font-bold">Ringkasan</h2>
        <div className="flex justify-between text-sm">
          <span className="text-gray-500">Subtotal ({lines.length} item)</span>
          <span className="font-medium">{formatIDR(total)}</span>
        </div>
        <div className="flex justify-between text-sm">
          <span className="text-gray-500">Ongkir</span>
          <span className="text-gray-400">Dihitung saat checkout</span>
        </div>
        <hr />
        <div className="flex justify-between font-bold">
          <span>Total</span>
          <span className="text-amber-600">{formatIDR(total)}</span>
        </div>
        <button
          type="button"
          onClick={() => navigate('/checkout')}
          className="w-full py-3 rounded-xl bg-amber-500 text-white font-semibold hover:bg-amber-600"
        >
          Checkout
        </button>
        {selectedCount > 0 && (
          <p className="text-xs text-gray-400">
            {selectedCount} item dipilih — gunakan aksi di atas untuk menghapus atau memindahkan ke wishlist.
          </p>
        )}
      </aside>
    </div>
  )
}

interface CartRowProps {
  line: CartLine
  busy: boolean
  checked: boolean
  onToggle: () => void
  onIncrease: () => void
  onDecrease: () => void
  onRemove: () => void
}

function CartRow({ line: l, busy, checked, onToggle, onIncrease, onDecrease, onRemove }: CartRowProps) {
  return (
    <div className={`p-4 flex gap-4 items-center ${busy ? 'opacity-60' : ''}`}>
      <input
        type="checkbox"
        checked={checked}
        onChange={onToggle}
        aria-label={`Pilih ${l.product_name}`}
        className="accent-amber-500"
      />
      {l.image_url ? (
        <img src={l.image_url} alt="" className="w-20 h-20 rounded-lg object-cover bg-gray-100" />
      ) : (
        <div className="w-20 h-20 rounded-lg bg-gray-100 flex items-center justify-center text-xs text-gray-400">
          {l.product_name.slice(0, 10)}
        </div>
      )}
      <div className="flex-1">
        <Link to={`/product/${l.product_slug ?? l.product_id}`} className="font-medium text-sm hover:text-amber-600 line-clamp-1">
          {l.product_name}
        </Link>
        <p className="text-xs text-gray-500 mt-0.5">
          {l.variant_name} · dari {l.seller_name}
        </p>
        <div className="flex items-center justify-between mt-3">
          <div className="flex items-center border rounded-lg">
            <button
              type="button"
              aria-label={l.quantity <= 1 ? `Hapus ${l.product_name} dari keranjang` : `Kurangi jumlah ${l.product_name}`}
              onClick={onDecrease}
              disabled={busy}
              className="px-3 py-1.5 text-gray-500 hover:text-gray-900 disabled:opacity-40"
            >
              −
            </button>
            <span className="w-8 text-center text-sm" aria-live="polite">
              {l.quantity}
            </span>
            <button
              type="button"
              aria-label={`Tambah jumlah ${l.product_name}`}
              onClick={onIncrease}
              disabled={busy}
              className="px-3 py-1.5 text-gray-500 hover:text-gray-900 disabled:opacity-40"
            >
              +
            </button>
          </div>
          <div className="flex items-center gap-3">
            <p className="font-bold text-amber-600">{formatIDR(l.subtotal)}</p>
            <button
              type="button"
              onClick={onRemove}
              disabled={busy}
              className="text-xs text-red-500 hover:underline disabled:opacity-40"
            >
              Hapus
            </button>
          </div>
        </div>
      </div>
    </div>
  )
}
