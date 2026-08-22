import { useEffect } from 'react'
import { Outlet, useLocation } from 'react-router-dom'
import { Header } from './Header'
import { Footer } from './Footer'
import { ChatWidget } from './ChatWidget'
import { VerifyEmailBanner } from './VerifyEmailBanner'
import { DEFAULT_TITLES } from '../Seo'

export function RootLayout() {
  const { pathname } = useLocation()

  useEffect(() => {
    const exact = DEFAULT_TITLES[pathname]
    if (exact) {
      document.title = exact
    }
  }, [pathname])

  return (
    <div className="min-h-screen flex flex-col bg-gray-50 text-gray-900 dark:bg-gray-950 dark:text-gray-100">
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
