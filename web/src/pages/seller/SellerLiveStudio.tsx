import { useState } from 'react'
import { useMutation, useQuery, useQueryClient } from '@tanstack/react-query'
import { api } from '../../lib/api'
import type { Product } from '../../types'
import { formatIDR } from '../../lib/format'

interface LiveSession {
  id: string
  title: string
  status: 'scheduled' | 'live' | 'ended'
  youtube_video_id?: string
  viewer_peak: number
}

interface LiveProductRow {
  variant_id: string
  product_name: string
  variant_name: string
  pinned_at?: string | null
  unpinned_at?: string | null
  regular_price: number
  stock: number
}

export function SellerLiveStudio() {
  const queryClient = useQueryClient()
  const [title, setTitle] = useState('')
  const [videoId, setVideoId] = useState('')
  const [managing, setManaging] = useState<string | null>(null)

  const { data } = useQuery({
    queryKey: ['seller-live'],
    queryFn: async () => (await api.get<{ sessions: LiveSession[] }>('/seller/live')).data.sessions,
    refetchInterval: 10_000,
  })

  const create = useMutation({
    mutationFn: async () =>
      api.post('/seller/live', { title, youtube_video_id: videoId }),
    onSuccess: () => {
      setTitle('')
      setVideoId('')
      queryClient.invalidateQueries({ queryKey: ['seller-live'] })
    },
    onError: (e: Error) => setError(e.message || 'Gagal membuat sesi live'),
  })

  const transition = useMutation({
    mutationFn: async ({ id, to }: { id: string; to: string }) =>
      api.post(`/seller/live/${id}/transition`, { to }),
    onSuccess: () => queryClient.invalidateQueries({ queryKey: ['seller-live'] }),
    onError: (e: Error) => setError(e.message || 'Gagal mengubah status sesi'),
  })
  const [error, setError] = useState('')

  return (
    <div className="space-y-6">
      <div className="flex items-center justify-between">
        <h1 className="text-xl font-bold">📺 Live Studio</h1>
        <span className="text-xs px-2 py-1 rounded-full bg-purple-100 text-purple-700">Live Commerce</span>
      </div>

      {error && (
        <p role="alert" className="rounded-lg bg-red-50 dark:bg-red-950/40 p-2.5 text-sm text-red-700">
          {error}
          <button type="button" onClick={() => setError('')} className="ml-2 underline">Tutup</button>
        </p>
      )}

      <div className="bg-white dark:bg-gray-900 border border-gray-200 dark:border-gray-700 rounded-xl p-5 grid grid-cols-1 md:grid-cols-3 gap-3">
        <label htmlFor="live-title" className="sr-only">Judul siaran</label>
        <input
          id="live-title"
          value={title}
          onChange={(e) => setTitle(e.target.value)}
          placeholder="Judul siaran (mis. Flash Sale Skincare!)"
          className="px-3 py-2 border rounded-lg text-sm outline-none"
        />
        <label htmlFor="live-video" className="sr-only">YouTube video ID</label>
        <input
          id="live-video"
          value={videoId}
          onChange={(e) => setVideoId(e.target.value.trim())}
          placeholder="YouTube video ID (untuk stream)"
          className="px-3 py-2 border rounded-lg text-sm outline-none"
        />
        <button type="button"
          onClick={() => create.mutate()}
          disabled={create.isPending || title.trim().length < 3}
          className="px-4 py-2 rounded-lg bg-red-600 text-white text-sm font-medium disabled:opacity-50"
        >
          {create.isPending ? 'Membuat...' : '+ Jadwalkan Siaran'}
        </button>
        <p className="md:col-span-3 text-xs text-gray-400">
          Masukkan ID video dari URL YouTube, mis. dQw4w9WgXcQ dari youtube.com/watch?v=dQw4w9WgXcQ.
          Saat siaran berjalan, produk yang di-pin otomatis muncul di halaman penonton.
        </p>
      </div>

      <div className="space-y-3">
        {(data ?? []).length === 0 && <p className="text-sm text-gray-500">Belum ada siaran.</p>}
        {(data ?? []).map((s) => (
          <div key={s.id} className="bg-white dark:bg-gray-900 border border-gray-200 dark:border-gray-700 rounded-xl p-4 space-y-3">
            <div className="flex items-center justify-between gap-3 flex-wrap">
              <div>
                <p className="font-medium text-sm">{s.title}</p>
                <p className="text-xs text-gray-500">
                  {s.status === 'live' ? '🔴 LIVE' : s.status} · 👁 {s.viewer_peak} penonton
                  {s.youtube_video_id ? ` · yt:${s.youtube_video_id}` : ''}
                </p>
              </div>
              <div className="flex gap-2">
                {s.status === 'scheduled' && (
                  <button type="button" onClick={() => transition.mutate({ id: s.id, to: 'live' })} disabled={transition.isPending}
                    className="px-4 py-2 rounded-lg bg-red-600 text-white text-sm font-medium disabled:opacity-50">
                    🔴 Go Live
                  </button>
                )}
                {s.status === 'live' && (
                  <button type="button" onClick={() => transition.mutate({ id: s.id, to: 'ended' })} disabled={transition.isPending}
                    className="px-4 py-2 rounded-lg border border-gray-300 text-sm disabled:opacity-50">
                    Akhiri Siaran
                  </button>
                )}
                {s.status !== 'ended' && (
                  <button type="button" onClick={() => setManaging(managing === s.id ? null : s.id)}
                    className="px-4 py-2 rounded-lg border text-sm hover:border-amber-400">
                    {managing === s.id ? 'Tutup' : 'Kelola Produk'}
                  </button>
                )}
              </div>
            </div>
            {managing === s.id && s.status !== 'ended' && (
              <StudioProducts sessionId={s.id} />
            )}
          </div>
        ))}
      </div>
    </div>
  )
}

