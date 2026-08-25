import { useState } from 'react'
import { useMutation, useQuery, useQueryClient } from '@tanstack/react-query'
import { api } from '../../lib/api'
import { formatDate } from '../../lib/format'

interface Ticket {
  id: string
  ticket_number: string
  user_id: string
  subject: string
  category: string
  priority: string
  status: string
  assigned_to?: string | null
  created_at: string
  updated_at: string
  last_message?: string
}

interface ChatSession {
  id: string
  user_id: string
  agent_id?: string | null
  status: string
  source: string
  created_at: string
  user_name?: string
}

interface ChatMessage {
  id: number
  session_id: string
  sender_role: string
  sender_id?: string | null
  body: string
  created_at: string
}

const STATUS_COLORS: Record<string, string> = {
  open: 'bg-blue-100 text-blue-700',
  in_progress: 'bg-amber-100 text-amber-700',
  resolved: 'bg-green-100 text-green-700',
  closed: 'bg-gray-100 text-gray-500',
}

export function SupportConsole() {
  const [tab, setTab] = useState<'tickets' | 'chat'>('tickets')
  const tabs: Array<{ key: 'tickets' | 'chat'; label: string }> = [
    { key: 'tickets', label: '🎫 Tiket' },
    { key: 'chat', label: '💬 Live Chat' },
  ]

  return (
    <div className="mx-auto max-w-6xl px-4 py-6 space-y-5">
      <div>
        <h1 className="text-xl font-bold">Support Console</h1>
        <p className="text-sm text-gray-500">Antrian tiket & sesi live chat pelanggan.</p>
      </div>

      <div className="flex gap-2 border-b border-gray-200 dark:border-gray-700">
        {tabs.map((t) => (
          <button
            key={t.key}
            type="button"
            onClick={() => setTab(t.key)}
            className={`px-4 py-2 text-sm font-medium rounded-t-lg ${
              tab === t.key ? 'bg-amber-500 text-white' : 'text-gray-500 hover:text-gray-800'
            }`}
          >
            {t.label}
          </button>
        ))}
      </div>

      {tab === 'tickets' ? <TicketQueue /> : <ChatQueue />}
    </div>
  )
}

function TicketQueue() {
  const queryClient = useQueryClient()
  const [openId, setOpenId] = useState<string | null>(null)

  const { data: tickets } = useQuery({
    queryKey: ['support-tickets'],
    queryFn: async () => (await api.get<{ tickets: Ticket[] }>('/support/tickets')).data.tickets,
    refetchInterval: 15_000,
  })

  const setStatus = useMutation({
    mutationFn: async ({ id, status }: { id: string; status: string }) =>
      api.post(`/support/tickets/${id}/status`, { status }),
    onSuccess: () => queryClient.invalidateQueries({ queryKey: ['support-tickets'] }),
    onError: () => alert('Gagal mengubah status tiket'),
  })

  const assignToMe = useMutation({
    mutationFn: async (id: string) => api.post(`/support/tickets/${id}/assign`, { agent_id: null }),
    onSuccess: () => queryClient.invalidateQueries({ queryKey: ['support-tickets'] }),
    onError: () => alert('Gagal mengambil tiket'),
  })

  if (!tickets?.length) {
    return <p className="text-sm text-gray-500 py-10 text-center">Tidak ada tiket terbuka. 🎉</p>
  }

  return (
    <div className="space-y-3">
      {tickets.map((t) => (
        <div key={t.id} className="bg-white dark:bg-gray-900 border border-gray-200 dark:border-gray-700 rounded-xl p-4">
          <div className="flex items-start justify-between gap-3">
            <div className="min-w-0">
              <p className="font-medium text-sm">
                <span className="font-mono text-xs text-gray-400 mr-2">{t.ticket_number}</span>
                {t.subject}
              </p>
              <p className="text-xs text-gray-500 mt-0.5 line-clamp-1">{t.last_message}</p>
              <p className="text-xs text-gray-400 mt-1">
                {t.category} · prioritas {t.priority} · {formatDate(t.created_at)}
                {t.assigned_to && <> · ditangani ✓</>}
              </p>
            </div>
            <div className="flex items-center gap-2 shrink-0">
              <span className={`px-2 py-0.5 rounded-full text-xs ${STATUS_COLORS[t.status] ?? ''}`}>
                {t.status.replace('_', ' ')}
              </span>
              {!t.assigned_to && (
                <button
                  type="button"
                  onClick={() => assignToMe.mutate(t.id)}
                  disabled={assignToMe.isPending}
                  className="px-3 py-1.5 rounded-lg bg-gray-900 text-white text-xs disabled:opacity-50"
                >
                  Ambil
                </button>
              )}
              <select
                value={t.status}
                onChange={(e) => setStatus.mutate({ id: t.id, status: e.target.value })}
                className="border rounded-lg px-2 py-1.5 text-xs bg-transparent"
              >
                {['open', 'in_progress', 'resolved', 'closed'].map((s) => (
                  <option key={s} value={s}>{s}</option>
                ))}
              </select>
              <button
                type="button"
                onClick={() => setOpenId(openId === t.id ? null : t.id)}
                className="px-3 py-1.5 rounded-lg border border-gray-300 text-xs hover:bg-gray-50"
              >
                {openId === t.id ? 'Tutup' : 'Balas'}
              </button>
            </div>
          </div>
          {openId === t.id && <TicketReply ticketId={t.id} />}
        </div>
      ))}
    </div>
  )
}

