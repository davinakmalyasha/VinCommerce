import { useEffect, useRef, useState } from 'react'
import { useMutation, useQuery, useQueryClient } from '@tanstack/react-query'
import { Link } from 'react-router-dom'
import { api, getAccessToken } from '../lib/api'
import { formatIDR } from '../lib/format'
import { Seo } from '../components/Seo'

interface LiveSession {
  id: string
  title: string
  seller_name?: string
  youtube_video_id?: string
  thumbnail_url?: string
  status: 'scheduled' | 'live' | 'ended'
  viewer_peak: number
}

interface LiveProduct {
  variant_id: string
  product_name: string
  variant_name: string
  image_url?: string
  regular_price: number
  price_override?: number | null
  stock: number
}

interface ChatMsg {
  user: string
  body: string
}

export function LivePage() {
  const [selected, setSelected] = useState<string | null>(null)

  const { data: sessions } = useQuery({
    queryKey: ['live-sessions'],
    queryFn: async () => (await api.get<{ sessions: LiveSession[] }>('/live')).data.sessions,
    refetchInterval: 15_000,
  })

  const live = (sessions ?? []).filter((s) => s.status === 'live')
  const others = (sessions ?? []).filter((s) => s.status !== 'live')

  if (selected) return <LiveRoom sessionId={selected} onBack={() => setSelected(null)} />

  return (
    <>
      <Seo title="Live Shopping — VinCommerce" description="Belanja langsung dari siaran penjual." path="/live" />
      <div className="mx-auto max-w-7xl px-4 py-6 space-y-8">
        <div>
          <h1 className="text-2xl font-extrabold flex items-center gap-2">
            <span className="w-3 h-3 rounded-full bg-red-600 animate-pulse" /> Live Shopping
          </h1>
          <p className="text-sm text-gray-500 mt-1">Belanja langsung dari siaran — produk di-pin real-time oleh penjual.</p>
        </div>

        {!sessions && <p className="text-sm text-gray-500">Memuat...</p>}
        {sessions && sessions.length === 0 && (
          <p className="text-sm text-gray-500">Belum ada siaran. Penjual bisa memulai dari Seller Center → Live.</p>
        )}

        {live.length > 0 && (
          <section>
            <h2 className="font-bold text-red-600 mb-3">🔴 Sedang Live</h2>
            <SessionGrid sessions={live} onPick={setSelected} />
          </section>
        )}
        {others.length > 0 && (
          <section>
            <h2 className="font-bold text-gray-500 mb-3">Terjadwal &amp; Selesai</h2>
            <SessionGrid sessions={others} onPick={setSelected} />
          </section>
        )}
      </div>
    </>
  )
}

function SessionGrid({ sessions, onPick }: { sessions: LiveSession[]; onPick: (id: string) => void }) {
  return (
    <div className="grid grid-cols-1 md:grid-cols-3 gap-4">
      {sessions.map((s) => (
        <button key={s.id} onClick={() => onPick(s.id)} className="text-left bg-white dark:bg-gray-900 border border-gray-200 dark:border-gray-700 rounded-2xl overflow-hidden hover:shadow-lg transition-shadow">
          <div className="aspect-video bg-gray-900 relative">
            {s.thumbnail_url ? (
              <img src={s.thumbnail_url} alt="" className="w-full h-full object-cover opacity-80" />
            ) : (
              <div className="w-full h-full flex items-center justify-center text-4xl">📺</div>
            )}
            <span className={`absolute top-2 left-2 px-2 py-0.5 rounded-full text-xs font-bold ${
              s.status === 'live' ? 'bg-red-600 text-white animate-pulse' : 'bg-gray-700 text-white'
            }`}>
              {s.status === 'live' ? 'LIVE' : s.status.toUpperCase()}
            </span>
          </div>
          <div className="p-4">
            <p className="font-semibold line-clamp-1">{s.title}</p>
            <p className="text-xs text-gray-500 mt-1">{s.seller_name} · 👁 {s.viewer_peak} penonton</p>
          </div>
        </button>
      ))}
    </div>
  )
}

