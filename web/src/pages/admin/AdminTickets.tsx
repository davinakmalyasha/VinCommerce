import { useState } from 'react'
import { useMutation, useQuery, useQueryClient } from '@tanstack/react-query'
import { api } from '../../lib/api'
import { formatDate } from '../../lib/format'

interface Ticket {
  id: string
  ticket_number: string
  subject: string
  category: string
  priority: string
  status: string
  last_message: string
  created_at: string
}

interface TicketMessage {
  id: number
  author_role: string
  author_name: string
  body: string
  is_internal: boolean
  created_at: string
}

const statusColors: Record<string, string> = {
  open: 'bg-amber-100 text-amber-700',
  in_progress: 'bg-blue-100 text-blue-700',
  resolved: 'bg-green-100 text-green-700',
  closed: 'bg-gray-200 text-gray-600',
}

export function AdminTickets() {
  const queryClient = useQueryClient()
  const [filter, setFilter] = useState('all')
  const [openId, setOpenId] = useState<string | null>(null)
  const [reply, setReply] = useState('')
  const [internal, setInternal] = useState(false)

  const { data } = useQuery({
    queryKey: ['admin-tickets', filter],
    queryFn: async () =>
      (await api.get<{ tickets: Ticket[] }>(`/support/tickets?status=${filter}`)).data.tickets,
  })

  const { data: detail } = useQuery({
    queryKey: ['admin-ticket', openId],
    queryFn: async () =>
      (await api.get<{ ticket: Ticket; messages: TicketMessage[] }>(`/tickets/${openId}`)).data,
    enabled: !!openId,
  })

  const sendReply = useMutation({
    mutationFn: async () =>
      api.post(`/tickets/${openId}/messages`, { body: reply, internal }),
    onSuccess: () => {
      setReply('')
      setInternal(false)
      queryClient.invalidateQueries({ queryKey: ['admin-ticket', openId] })
      queryClient.invalidateQueries({ queryKey: ['admin-tickets'] })
    },
  })

  const setStatus = useMutation({
    mutationFn: async (status: string) => api.post(`/support/tickets/${openId}/status`, { status }),
    onSuccess: () => {
      queryClient.invalidateQueries({ queryKey: ['admin-ticket', openId] })
      queryClient.invalidateQueries({ queryKey: ['admin-tickets'] })
    },
  })

  return (
    <div className="space-y-4">
      <div className="flex items-center justify-between">
        <h1 className="text-xl font-bold">Antrian Tiket Dukungan</h1>
        <select
          value={filter}
          onChange={(e) => setFilter(e.target.value)}
          className="px-3 py-2 border rounded-lg text-sm outline-none"
        >
          <option value="all">Semua</option>
          <option value="open">Open</option>
          <option value="in_progress">In Progress</option>
          <option value="resolved">Resolved</option>
          <option value="closed">Closed</option>
        </select>
      </div>

      <div className="grid grid-cols-1 lg:grid-cols-2 gap-4">
        <div className="space-y-2">
          {data?.length === 0 && <p className="text-gray-500 text-sm">Tidak ada tiket.</p>}
          {data?.map((t) => (
            <button type="button"
              key={t.id}
              onClick={() => setOpenId(t.id)}
              className={`w-full text-left bg-white border rounded-xl p-4 hover:border-amber-400 transition-colors ${
                openId === t.id ? 'border-amber-500 ring-1 ring-amber-200' : 'border-gray-200'
              }`}
            >
              <div className="flex items-center justify-between">
                <span className="font-mono text-xs text-gray-400">{t.ticket_number}</span>
                <span className={`px-2.5 py-0.5 rounded-full text-xs font-medium ${statusColors[t.status]}`}>
                  {t.status}
                </span>
              </div>
              <p className="font-medium text-sm mt-1">{t.subject}</p>
              <p className="text-xs text-gray-500 mt-1 line-clamp-1">{t.last_message}</p>
              <div className="flex items-center gap-2 mt-2 text-xs text-gray-400">
                <span className={`font-medium ${t.priority === 'urgent' ? 'text-red-600' : t.priority === 'high' ? 'text-orange-600' : ''}`}>
                  {t.priority.toUpperCase()}
                </span>
                <span>·</span>
                <span>{formatDate(t.created_at)}</span>
              </div>
            </button>
          ))}
        </div>

        <div className="bg-white border border-gray-200 rounded-xl p-5 h-fit">
          {!openId ? (
            <p className="text-gray-400 text-sm text-center py-12">Pilih tiket untuk membalas.</p>
          ) : (
            <div className="space-y-4">
              <div className="flex items-center justify-between">
                <div>
                  <p className="font-mono text-xs text-gray-400">{detail?.ticket.ticket_number}</p>
                  <h2 className="font-bold">{detail?.ticket.subject}</h2>
                </div>
                <select
                  value={detail?.ticket.status}
                  onChange={(e) => setStatus.mutate(e.target.value)}
                  className="px-3 py-2 border rounded-lg text-sm outline-none"
                >
                  <option value="open">Open</option>
                  <option value="in_progress">In Progress</option>
                  <option value="resolved">Resolved</option>
                  <option value="closed">Closed</option>
                </select>
              </div>

              <div className="space-y-2 max-h-72 overflow-y-auto">
                {detail?.messages.map((m) => (
                  <div
                    key={m.id}
                    className={`rounded-xl p-3 text-sm ${
                      m.is_internal
                        ? 'bg-gray-100 border border-dashed border-gray-300 text-gray-600'
                        : m.author_role === 'agent'
                          ? 'bg-amber-50 border border-amber-200'
                          : 'bg-white border border-gray-200'
                    }`}
                  >
                    <p className="text-xs font-medium mb-1">
                      {m.author_name} {m.is_internal && <span className="text-gray-400">· internal</span>}
                    </p>
                    <p className="whitespace-pre-line">{m.body}</p>
                  </div>
                ))}
              </div>

              <div className="space-y-2">
                <label className="flex items-center gap-2 text-xs text-gray-500">
                  <input type="checkbox" checked={internal} onChange={(e) => setInternal(e.target.checked)} />
                  Catatan internal (hanya terlihat staf)
                </label>
                <textarea
                  rows={3}
                  value={reply}
                  onChange={(e) => setReply(e.target.value)}
                  placeholder={internal ? 'Catatan internal...' : 'Balas ke pelanggan...'}
                  className="w-full px-3 py-2 border rounded-lg text-sm outline-none"
                />
                <button type="button"
                  onClick={() => sendReply.mutate()}
                  disabled={sendReply.isPending || !reply.trim()}
                  className="w-full py-2.5 rounded-lg bg-gray-900 text-white text-sm disabled:opacity-50"
                >
                  {internal ? 'Simpan Catatan' : 'Kirim Balasan'}
                </button>
              </div>
            </div>
          )}
        </div>
      </div>
    </div>
  )
}
