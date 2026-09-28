import { useEffect, useState } from 'react'
import { useMutation, useQuery, useQueryClient } from '@tanstack/react-query'
import { Link } from 'react-router-dom'
import { api, getAccessToken, refreshAccessToken } from '../../lib/api'
import { useSession } from '../../stores/session'
import { formatDate } from '../../lib/format'
import { notificationHref } from '../../lib/notifications'
import { Modal } from '../Modal'

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

  // The server count is the single source of truth. A local `live` counter
  // used to sit on top of it, so every streamed order event grew the badge by
  // 2 (one from the local increment, one from the refetched server count that
  // already included the event) and it never came back down.
  const { data: unread } = useQuery({
    queryKey: ['notif-unread'],
    queryFn: async () => (await api.get<{ unread: number }>('/notifications/unread-count')).data.unread,
    enabled: !!user,
    refetchInterval: 30_000,
  })

  const notificationsQuery = useQuery({
    queryKey: ['notifications'],
    queryFn: async () => (await api.get<{ notifications: Notification[] }>('/notifications?limit=15')).data.notifications,
    enabled: !!user && open,
    staleTime: 10_000,
  })
  const notifications = notificationsQuery.data

  // Scope the mark-read to the rows this dropdown actually renders. Posting
  // with no body marked the ENTIRE account read, including notifications the
  // user never saw.
  const markRead = useMutation({
    mutationFn: async (ids: number[]) => api.post('/notifications/read', { ids }),
    onSuccess: () => {
      queryClient.invalidateQueries({ queryKey: ['notif-unread'] })
      queryClient.invalidateQueries({ queryKey: ['notifications'] })
    },
  })


  useEffect(() => {
    if (!user) return
    let cancelled = false
    let reconnectTimer: number | undefined
    const cleanupFns: Array<() => void> = []

    const connect = async (): Promise<void> => {
      // The access token is short-lived and memory-only. Refresh FIRST so the
      // stream doesn't 401 immediately after boot; raw fetch bypasses the axios
      // refresh interceptor, so an expired token here would silently kill all
      // realtime notifications for the whole session.
      await refreshAccessToken()
      if (cancelled) return

      const controller = new AbortController()
      const onCleanup = () => controller.abort()
      cleanupFns.push(onCleanup)

      let res: Response
      try {
        res = await fetch('/api/v1/stream/orders', {
          headers: { Authorization: `Bearer ${getAccessToken()}` },
          signal: controller.signal,
        })
      } catch {
        cleanupFns.splice(cleanupFns.indexOf(onCleanup), 1)
        throw new Error('stream aborted')
      }
      if (!res.ok || !res.body) {
        cleanupFns.splice(cleanupFns.indexOf(onCleanup), 1)
        throw new Error('stream unavailable')
      }
      const reader = res.body.getReader()
      const decoder = new TextDecoder()
      let buffer = ''

      const pump = async (): Promise<void> => {
        for (;;) {
          const { done, value } = await reader.read()
          if (done || cancelled) return
          buffer += decoder.decode(value, { stream: true })
          const parts = buffer.split('\n\n')
          buffer = parts.pop() ?? ''
          for (const part of parts) {
            if (part.includes('event: order') && part.includes('data:')) {
              // One invalidation only. The refetched server count already
              // includes this event; the local bump was pure double counting.
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
        }
      }

      try {
        await pump()
        // Server closed gracefully — reconnect unless unmounted/logged out.
        if (!cancelled) reconnectTimer = window.setTimeout(() => void connect(), 5000)
      } finally {
        const idx = cleanupFns.indexOf(onCleanup)
        if (idx >= 0) cleanupFns.splice(idx, 1)
      }
    }

    connect().catch(() => {
      if (!cancelled) reconnectTimer = window.setTimeout(() => void connect(), 5000)
    })

    return () => {
      cancelled = true
      if (reconnectTimer) clearTimeout(reconnectTimer)
      for (const fn of cleanupFns) fn() // abort any in-flight stream fetch
      cleanupFns.length = 0
    }
  }, [user, queryClient])

  if (!user) return null

  const badge = unread ?? 0
  // Only the rows the dropdown is about to show get marked read.
  const visibleUnread = (notifications ?? []).filter((n) => !n.read_at).map((n) => n.id)

  return (
    <div className="relative">
      <button
        type="button"
        onClick={() => setOpen((o) => !o)}
        className="px-3 py-2 rounded-lg hover:bg-amber-600 relative text-white"
        title="Notifikasi"
        aria-haspopup="dialog"
        aria-expanded={open}
      >
        🔔
        {badge > 0 && (
          <span className="absolute top-0 right-0 bg-red-600 text-white text-xs rounded-full min-w-5 h-5 flex items-center justify-center px-1">
            {badge}
          </span>
        )}
      </button>

      <Modal
        open={open}
        onClose={() => setOpen(false)}
        title="Notifikasi"
        variant="anchored"
        hideCloseButton
        panelClassName="!p-0"
      >
        <div className="p-4 border-b flex justify-between items-center">
          <span className="text-xs text-gray-500">{badge} belum dibaca</span>
          {visibleUnread.length > 0 && (
            <button
              type="button"
              onClick={() => markRead.mutate(visibleUnread)}
              disabled={markRead.isPending}
              className="text-xs text-amber-600 hover:underline disabled:opacity-50"
            >
              Tandai dibaca
            </button>
          )}
          <Link to="/notifications" className="text-xs text-amber-600 hover:underline" onClick={() => setOpen(false)}>
            Semua
          </Link>
        </div>
        <div className="max-h-96 overflow-y-auto">
          {notificationsQuery.isLoading && (
            <p className="text-sm text-gray-500 text-center py-10">Memuat notifikasi...</p>
          )}
          {notificationsQuery.isError && (
            <div role="alert" className="p-4 text-center text-sm text-red-600">
              <p>Gagal memuat notifikasi.</p>
              <button
                type="button"
                onClick={() => void notificationsQuery.refetch()}
                className="mt-2 underline"
              >
                Coba lagi
              </button>
            </div>
          )}
          {notifications?.length === 0 && (
            <p className="text-sm text-gray-500 text-center py-10">Belum ada notifikasi.</p>
          )}
          {notifications?.map((n) => {
            const href = notificationHref(n.data)
            const item = (
              <div className="px-4 py-3 border-b border-gray-50 last:border-0 hover:bg-gray-50 text-left">
                <div className="flex items-center justify-between gap-2">
                  <p className={`text-sm font-medium ${n.read_at ? '' : 'text-amber-700'}`}>{n.title}</p>
                  <span className="text-xs text-gray-400 shrink-0">{formatDate(n.created_at)}</span>
                </div>
                <p className="text-xs text-gray-500 mt-0.5">{n.body}</p>
              </div>
            )
            return href ? (
              <Link key={n.id} to={href} onClick={() => setOpen(false)} className="block">
                {item}
              </Link>
            ) : (
              <div key={n.id}>{item}</div>
            )
          })}
        </div>
      </Modal>
    </div>
  )
}


