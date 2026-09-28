import { useEffect, useRef, useState } from 'react'
import { useMutation, useQuery, useQueryClient } from '@tanstack/react-query'
import { api } from '../lib/api'
import { formatDate } from '../lib/format'

interface ChatMessage {
  id: number
  sender_role: string
  body: string
  created_at: string
}

// OrderChat is the buyer↔seller conversation scoped to one order.
// Sellers reach the same session through the participant check; buyers get a
// floating panel on the order detail page.
export function OrderChat({ orderId, status }: { orderId: string; status: string }) {
  const queryClient = useQueryClient()
  const [open, setOpen] = useState(false)
  const [body, setBody] = useState('')
  const bottomRef = useRef<HTMLDivElement>(null)

  const chatable = ['paid', 'packed', 'shipped', 'delivered', 'completed'].includes(status)

  const { data: existing } = useQuery({
    queryKey: ['order-chat', orderId],
    queryFn: async () => (await api.get<{ session: { id: string } | null }>(`/chat/orders/${orderId}`)).data.session,
    enabled: chatable,
    retry: false,
  })

  const openChat = useMutation({
    mutationFn: async () => (await api.post<{ session: { id: string } }>(`/chat/orders/${orderId}`)).data.session,
    onSuccess: () => {
      queryClient.invalidateQueries({ queryKey: ['order-chat', orderId] })
      setOpen(true)
    },
    onError: (e: Error) => setError(e.message || 'Gagal membuka percakapan'),
  })
  const [error, setError] = useState('')

  const { data: thread } = useQuery({
    queryKey: ['order-chat-messages', existing?.id],
    queryFn: async () =>
      (await api.get<{ messages: ChatMessage[] }>(`/chat/sessions/${existing!.id}`)).data.messages,
    enabled: !!existing && open,
    refetchInterval: 4_000, // seller replies appear within seconds
  })

  const send = useMutation({
    mutationFn: async () => api.post(`/chat/sessions/${existing!.id}/messages`, { body }),
    onSuccess: () => {
      setBody('')
      queryClient.invalidateQueries({ queryKey: ['order-chat-messages', existing?.id] })
    },
    onError: (e: Error) => setError(e.message || 'Gagal mengirim pesan'),
  })

  useEffect(() => {
    if (open) bottomRef.current?.scrollTo({ top: bottomRef.current.scrollHeight })
  }, [thread?.length, open])

  if (!chatable) return null

  return (
    <div className="bg-white dark:bg-gray-900 border border-gray-200 dark:border-gray-700 rounded-xl p-4">
      <button type="button" onClick={() => setOpen(!open)} className="w-full flex items-center justify-between text-left">
        <span className="font-bold text-sm">💬 Chat dengan Penjual</span>
        <span className="text-xs text-gray-400">{open ? 'tutup ▲' : 'buka ▼'}</span>
      </button>

      {open && (
        <div className="mt-3">
          {error && (
            <p role="alert" className="mb-2 text-xs text-red-600 bg-red-50 dark:bg-red-950/40 rounded-lg p-2">
              {error}
            </p>
          )}
          {!existing ? (
            <button
              type="button"
              onClick={() => openChat.mutate()}
              disabled={openChat.isPending}
              className="px-4 py-2 rounded-lg bg-blue-600 text-white text-sm font-medium disabled:opacity-50"
            >
              {openChat.isPending ? 'Membuka...' : 'Mulai percakapan'}
            </button>
          ) : (
            <>
              <div ref={bottomRef} className="max-h-64 overflow-y-auto space-y-1.5 text-sm pr-1">
                {(thread ?? []).map((m) => (
                  <div key={m.id} className={m.sender_role === 'customer' ? 'text-right' : ''}>
                    <p
                      className={`inline-block max-w-[85%] text-left px-3 py-1.5 rounded-xl ${
                        m.sender_role === 'customer'
                          ? 'bg-blue-600 text-white'
                          : 'bg-gray-100 dark:bg-gray-800'
                      }`}
                    >
                      {m.body}
                    </p>
                    <p className="text-[10px] text-gray-400 mt-0.5">{formatDate(m.created_at)}</p>
                  </div>
                ))}
                {(thread ?? []).length === 0 && (
                  <p className="text-xs text-gray-400">Tanya apa saja soal pesanan ini.</p>
                )}
              </div>
              <form
                onSubmit={(e) => {
                  e.preventDefault()
                  if (body.trim()) send.mutate()
                }}
                className="flex gap-2 mt-2"
              >
                <label htmlFor={`order-chat-input-${orderId}`} className="sr-only">Pesan</label>
                <input
                  id={`order-chat-input-${orderId}`}
                  value={body}
                  onChange={(e) => setBody(e.target.value)}
                  placeholder="Tulis pesan..."
                  maxLength={2000}
                  className="flex-1 min-w-0 px-3 py-2 border rounded-lg text-sm outline-none focus:border-amber-400"
                />
                <button
                  type="submit"
                  disabled={!body.trim() || send.isPending}
                  className="px-4 py-2 rounded-lg bg-blue-600 text-white text-sm disabled:opacity-50"
                >
                  Kirim
                </button>
              </form>
            </>
          )}
        </div>
      )}
    </div>
  )
}
