import { useMutation, useQuery, useQueryClient } from '@tanstack/react-query'
import { Link, Navigate } from 'react-router-dom'
import { api } from '../lib/api'
import { useSession } from '../stores/session'
import { formatDate } from '../lib/format'
import { notificationHref } from '../lib/notifications'

interface Notification {
  id: number
  type: string
  title: string
  body: string
  data: Record<string, unknown>
  read_at: string | null
  created_at: string
}

export function NotificationsPage() {
  const { user } = useSession()
  const queryClient = useQueryClient()

  const { data } = useQuery({
    queryKey: ['notifications-all'],
    queryFn: async () => (await api.get<{ notifications: Notification[] }>('/notifications?limit=50')).data.notifications,
    enabled: !!user,
  })

  const markAll = useMutation({
    mutationFn: async () => api.post('/notifications/read'),
    onSuccess: () => queryClient.invalidateQueries({ queryKey: ['notifications-all'] }),
  })

  if (!user) return <Navigate to="/login" replace />

  return (
    <div className="mx-auto max-w-3xl px-4 py-6">
      <div className="flex items-center justify-between mb-6">
        <h1 className="text-xl font-bold">Notifikasi</h1>
        <button onClick={() => markAll.mutate()} className="text-sm text-amber-600 hover:underline">
          Tandai semua dibaca
        </button>
      </div>
      <div className="space-y-2">
        {data?.length === 0 && <p className="text-gray-500 text-sm">Belum ada notifikasi.</p>}
        {data?.map((n) => {
          const href = notificationHref(n.data)
          const content = (
            <div
              className={`bg-white dark:bg-gray-900 border rounded-xl p-4 ${
                n.read_at ? 'border-gray-200 dark:border-gray-700' : 'border-amber-300 dark:border-amber-600'
              }`}
            >
              <div className="flex items-center justify-between">
                <p className="font-medium text-sm">{n.title}</p>
                {!n.read_at && <span className="w-2 h-2 rounded-full bg-amber-500" />}
              </div>
              <p className="text-sm text-gray-600 dark:text-gray-300 mt-1">{n.body}</p>
              <p className="text-xs text-gray-400 mt-1">{formatDate(n.created_at)}</p>
            </div>
          )
          return href ? (
            <Link key={n.id} to={href}>
              {content}
            </Link>
          ) : (
            <div key={n.id}>{content}</div>
          )
        })}
      </div>
    </div>
  )
}
