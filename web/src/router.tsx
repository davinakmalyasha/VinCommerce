import { lazy, Suspense } from 'react'
import { createBrowserRouter } from 'react-router-dom'
import { RootLayout } from './components/layout/RootLayout'
import { HomePage } from './pages/HomePage'
import { SearchPage } from './pages/SearchPage'
import { ProductPage } from './pages/ProductPage'
import { CartPage } from './pages/CartPage'
import { CheckoutPage } from './pages/CheckoutPage'
import { OrdersPage } from './pages/OrdersPage'
import { OrderDetailPage } from './pages/OrderDetailPage'
import { LoginPage } from './pages/LoginPage'
import { RegisterPage } from './pages/RegisterPage'
import { AccountPage } from './pages/AccountPage'
import { WishlistPage } from './pages/WishlistPage'

// Seller Center + Admin Console are separate bundles, loaded on demand.
const lazyOf = (loader: () => Promise<Record<string, unknown>>, name: string) =>
  lazy(() => loader().then((m) => ({ default: m[name] as React.ComponentType })))

const SellerLayout = lazyOf(() => import('./pages/seller/SellerLayout'), 'SellerLayout')
const SellerHome = lazyOf(() => import('./pages/seller/SellerLayout'), 'SellerHome')
const SellerProducts = lazyOf(() => import('./pages/seller/SellerProducts'), 'SellerProducts')
const SellerOrders = lazyOf(() => import('./pages/seller/SellerOrders'), 'SellerOrders')
const SellerReturns = lazyOf(() => import('./pages/seller/SellerReturns'), 'SellerReturns')
const SellerWallet = lazyOf(() => import('./pages/seller/SellerWallet'), 'SellerWallet')
const SellerAnalytics = lazyOf(() => import('./pages/seller/SellerAnalytics'), 'SellerAnalytics')
const SellerSettings = lazyOf(() => import('./pages/seller/SellerSettings'), 'SellerSettings')
const SellerCoupons = lazyOf(() => import('./pages/seller/SellerCoupons'), 'SellerCoupons')

const AdminLayout = lazyOf(() => import('./pages/admin/AdminLayout'), 'AdminLayout')
const AdminStores = lazyOf(() => import('./pages/admin/AdminStores'), 'AdminStores')
const AdminOverview = lazyOf(() => import('./pages/admin/AdminStores'), 'AdminOverview')
const AdminAnalytics = lazyOf(() => import('./pages/admin/AdminAnalytics'), 'AdminAnalytics')
const AdminCoupons = lazyOf(() => import('./pages/admin/AdminCoupons'), 'AdminCoupons')
const AdminUsers = lazyOf(() => import('./pages/admin/AdminUsers'), 'AdminUsers')
const AdminArticles = lazyOf(() => import('./pages/admin/AdminArticles'), 'AdminArticles')
const AdminFlags = lazyOf(() => import('./pages/admin/AdminFlags'), 'AdminFlags')
const AdminTickets = lazyOf(() => import('./pages/admin/AdminTickets'), 'AdminTickets')
const AdminReviews = lazyOf(() => import('./pages/admin/AdminReviews'), 'AdminReviews')
const AdminReports = lazyOf(() => import('./pages/admin/AdminReports'), 'AdminReports')
const AdminReturns = lazyOf(() => import('./pages/admin/AdminReturns'), 'AdminReturns')
const AdminShipping = lazyOf(() => import('./pages/admin/AdminShipping'), 'AdminShipping')
const AdminOps = lazyOf(() => import('./pages/admin/AdminOpsPages'), 'AdminOrders')
const AdminAudit = lazyOf(() => import('./pages/admin/AdminOpsPages'), 'AdminAudit')
const AdminCommission = lazyOf(() => import('./pages/admin/AdminOpsPages'), 'AdminCommission')
const AdminCatalog = lazyOf(() => import('./pages/admin/AdminOpsPages'), 'AdminCatalog')
const AdminFlashSales = lazyOf(() => import('./pages/admin/AdminOpsPages'), 'AdminFlashSales')
const AdminDisputes = lazyOf(() => import('./pages/admin/AdminOpsPages'), 'AdminDisputes')

import { HelpCenterPage } from './pages/HelpCenterPage'
import { HelpArticlePage } from './pages/HelpArticlePage'
import { FaqPage } from './pages/FaqPage'
import { ContactPage } from './pages/ContactPage'
import { MyTicketsPage } from './pages/MyTicketsPage'
import { TicketDetailPage } from './pages/TicketDetailPage'
import { DocsPage } from './pages/DocsPage'
import { LegalPage } from './pages/LegalPage'
import { StorePage } from './pages/StorePage'
import { NotificationsPage } from './pages/NotificationsPage'
import { FlashSalePage } from './pages/FlashSalePage'
import { TrackingPage } from './pages/TrackingPage'
import { VouchersPage } from './pages/VouchersPage'
import { ComparePage } from './pages/ComparePage'
import { FollowedStoresPage } from './pages/FollowedStoresPage'
import { VerifyEmailPage } from './pages/VerifyEmailPage'
import { ResetPasswordPage } from './pages/ResetPasswordPage'
import { MyReturnsPage } from './pages/MyReturnsPage'
import { WalletPage } from './pages/WalletPage'
import { DiscoverFeedPage } from './pages/DiscoverFeedPage'
import { InviteLandingPage } from './pages/InviteLandingPage'
import { NotFoundPage } from './pages/NotFoundPage'

