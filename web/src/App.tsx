import { useEffect } from 'react'
import { QueryClient, QueryClientProvider } from '@tanstack/react-query'
import { RouterProvider } from 'react-router-dom'
import { router } from './router'
import { useSession, bindQueryCacheClear } from './stores/session'
import { initTheme } from './stores/theme'

initTheme()

const queryClient = new QueryClient({
  defaultOptions: {
    queries: {
      staleTime: 30_000,
      retry: 1,
      refetchOnWindowFocus: false,
    },
  },
})

// Let the session store purge cached per-user queries on login/logout.
bindQueryCacheClear(() => queryClient.clear())

function SessionBootstrap() {
  const restore = useSession((s) => s.restore)
  const loading = useSession((s) => s.loading)

  useEffect(() => {
    restore()
  }, [restore])

  if (loading) {
    return (
      <div className="min-h-screen flex items-center justify-center text-gray-400 text-sm">
        Memuat sesi...
      </div>
    )
  }
  return <RouterProvider router={router} />
}

export default function App() {
  return (
    <QueryClientProvider client={queryClient}>
      <SessionBootstrap />
    </QueryClientProvider>
  )
}
