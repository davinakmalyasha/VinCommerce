import { useQuery } from '@tanstack/react-query'
import { Link, Navigate } from 'react-router-dom'
import { api } from '../lib/api'
import { useSession } from '../stores/session'
import { formatDate } from '../lib/format'

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

const statusStyle: Record<string, string> = {
  open: 'bg-amber-100 text-amber-700',
  in_progress: 'bg-blue-100 text-blue-700',
  resolved: 'bg-green-100 text-green-700',
  closed: 'bg-gray-200 text-gray-600',
}

const priorityStyle: Record<string, string> = {
  low: 'text-gray-400',
  normal: 'text-gray-600',
  high: 'text-orange-600',
  urgent: 'text-red-600',
}

export function MyTicketsPage() {
  const { user } = useSession()

  const { data } = useQuery({
    queryKey: ['my-tickets'],
    queryFn: async () => (await api.get<{ tickets: Ticket[] }>('/tickets')).data.tickets,
    enabled: !!user,
  })

  if (!user) return <Navigate to="/login" replace />

  return (
    <div className="mx-auto max-w-4xl px-4 py-6">
      <div className="flex items-center justify-between mb-6">
        <h1 className="text-xl font-bold">Tiket Dukungan</h1>
        <Link to="/contact" className="px-4 py-2 rounded-lg bg-amber-500 text-white text-sm font-medium hover:bg-amber-600">
          + Tiket Baru
        </Link>
      </div>
      <div className="space-y-3">
        {data?.length === 0 && <p className="text-gray-500 text-sm">Belum ada tiket.</p>}
        {data?.map((t) => (
          <Link
            key={t.id}
            to={`/account/tickets/${t.id}`}
            className="block bg-white border border-gray-200 rounded-xl p-5 hover:border-amber-400 hover:shadow-md transition-all"
          >
            <div className="flex items-center justify-between">
              <div className="flex items-center gap-3">
                <span className="font-mono text-xs text-gray-400">{t.ticket_number}</span>
                <span className={`px-2.5 py-0.5 rounded-full text-xs font-medium ${statusStyle[t.status]}`}>{t.status}</span>
                <span className={`text-xs font-medium ${priorityStyle[t.priority]}`}>
                  {t.priority.toUpperCase()}
                </span>
              </div>
              <span className="text-xs text-gray-400">{formatDate(t.created_at)}</span>
            </div>
            <h2 className="font-semibold mt-2">{t.subject}</h2>
            {t.last_message && <p className="text-sm text-gray-500 mt-1 line-clamp-1">{t.last_message}</p>}
          </Link>
        ))}
      </div>
    </div>
  )
}