function LiveRoom({ sessionId, onBack }: { sessionId: string; onBack: () => void }) {
  const queryClient = useQueryClient()
  const [chat, setChat] = useState<ChatMsg[]>([])
  const [input, setInput] = useState('')
  const chatRef = useRef<HTMLDivElement>(null)

  const { data: session } = useQuery({
    queryKey: ['live', sessionId],
    queryFn: async () => (await api.get<{ session: LiveSession }>(`/live/${sessionId}`)).data.session,
  })

  const { data: pinned } = useQuery({
    queryKey: ['live-pinned', sessionId],
    queryFn: async () => (await api.get<{ pinned: LiveProduct[] }>(`/live/${sessionId}/pinned`)).data.pinned,
    refetchInterval: 4000,
  })

  // Realtime channel via authenticated fetch-stream (EventSource cannot send headers).
  useEffect(() => {
    let cancelled = false
    const controller = new AbortController()
    const run = async () => {
      try {
        const res = await fetch(`/api/v1/stream/live/${sessionId}`, {
          headers: { Authorization: `Bearer ${getAccessToken()}` },
          signal: controller.signal,
        })
        if (!res.ok || !res.body) return
        const reader = res.body.getReader()
        const decoder = new TextDecoder()
        let buffer = ''
        for (;;) {
          const { done, value } = await reader.read()
          if (done || cancelled) return
          buffer += decoder.decode(value, { stream: true })
          const parts = buffer.split('\n\n')
          buffer = parts.pop() ?? ''
          for (const part of parts) {
            const dataLine = part.split('\n').find((l) => l.startsWith('data:'))
            if (!dataLine) continue
            try {
              const ev = JSON.parse(dataLine.slice(5).trim())
              if (ev.type === 'chat') {
                setChat((c) => [...c.slice(-40), { user: ev.user, body: ev.body }])
                queryClient.invalidateQueries({ queryKey: ['live-pinned', sessionId] })
              }
              if (ev.type === 'session.status') {
                queryClient.invalidateQueries({ queryKey: ['live-sessions'] })
              }
            } catch {
              // ignore malformed frames
            }
          }
        }
      } catch {
        // aborted or network error
      }
    }
    run()
    return () => {
      cancelled = true
      controller.abort()
    }
  }, [sessionId, queryClient])

  useEffect(() => {
    chatRef.current?.scrollTo({ top: chatRef.current.scrollHeight })
  }, [chat.length])

  const sendChat = useMutation({
    mutationFn: async () => api.post(`/live/${sessionId}/chat`, { body: input }),
    onSuccess: () => setInput(''),
  })

  const addToCart = useMutation({
    mutationFn: async (variantId: string) =>
      api.post('/cart/items', { variant_id: variantId, quantity: 1 }),
    onSuccess: () => queryClient.invalidateQueries({ queryKey: ['cart'] }),
  })

  return (
    <>
      <Seo title={session?.title ?? 'Live'} />
      <div className="mx-auto max-w-6xl px-4 py-6 space-y-4">
        <Link to="/live" onClick={onBack} className="text-sm text-gray-400 hover:text-gray-700">← Semua siaran</Link>

        <div className="grid grid-cols-1 lg:grid-cols-3 gap-4">
          <div className="lg:col-span-2 space-y-3">
            <div className="aspect-video bg-black rounded-2xl overflow-hidden">
              {session?.status === 'live' && session.youtube_video_id ? (
                <iframe
                  title={session.title}
                  src={`https://www.youtube.com/embed/${session.youtube_video_id}?autoplay=1`}
                  allow="accelerometer; autoplay; encrypted-media; picture-in-picture"
                  allowFullScreen
                  className="w-full h-full"
                />
              ) : (
                <div className="w-full h-full flex flex-col items-center justify-center text-gray-400">
                  <span className="text-4xl mb-2">{session?.status === 'ended' ? '🏁' : '⏰'}</span>
                  <p className="text-sm">{session?.status === 'ended' ? 'Siaran sudah berakhir' : 'Siaran belum dimulai'}</p>
                </div>
              )}
            </div>
            <div>
              <div className="flex items-center gap-2 flex-wrap">
                {session?.status === 'live' && (
                  <span className="px-2 py-0.5 rounded-full bg-red-600 text-white text-xs font-bold animate-pulse">● LIVE</span>
                )}
                <h1 className="text-lg font-bold">{session?.title}</h1>
              </div>
              <p className="text-xs text-gray-500 mt-1">
                {session?.seller_name} · 👁 {session?.viewer_peak ?? 0} penonton
              </p>
            </div>
          </div>

          <div className="space-y-4">
            {/* pinned products shelf */}
            <div className="bg-white dark:bg-gray-900 border border-gray-200 dark:border-gray-700 rounded-xl p-4">
              <h2 className="font-bold text-sm mb-2">📌 Dipromosikan sekarang</h2>
              {(pinned ?? []).length === 0 && <p className="text-xs text-gray-400">Belum ada produk di-pin.</p>}
              <div className="space-y-2">
                {(pinned ?? []).map((p) => {
                  const price = p.price_override ?? p.regular_price
                  const hasOverride = p.price_override != null && p.price_override < p.regular_price
                  return (
                    <div key={p.variant_id} className="flex items-center gap-2 border rounded-xl p-2">
                      {p.image_url ? (
                        <img src={p.image_url} alt="" className="w-12 h-12 rounded-lg object-cover" />
                      ) : (
                        <div className="w-12 h-12 rounded-lg bg-gray-100 dark:bg-gray-800" />
                      )}
                      <div className="flex-1 min-w-0">
                        <p className="text-xs line-clamp-1">{p.product_name}</p>
                        <p className="text-sm font-bold text-amber-600">
                          {formatIDR(price)}
                          {hasOverride && <span className="ml-1 text-xs text-gray-400 line-through">{formatIDR(p.regular_price)}</span>}
                        </p>
                      </div>
                      <button
                        onClick={() => addToCart.mutate(p.variant_id)}
                        disabled={addToCart.isPending || p.stock === 0}
                        className="px-3 py-1.5 rounded-lg bg-amber-500 text-white text-xs font-medium disabled:opacity-50"
                      >
                        + Keranjang
                      </button>
                    </div>
                  )
                })}
              </div>
            </div>

            {/* live chat */}
            <div className="bg-white dark:bg-gray-900 border border-gray-200 dark:border-gray-700 rounded-xl p-4 flex flex-col h-80">
              <h2 className="font-bold text-sm mb-2">💬 Chat</h2>
              <div ref={chatRef} className="flex-1 overflow-y-auto space-y-1.5 text-sm pr-1">
                {chat.map((m, i) => (
                  <p key={i}><b className="text-amber-600">{m.user}:</b> {m.body}</p>
                ))}
                {chat.length === 0 && <p className="text-xs text-gray-400">Sapa penjual dan sesama penonton!</p>}
              </div>
              <form
                onSubmit={(e) => {
                  e.preventDefault()
                  if (input.trim()) sendChat.mutate()
                }}
                className="flex gap-2 pt-2"
              >
                <input
                  value={input}
                  onChange={(e) => setInput(e.target.value)}
                  placeholder="Tulis pesan..."
                  className="flex-1 px-3 py-2 border rounded-lg text-sm outline-none"
                />
                <button
                  type="submit"
                  disabled={!input.trim() || sendChat.isPending}
                  className="px-4 py-2 rounded-lg bg-blue-600 text-white text-sm disabled:opacity-50"
                >
                  Kirim
                </button>
              </form>
            </div>
          </div>
        </div>
      </div>
    </>
  )
}
