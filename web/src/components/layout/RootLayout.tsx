import { useEffect } from 'react'
import { Outlet, useLocation, useNavigate } from 'react-router-dom'
import { Header } from './Header'
import { Footer } from './Footer'
import { ChatWidget } from './ChatWidget'
import { VerifyEmailBanner } from './VerifyEmailBanner'
import { useSession } from '../../stores/session'
import { DEFAULT_TITLES } from '../Seo'

export function RootLayout() {
  const { pathname } = useLocation()
  const impersonating = useSession((s) => s.impersonating)
  const stopImpersonation = useSession((s) => s.stopImpersonation)
  const navigate = useNavigate()

  useEffect(() => {
    const exact = DEFAULT_TITLES[pathname]
    if (exact) {
      document.title = exact
    }
  }, [pathname])

  return (
    <div className="min-h-screen flex flex-col bg-gray-50 text-gray-900 dark:bg-gray-950 dark:text-gray-100">
      {impersonating && (
        <div className="bg-red-600 text-white text-sm px-4 py-2 flex items-center justify-between">
          <p>
            🎭 Mode impersonasi: kamu login sebagai <b>{impersonating.name}</b> ({impersonating.email}). Semua aksi tercatat di audit log.
          </p>
          <button
            onClick={async () => {
              await stopImpersonation()
              navigate('/admin/users')
            }}
            className="px-3 py-1 rounded-lg bg-white/20 hover:bg-white/30 text-xs font-medium"
          >
            Kembali ke Admin
          </button>
        </div>
      )}
      <VerifyEmailBanner />
      <Header />
      <main className="flex-1">
        <Outlet />
      </main>
      <Footer />
      <ChatWidget />
    </div>
  )
}