function LazyOutlet({ children }: { children: React.ReactNode }) {
  return <Suspense fallback={<div className="py-24 text-center text-sm text-gray-400">Memuat modul...</div>}>{children}</Suspense>
}

export const router = createBrowserRouter([
  {
    path: '/',
    element: <RootLayout />,
    children: [
      { index: true, element: <HomePage /> },
      { path: 'discover', element: <DiscoverFeedPage /> },
      { path: 'search', element: <SearchPage /> },
      { path: 'product/:slug', element: <ProductPage /> },
      { path: 'store/:slug', element: <StorePage /> },
      { path: 'cart', element: <CartPage /> },
      { path: 'checkout', element: <CheckoutPage /> },
      { path: 'orders', element: <OrdersPage /> },
      { path: 'orders/:id', element: <OrderDetailPage /> },
      { path: 'login', element: <LoginPage /> },
      { path: 'register', element: <RegisterPage /> },
      { path: 'verify-email', element: <VerifyEmailPage /> },
      { path: 'reset-password', element: <ResetPasswordPage /> },
      { path: 'invite/:code', element: <InviteLandingPage /> },
      { path: 'account', element: <AccountPage /> },
      { path: 'wishlist', element: <WishlistPage /> },
      { path: 'wallet', element: <WalletPage /> },
      { path: 'returns', element: <MyReturnsPage /> },
      { path: 'followed-stores', element: <FollowedStoresPage /> },
      { path: 'notifications', element: <NotificationsPage /> },
      { path: 'flash-sales', element: <FlashSalePage /> },
      { path: 'tracking/:number', element: <TrackingPage /> },
      { path: 'vouchers', element: <VouchersPage /> },
      { path: 'compare', element: <ComparePage /> },
      { path: 'help', element: <HelpCenterPage /> },
      { path: 'help/:slug', element: <HelpArticlePage /> },
      { path: 'faq', element: <FaqPage /> },
      { path: 'contact', element: <ContactPage /> },
      { path: 'account/tickets', element: <MyTicketsPage /> },
      { path: 'account/tickets/:id', element: <TicketDetailPage /> },
      { path: 'docs', element: <DocsPage /> },
      { path: 'docs/api', element: <HelpArticlePage /> },
      { path: 'terms', element: <LegalPage page="terms" /> },
      { path: 'privacy', element: <LegalPage page="privacy" /> },
      { path: 'refund-policy', element: <LegalPage page="refund-policy" /> },
      { path: 'shipping-policy', element: <LegalPage page="shipping-policy" /> },
      { path: '*', element: <NotFoundPage /> },
      {
        path: 'seller',
        element: <LazyOutlet><SellerLayout /></LazyOutlet>,
        children: [
          { index: true, element: <SellerHome /> },
          { path: 'products', element: <SellerProducts /> },
          { path: 'orders', element: <SellerOrders /> },
          { path: 'returns', element: <SellerReturns /> },
          { path: 'wallet', element: <SellerWallet /> },
          { path: 'analytics', element: <SellerAnalytics /> },
          { path: 'coupons', element: <SellerCoupons /> },
          { path: 'settings', element: <SellerSettings /> },
        ],
      },
      {
        path: 'admin',
        element: <LazyOutlet><AdminLayout /></LazyOutlet>,
        children: [
          { index: true, element: <AdminOverview /> },
          { path: 'stores', element: <AdminStores /> },
          { path: 'tickets', element: <AdminTickets /> },
          { path: 'reviews', element: <AdminReviews /> },
          { path: 'reports', element: <AdminReports /> },
          { path: 'returns', element: <AdminReturns /> },
          { path: 'coupons', element: <AdminCoupons /> },
          { path: 'shipping', element: <AdminShipping /> },
          { path: 'users', element: <AdminUsers /> },
          { path: 'articles', element: <AdminArticles /> },
          { path: 'flags', element: <AdminFlags /> },
          { path: 'analytics', element: <AdminAnalytics /> },
          { path: 'orders', element: <AdminOps /> },
          { path: 'audit', element: <AdminAudit /> },
          { path: 'commission', element: <AdminCommission /> },
          { path: 'catalog', element: <AdminCatalog /> },
          { path: 'flash-sales', element: <AdminFlashSales /> },
          { path: 'disputes', element: <AdminDisputes /> },
        ],
      },
    ],
  },
])
