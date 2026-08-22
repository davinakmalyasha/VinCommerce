import { useEffect, useState } from 'react'
import { useMutation, useQuery, useQueryClient } from '@tanstack/react-query'
import { Link } from 'react-router-dom'
import { api, getAccessToken } from '../../lib/api'
import { useSession } from '../../stores/session'
import { formatDate } from '../../lib/format'
import { notificationHref } from '../../lib/notifications'

interface Notification {
  id: number
  type: string
  title: string
  body: string
  data: Record<string, unknown>
  read_at: string | null
  created_at: string
}

export function NotificationBell() {
  const { user } = useSession()
  const queryClient = useQueryClient()
  const [open, setOpen] = useState(false)
  const [live, setLive] = useState(0)

  const { data: unread } = useQuery({
    queryKey: ['notif-unread'],
    queryFn: async () => (await api.get<{ unread: number }>('/notifications/unread-count')).data.unread,
    enabled: !!user,
    refetchInterval: 30_000,
  })

  const { data: notifications } = useQuery({
    queryKey: ['notifications'],
    queryFn: async () => (await api.get<{ notifications: Notification[] }>('/notifications?limit=15')).data.notifications,
    enabled: !!user && open,
  })

  const markRead = useMutation({
    mutationFn: async () => api.post('/notifications/read'),
    onSuccess: () => {
      queryClient.invalidateQueries({ queryKey: ['notif-unread'] })
      queryClient.invalidateQueries({ queryKey: ['notifications'] })
    },
  })

  useEffect(() => {
    if (!user) return
    let cancelled = false

    const connect = async () => {
      const res = await fetch('/api/v1/stream/orders', {
        headers: { Authorization: `Bearer ${getAccessToken()}` },
      })
      if (!res.ok || !res.body) return
      const reader = res.body.getReader()
      const decoder = new TextDecoder()
      let buffer = ''

      const pump = (): Promise<void> =>
        reader.read().then(({ done, value }) => {
          if (done || cancelled) return
          buffer += decoder.decode(value, { stream: true })
          const parts = buffer.split('\n\n')
          buffer = parts.pop() ?? ''
          for (const part of parts) {
            if (part.includes('event: order') && part.includes('data:')) {
              setLive((n) => n + 1)
              queryClient.invalidateQueries({ queryKey: ['notif-unread'] })
              // Repaint any open order views on realtime status changes.
              queryClient.invalidateQueries({ queryKey: ['orders'] })
              try {
                const dataLine = part.split('\n').find((l) => l.startsWith('data:'))
                if (dataLine) {
                  const ev = JSON.parse(dataLine.slice(5).trim()) as { order_id?: string }
                  if (ev.order_id) {
                    queryClient.invalidateQueries({ queryKey: ['order', ev.order_id] })
                    queryClient.invalidateQueries({ queryKey: ['order-events', ev.order_id] })
                  }
                }
              } catch {
                // malformed event payload: ignore
              }
            }
          }
          return pump()
        })

      pump().catch(() => {
        if (!cancelled) setTimeout(connect, 5000)
      })
    }

    connect()
    return () => {
      cancelled = true
    }
  }, [user, queryClient])

  if (!user) return null

  const badge = (unread ?? 0) + live

  return (
    <div className="relative">
      <button
        onClick={() => {
          setOpen(!open)
          setLive(0)
          if (badge > 0) markRead.mutate()
        }}
        className="px-3 py-2 rounded-lg hover:bg-amber-600 relative text-white"
        title="Notifikasi"
      >
        🔔
        {badge > 0 && (
          <span className="absolute top-0 right-0 bg-red-600 text-white text-xs rounded-full min-w-5 h-5 flex items-center justify-center px-1">
            {badge}
          </span>
        )}
      </button>

      {open && (
        <>
          <div className="fixed inset-0 z-40" onClick={() => setOpen(false)} />
          <div className="absolute right-0 top-12 z-50 w-96 bg-white rounded-xl shadow-xl border border-gray-200 overflow-hidden">
            <div className="p-4 border-b flex justify-between items-center">
              <h3 className="font-bold text-sm">Notifikasi</h3>
              <Link to="/notifications" className="text-xs text-amber-600 hover:underline" onClick={() => setOpen(false)}>
                Semua
              </Link>
            </div>
            <div className="max-h-96 overflow-y-auto">
              {notifications?.length === 0 && (
                <p className="text-sm text-gray-500 text-center py-10">Belum ada notifikasi.</p>
              )}
              {notifications?.map((n) => {
                const href = notificationHref(n.data)
                const item = (
                  <div className="px-4 py-3 border-b border-gray-50 last:border-0 hover:bg-gray-50">
                    <div className="flex items-center justify-between">
                      <p className="text-sm font-medium">{n.title}</p>
                      <span className="text-xs text-gray-400">{formatDate(n.created_at)}</span>
                    </div>
                    <p className="text-xs text-gray-500 mt-0.5">{n.body}</p>
                  </div>
                )
                return href ? (
                  <Link key={n.id} to={href} onClick={() => setOpen(false)}>
                    {item}
                  </Link>
                ) : (
                  <div key={n.id}>{item}</div>
                )
              })}
            </div>
          </div>
        </>
      )}
    </div>
  )
}