function TicketReply({ ticketId }: { ticketId: string }) {
  const queryClient = useQueryClient()
  const [body, setBody] = useState('')
  const [internal, setInternal] = useState(false)

  const { data } = useQuery({
    queryKey: ['ticket-detail', ticketId],
    queryFn: async () => (await api.get<{ messages: ChatMessage[] }>(`/tickets/${ticketId}`)).data,
  })

  const send = useMutation({
    mutationFn: async () => api.post(`/tickets/${ticketId}/messages`, { body, is_internal: internal }),
    onSuccess: () => {
      setBody('')
      queryClient.invalidateQueries({ queryKey: ['ticket-detail', ticketId] })
    },
    onError: () => alert('Gagal mengirim balasan'),
  })

  return (
    <div className="mt-3 pt-3 border-t border-gray-100 dark:border-gray-800 space-y-2">
      {(data?.messages ?? []).map((m) => (
        <div key={m.id} className={`rounded-lg p-2.5 text-sm ${m.sender_role === 'customer' ? 'bg-gray-50 dark:bg-gray-800' : 'bg-amber-50 dark:bg-amber-950/40'}`}>
          <p>{m.body}</p>
          <p className="text-[11px] text-gray-400 mt-1">
            {m.sender_role} · {formatDate(m.created_at)}
          </p>
        </div>
      ))}
      <textarea
        value={body}
        onChange={(e) => setBody(e.target.value)}
        rows={2}
        placeholder="Tulis balasan..."
        className="w-full px-3 py-2 border rounded-lg text-sm outline-none focus:border-amber-400"
      />
      <div className="flex items-center justify-between">
        <label className="flex items-center gap-1.5 text-xs text-gray-500">
          <input type="checkbox" checked={internal} onChange={(e) => setInternal(e.target.checked)} className="accent-amber-500" />
          Catatan internal (tidak dikirim ke pelanggan)
        </label>
        <button
          type="button"
          onClick={() => body.trim() && send.mutate()}
          disabled={send.isPending || !body.trim()}
          className="px-4 py-2 rounded-lg bg-amber-500 text-white text-sm font-medium disabled:opacity-50"
        >
          {send.isPending ? 'Mengirim...' : 'Kirim'}
        </button>
      </div>
    </div>
  )
}

