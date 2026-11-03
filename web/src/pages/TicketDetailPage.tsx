import { useState } from 'react'
import { useMutation, useQuery, useQueryClient } from '@tanstack/react-query'
import { useParams, Link } from 'react-router-dom'
import { api } from '../lib/api'
import { formatDate } from '../lib/format'
import { FileUpload } from '../components/FileUpload'

interface TicketMessage {
  id: number
  author_role: string
  author_name: string
  body: string
  attachments?: string[]
  is_internal: boolean
  created_at: string
}

interface Ticket {
  id: string
  ticket_number: string
  subject: string
  category: string
  priority: string
  status: string
  rating?: number | null
  created_at: string
}

export function TicketDetailPage() {
  const { id } = useParams()
  const queryClient = useQueryClient()
  const [body, setBody] = useState('')
  const [attachments, setAttachments] = useState<string[]>([])

  const { data } = useQuery({
    queryKey: ['ticket', id],
    queryFn: async () => (await api.get<{ ticket: Ticket; messages: TicketMessage[] }>(`/tickets/${id}`)).data,
  })

  const reply = useMutation({
    mutationFn: async () =>
      api.post(`/tickets/${id}/messages`, { body, attachments: attachments.length > 0 ? attachments : undefined }),
    onSuccess: () => {
      setBody('')
      setAttachments([])
      queryClient.invalidateQueries({ queryKey: ['ticket', id] })
      queryClient.invalidateQueries({ queryKey: ['my-tickets'] })
    },
  })

  const rate = useMutation({
    mutationFn: async (rating: number) => api.post(`/tickets/${id}/rating`, { rating }),
    onSuccess: () => {
      queryClient.invalidateQueries({ queryKey: ['ticket', id] })
    },
  })

  if (!data) return <div className="mx-auto max-w-3xl px-4 py-16 text-center text-gray-500">Memuat...</div>

  const { ticket, messages } = data

  return (
    <div className="mx-auto max-w-3xl px-4 py-6">
      <Link to="/account/tickets" className="text-sm text-gray-400 hover:text-gray-700">
        ← Tiket Saya
      </Link>
      <div className="flex items-center justify-between mt-3 mb-6">
        <div>
          <p className="font-mono text-xs text-gray-400">{ticket.ticket_number}</p>
          <h1 className="text-xl font-bold">{ticket.subject}</h1>
        </div>
        <span className="px-3 py-1.5 rounded-full bg-amber-100 text-amber-700 text-sm font-medium">{ticket.status}</span>
      </div>

      <div className="space-y-3 mb-6">
        {messages.map((m) => (
          <div
            key={m.id}
            className={`rounded-xl p-4 border ${
              m.author_role === 'customer'
                ? 'bg-white border-gray-200'
                : m.is_internal
                  ? 'bg-gray-100 border-dashed border-gray-300'
                  : 'bg-amber-50 border-amber-200'
            }`}
          >
            <div className="flex items-center justify-between mb-1">
              <p className="text-sm font-medium">
                {m.author_name}
                {m.is_internal && <span className="ml-2 text-xs text-gray-500">(catatan internal)</span>}
              </p>
              <span className="text-xs text-gray-400">{formatDate(m.created_at)}</span>
            </div>
            <p className="text-sm text-gray-700 whitespace-pre-line">{m.body}</p>
            {(m.attachments ?? []).length > 0 && (
              <div className="flex gap-2 mt-2 flex-wrap">
                {(m.attachments ?? []).map((url) => (
                  <a key={url} href={url} target="_blank" rel="noreferrer">
                    <img src={url} alt="Lampiran tiket" className="w-20 h-20 rounded-lg object-cover border border-gray-200" />
                  </a>
                ))}
              </div>
            )}
          </div>
        ))}
      </div>

      {ticket.status !== 'closed' && (
        <div className="bg-white border border-gray-200 rounded-xl p-5">
          <label htmlFor="ticket-reply" className="sr-only">Balasan untuk tiket</label>
          <textarea
            id="ticket-reply"
            rows={3}
            value={body}
            onChange={(e) => setBody(e.target.value)}
            placeholder="Tulis balasan..."
            className="w-full px-4 py-3 border rounded-xl text-sm outline-none focus:border-amber-400"
          />
          <div className="mt-2 space-y-2">
            <span className="text-xs text-gray-500 block">Lampiran foto (opsional, maks. 3)</span>
            {attachments.map((url, i) => (
              <FileUpload
                key={url}
                id={`ticket-attachment-${i}`}
                value={url}
                label="Tambah foto"
                onChange={(next) =>
                  setAttachments((prev) => (next ? prev.map((u, j) => (j === i ? next : u)) : prev.filter((_, j) => j !== i)))
                }
              />
            ))}
            {attachments.length < 3 && (
              <FileUpload
                id="ticket-attachment-new"
                value=""
                label="Tambah foto"
                onChange={(next) => next && setAttachments((prev) => [...prev, next])}
              />
            )}
          </div>
          <button type="button"
            onClick={() => reply.mutate()}
            disabled={reply.isPending || !body.trim()}
            className="mt-3 px-6 py-2.5 rounded-xl bg-amber-500 text-white text-sm font-medium hover:bg-amber-600 disabled:opacity-50"
          >
            {reply.isPending ? 'Mengirim...' : 'Balas'}
          </button>
        </div>
      )}

      {(ticket.status === 'resolved' || ticket.status === 'closed') && (
        <div className="bg-white border border-gray-200 rounded-xl p-5 mt-4">
          <p className="text-sm font-medium mb-2">Bagaimana penanganan tiket ini?</p>
          {ticket.rating ? (
            <p className="text-sm text-gray-600">
              {'★'.repeat(ticket.rating)}{'☆'.repeat(5 - ticket.rating)} Terima kasih atas penilaianmu!
            </p>
          ) : (
            <div className="flex gap-1.5">
              {[1, 2, 3, 4, 5].map((s) => (
                <button
                  key={s}
                  type="button"
                  onClick={() => rate.mutate(s)}
                  disabled={rate.isPending}
                  aria-label={`Nilai ${s} dari 5`}
                  className="text-2xl text-amber-400 hover:scale-110 disabled:opacity-50"
                >
                  ★
                </button>
              ))}
            </div>
          )}
        </div>
      )}
    </div>
  )
}
