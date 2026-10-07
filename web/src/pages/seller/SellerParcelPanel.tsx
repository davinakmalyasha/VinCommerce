import { useMutation, useQuery, useQueryClient } from '@tanstack/react-query'
import { useState } from 'react'
import { api, downloadFile } from '../../lib/api'
import { formatDate } from '../../lib/format'

interface ParcelLine {
  id: string
  product_name: string
  quantity: number
}

interface Parcel {
  id: string
  sequence: number
  kind: string
  status: string
  carrier: string
  carrier_service?: string
  tracking_number?: string
  label_url?: string
  label_format?: string
  weight_grams: number
  shipped_at?: string
  delivered_at?: string
}

const CARRIERS = ['JNE', 'J&T', 'SiCepat', 'Pos Indonesia', 'GoSend', 'AnterAja']

const PARCEL_STATUS: Record<string, string> = {
  created: 'bg-gray-100 text-gray-700',
  labeled: 'bg-indigo-100 text-indigo-700',
  dispatched: 'bg-blue-100 text-blue-700',
  in_transit: 'bg-blue-100 text-blue-700',
  delivered: 'bg-green-100 text-green-700',
  received: 'bg-green-100 text-green-700',
  cancelled: 'bg-gray-100 text-gray-500',
  void: 'bg-gray-100 text-gray-500 line-through',
}

function openLabel(url: string) {
  if (/^https?:\/\//i.test(url)) {
    window.open(url, '_blank', 'noopener,noreferrer')
    return
  }
  void downloadFile(url, 'label-parcel.pdf')
}

/**
 * Create, label and dispatch the parcels for one order.
 *
 * Kept out of SellerOrders on purpose: that page is a list of every order, and
 * putting a quantity picker, a carrier select, a tracking form and a
 * spend-money confirmation inline on every row would make it unusable at the
 * 20 orders a page it renders. The panel is opened per order.
 */