function StudioProducts({ sessionId }: { sessionId: string }) {
  const [page] = useState(1)
  const { data: attached, refetch } = useQuery({
    queryKey: ['live-catalog', sessionId],
    queryFn: async () => (await api.get<{ products: LiveProductRow[] }>(`/seller/live/${sessionId}/catalog`)).data.products,
  })

  const { data: products } = useQuery({
    queryKey: ['seller-products', page],
    queryFn: async () =>
      (await api.get<{ products: Product[] }>('/seller/products', { params: { page, page_size: 20 } })).data.products,
  })

  const attachAll = useMutation({
    mutationFn: async (variantIds: string[]) =>
      api.put(`/seller/live/${sessionId}/products`, { variant_ids: variantIds }),
    onSuccess: () => refetch(),
    onError: (e: Error) => setError(e.message || 'Gagal menempel produk'),
  })

  const togglePin = useMutation({
    mutationFn: async ({ variantId, pinned }: { variantId: string; pinned: boolean }) =>
      api.post(`/seller/live/${sessionId}/pin`, { variant_id: variantId, pinned }),
    onSuccess: () => refetch(),
    onError: (e: Error) => setError(e.message || 'Gagal mengubah status sematan'),
  })
  const [error, setError] = useState('')

  const attachedIds = new Set((attached ?? []).map((p) => p.variant_id))

  const pickVariant = (_p: Product, v: { id: string; name: string; price: number }) => {
    if (!attachedIds.has(v.id)) {
      attachAll.mutate([...(attached?.map((a) => a.variant_id) ?? []), v.id])
    }
  }

  const isPinned = (row: LiveProductRow) => !!row.pinned_at && !row.unpinned_at

  return (
    <div className="border-t pt-3 space-y-4">
      {error && (
        <p role="alert" className="rounded-lg bg-red-50 dark:bg-red-950/40 p-2.5 text-xs text-red-700">{error}</p>
      )}
      <div className="space-y-2 max-h-72 overflow-y-auto pr-1">
        {(attached ?? []).map((row) => (
          <div key={row.variant_id} className={`flex items-center gap-3 border rounded-xl p-2.5 ${isPinned(row) ? 'border-red-300 bg-red-50 dark:bg-red-900/20' : ''}`}>
            <div className="flex-1 min-w-0">
              <p className="text-sm line-clamp-1">{row.product_name}</p>
              <p className="text-xs text-gray-500">{row.variant_name} · {formatIDR(row.regular_price)} · stok {row.stock}</p>
            </div>
            <button type="button"
              onClick={() => togglePin.mutate({ variantId: row.variant_id, pinned: !isPinned(row) })}
              disabled={togglePin.isPending}
              className={`px-3 py-1.5 rounded-lg text-xs font-medium ${
                isPinned(row) ? 'bg-gray-900 dark:bg-gray-100 dark:text-gray-900 text-white' : 'bg-red-600 text-white'
              }`}
            >
              {isPinned(row) ? '📌 Unpin' : 'Pin ke layar'}
            </button>
          </div>
        ))}
        {(attached ?? []).length === 0 && <p className="text-xs text-gray-400">Belum ada produk dipilih.</p>}
      </div>

      <details className="bg-gray-50 dark:bg-gray-800 rounded-xl p-3">
        <summary className="cursor-pointer text-xs font-medium">+ Pilih produk dari katalog</summary>
        <div className="mt-2 space-y-2 max-h-56 overflow-y-auto">
          {(products ?? []).map((p) => (
            <div key={p.id} className="text-xs">
              <p className="font-medium mb-1">{p.name}</p>
              <div className="flex flex-wrap gap-1.5">
                {(p.variants ?? []).map((v) => (
                  <button type="button" key={v.id} onClick={() => pickVariant(p, v)} disabled={attachedIds.has(v.id)}
                    className="px-2 py-1 rounded-full border hover:border-amber-400 disabled:opacity-40">
                    {v.name}
                  </button>
                ))}
              </div>
            </div>
          ))}
        </div>
      </details>
    </div>
  )
}