function ChatQueue() {
  const queryClient = useQueryClient()
  const [openId, setOpenId] = useState<string | null>(null)

  const { data: sessions } = useQuery({
    queryKey: ['chat-queue'],
    queryFn: async () => (await api.get<{ sessions: ChatSession[] }>('/support/chat/queue')).data.sessions,
    refetchInterval: 8_000,
  })

  const claim = useMutation({
    mutationFn: async (id: string) => api.post(`/support/chat/sessions/${id}/claim`),
    onSuccess: () => queryClient.invalidateQueries({ queryKey: ['chat-queue'] }),
    onError: () => alert('Gagal mengambil sesi chat'),
  })

  return (
    <div className="space-y-3">
      {!sessions?.length && (
        <p className="text-sm text-gray-500 py-10 text-center">Belum ada sesi chat terbuka.</p>
      )}
      {sessions?.map((s) => (
        <div key={s.id} className="bg-white dark:bg-gray-900 border border-gray-200 dark:border-gray-700 rounded-xl p-4 flex items-center justify-between gap-3">
          <div className="min-w-0">
            <p className="font-medium text-sm truncate">{s.user_name ?? s.user_id}</p>
            <p className="text-xs text-gray-400 mt-0.5">
              sumber: {s.source} · mulai {formatDate(s.created_at)}
              {s.agent_id && <> · diambil ✓</>}
            </p>
          </div>
          <div className="flex gap-2 shrink-0">
            {!s.agent_id && (
              <button
                type="button"
                onClick={() => claim.mutate(s.id)}
                disabled={claim.isPending}
                className="px-3 py-1.5 rounded-lg bg-gray-900 text-white text-xs disabled:opacity-50"
              >
                Klaim
              </button>
            )}
            <button
              type="button"
              onClick={() => setOpenId(openId === s.id ? null : s.id)}
              className="px-3 py-1.5 rounded-lg border border-gray-300 text-xs hover:bg-gray-50"
            >
              {openId === s.id ? 'Tutup' : 'Buka'}
            </button>
          </div>
        </div>
      ))}
      {openId && <StaffChatRoom sessionId={openId} />}
    </div>
  )
}

function StaffChatRoom({ sessionId }: { sessionId: string }) {
  const queryClient = useQueryClient()
  const [body, setBody] = useState('')

  const { data } = useQuery({
    // Poll instead of SSE: the staff console is a low-traffic surface and
    // polling keeps it simple; customer replies appear within 3s.
    queryKey: ['staff-chat', sessionId],
    queryFn: async () => (await api.get<{ messages: ChatMessage[] }>(`/chat/sessions/${sessionId}`)).data,
    refetchInterval: 3_000,
  })

  const send = useMutation({
    mutationFn: async () => api.post(`/support/chat/sessions/${sessionId}/messages`, { body }),
    onSuccess: () => {
      setBody('')
      queryClient.invalidateQueries({ queryKey: ['staff-chat', sessionId] })
    },
    onError: () => alert('Gagal mengirim pesan'),
  })

  return (
    <div className="bg-white dark:bg-gray-900 border border-gray-200 dark:border-gray-700 rounded-xl p-4 space-y-3">
      <h3 className="font-bold text-sm">💬 Sesi {sessionId.slice(0, 8)}</h3>
      <div className="max-h-72 overflow-y-auto space-y-1.5 text-sm">
        {(data?.messages ?? []).map((m) => (
          <p key={m.id}>
            <b className={m.sender_role === 'agent' ? 'text-amber-600' : 'text-blue-600'}>
              {m.sender_role}:
            </b>{' '}
            {m.body}
          </p>
        ))}
      </div>
      <form
        onSubmit={(e) => {
          e.preventDefault()
          if (body.trim()) send.mutate()
        }}
        className="flex gap-2"
      >
        <input
          value={body}
          onChange={(e) => setBody(e.target.value)}
          placeholder="Balas sebagai agen..."
          className="flex-1 px-3 py-2 border rounded-lg text-sm outline-none focus:border-amber-400"
        />
        <button
          type="submit"
          disabled={!body.trim() || send.isPending}
          className="px-4 py-2 rounded-lg bg-blue-600 text-white text-sm disabled:opacity-50"
        >
          Kirim
        </button>
      </form>
    </div>
  )
}