export function SellerParcelPanel({ orderId, items }: { orderId: string; items: ParcelLine[] }) {
  const queryClient = useQueryClient()
  const [error, setError] = useState('')
  const [qty, setQty] = useState<Record<string, number>>({})
  const [carrier, setCarrier] = useState(CARRIERS[0])
  const [creating, setCreating] = useState(false)
  const [trackingFor, setTrackingFor] = useState<string | null>(null)
  const [tracking, setTracking] = useState('')
  const [confirmLabel, setConfirmLabel] = useState<string | null>(null)

  const key = ['seller-order-parcels', orderId]

  const { data: parcels, isLoading } = useQuery({
    queryKey: key,
    queryFn: async () =>
      (await api.get<{ parcels: Parcel[] }>(`/seller/orders/${orderId}/parcels`)).data.parcels,
  })

  const invalidate = () => {
    queryClient.invalidateQueries({ queryKey: key })
    queryClient.invalidateQueries({ queryKey: ['seller-orders'] })
  }

  // The server rejects an empty parcel outright, so the button is disabled rather
  // than letting the seller click and read an error for a form that obviously has
  // nothing in it.
  const create = useMutation({
    mutationFn: async () =>
      api.post<{ parcel: Parcel }>(`/seller/orders/${orderId}/parcels`, {
        carrier,
        items: Object.entries(qty)
          .filter(([, q]) => q > 0)
          .map(([order_item_id, quantity]) => ({ order_item_id, quantity })),
      }),
    onSuccess: async () => {
      setError('')
      setCreating(false)
      setQty({})
      await invalidate()
    },
    onError: (e: Error) => setError(e.message),
  })

  // Buying a label SPENDS MONEY and is often irreversible at the carrier, while
  // handing the box over is free. Never folded into dispatch -- two buttons, two
  // confirmations.
  const buyLabel = useMutation({
    mutationFn: async ({ id, format }: { id: string; format: string }) =>
      api.post<{ parcel: Parcel }>(`/seller/parcels/${id}/label`, { format }),
    onSuccess: async ({ data }) => {
      setConfirmLabel(null)
      if (data.parcel?.label_url) openLabel(data.parcel.label_url)
      await invalidate()
    },
    onError: (e: Error) => {
      setConfirmLabel(null)
      setError(e.message)
    },
  })

  const dispatch = useMutation({
    mutationFn: async ({ id }: { id: string }) =>
      api.post<{ parcel: Parcel }>(`/seller/parcels/${id}/dispatch`, { tracking_number: tracking }),
    onSuccess: async () => {
      setTrackingFor(null)
      setTracking('')
      setError('')
      await invalidate()
    },
    onError: (e: Error) => setError(e.message),
  })

  const totalQty = Object.values(qty).reduce((a, b) => a + (b || 0), 0)

  return (
    <div className="border-t mt-3 pt-3 space-y-3">
      <div className="flex items-center justify-between">
        <p className="text-xs font-medium uppercase text-gray-500">Parcel</p>
        <button type="button"
          onClick={() => setCreating((c) => !c)}
          className="text-xs text-indigo-600 hover:underline"
        >
          {creating ? 'Batal' : '+ Buat parcel'}
        </button>
      </div>

      {error && (
        <p role="alert" className="rounded-lg bg-red-50 p-2 text-xs text-red-700">{error}</p>
      )}

      {creating && (
        <div className="rounded-lg bg-gray-50 p-3 space-y-2">
          <p className="text-xs text-gray-600">Jumlah per item yang dimasukkan ke parcel ini.</p>
          <div className="space-y-1.5">
            {items.map((it) => (
              <div key={it.id} className="flex items-center justify-between gap-3 text-xs">
                <span className="line-clamp-1 flex-1">{it.product_name}</span>
                <span className="text-gray-400">max {it.quantity}</span>
                <input
                  type="number"
                  min={0}
                  max={it.quantity}
                  value={qty[it.id] ?? 0}
                  onChange={(e) =>
                    setQty((q) => ({
                      ...q,
                      // Clamped to what was actually ordered: the server refuses a
                      // quantity over the order line, but a form that lets you type
                      // it and then rejects it is a form with a bug in it.
                      [it.id]: Math.max(0, Math.min(it.quantity, Number(e.target.value) || 0)),
                    }))
                  }
                  className="w-20 rounded border border-gray-300 px-2 py-1 text-right"
                  aria-label={`Jumlah ${it.product_name}`}
                />
              </div>
            ))}
          </div>
          <div className="flex items-center gap-2">
            <select value={carrier} onChange={(e) => setCarrier(e.target.value)}
              className="rounded border border-gray-300 px-2 py-1 text-xs" aria-label="Kurir">
              {CARRIERS.map((c) => <option key={c}>{c}</option>)}
            </select>
            <button type="button"
              onClick={() => create.mutate()}
              disabled={create.isPending || totalQty === 0}
              className="rounded-lg bg-indigo-600 px-4 py-1.5 text-xs font-medium text-white hover:bg-indigo-700 disabled:opacity-50"
            >
              {create.isPending ? 'Membuat…' : `Buat parcel (${totalQty} unit)`}
            </button>
          </div>
        </div>
      )}

      {isLoading && <p className="text-xs text-gray-500">Memuat parcel…</p>}
      {!isLoading && (parcels?.length ?? 0) === 0 && !creating && (
        <p className="text-xs text-gray-500">Belum ada parcel untuk pesanan ini.</p>
      )}

      {parcels?.map((p) => (
        <div key={p.id} className="rounded-lg border border-gray-200 p-3 space-y-2">
          <div className="flex flex-wrap items-center justify-between gap-2">
            <div className="flex flex-wrap items-center gap-x-3 gap-y-1 text-xs">
              <span className="font-medium">Parcel #{p.sequence}</span>
              <span className={`rounded-full px-2 py-0.5 font-medium ${PARCEL_STATUS[p.status] ?? 'bg-gray-100 text-gray-700'}`}>
                {p.status}
              </span>
              <span className="text-gray-500">{p.carrier}{p.carrier_service ? ` · ${p.carrier_service}` : ''}</span>
              {p.weight_grams > 0 && <span className="text-gray-500">{p.weight_grams} g</span>}
              {p.tracking_number && <span className="font-mono text-blue-700">{p.tracking_number}</span>}
              {p.delivered_at && <span className="text-gray-500">tiba {formatDate(p.delivered_at)}</span>}
              {!p.delivered_at && p.shipped_at && <span className="text-gray-500">kirim {formatDate(p.shipped_at)}</span>}
            </div>
            <div className="flex items-center gap-2">
              {p.label_url && (
                <button type="button"
                  onClick={() => openLabel(p.label_url!)}
                  className="text-xs text-indigo-600 hover:underline"
                >
                  Label {p.label_format ?? 'pdf'}
                </button>
              )}
              {confirmLabel === p.id ? (
                <span className="flex items-center gap-1.5 text-xs text-red-600">
                  Bayar sekarang?
                  <button type="button"
                    onClick={() => buyLabel.mutate({ id: p.id, format: p.label_format ?? 'pdf' })}
                    disabled={buyLabel.isPending}
                    className="rounded bg-red-600 px-2 py-0.5 text-white disabled:opacity-50"
                  >
                    {buyLabel.isPending ? '…' : 'Ya'}
                  </button>
                  <button type="button" onClick={() => setConfirmLabel(null)} className="text-gray-500">Batal</button>
                </span>
              ) : (
                !p.label_url && (
                  <button type="button"
                    onClick={() => setConfirmLabel(p.id)}
                    className="rounded border border-indigo-300 px-2 py-1 text-xs text-indigo-700 hover:bg-indigo-50"
                    title="Membeli label berbayar dan tidak selalu bisa dibatalkan"
                  >
                    Beli label
                  </button>
                )
              )}
            </div>
          </div>

          {['created', 'labeled'].includes(p.status) && (
            trackingFor === p.id ? (
              <div className="flex items-center gap-2">
                <input
                  placeholder="Nomor resi"
                  value={tracking}
                  onChange={(e) => setTracking(e.target.value)}
                  className="w-44 rounded border border-gray-300 px-2 py-1 text-xs"
                  aria-label="Nomor resi"
                />
                <button type="button"
                  onClick={() => dispatch.mutate({ id: p.id })}
                  disabled={dispatch.isPending || !tracking.trim()}
                  className="rounded-lg bg-purple-600 px-3 py-1 text-xs text-white hover:bg-purple-700 disabled:opacity-50"
                >
                  {dispatch.isPending ? '…' : 'Serahkan ke kurir'}
                </button>
                <button type="button" onClick={() => setTrackingFor(null)} className="text-xs text-gray-400">Batal</button>
              </div>
            ) : (
              <button type="button"
                onClick={() => setTrackingFor(p.id)}
                className="text-xs text-purple-700 hover:underline"
              >
                Serahkan ke kurir (isi resi)
              </button>
            )
          )}
        </div>
      ))}
    </div>
  )
}
