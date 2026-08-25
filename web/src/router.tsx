import { lazy, Suspense } from 'react'
import { createBrowserRouter } from 'react-router-dom'
import { RootLayout } from './components/layout/RootLayout'
import { HomePage } from './pages/HomePage'
import { SearchPage } from './pages/SearchPage'
import { ProductPage } from './pages/ProductPage'
import { CartPage } from './pages/CartPage'

// Secondary storefront pages + Seller Center + Admin Console are separate
// bundles, loaded on demand (keeps the entry chunk small for first paint).
const lazyOf = (loader: () => Promise<Record<string, unknown>>, name: string) =>
  lazy(() => loader().then((m) => ({ default: m[name] as React.ComponentType })))

const CheckoutPage = lazyOf(() => import('./pages/CheckoutPage'), 'CheckoutPage')
const OrdersPage = lazyOf(() => import('./pages/OrdersPage'), 'OrdersPage')
const OrderDetailPage = lazyOf(() => import('./pages/OrderDetailPage'), 'OrderDetailPage')
const LoginPage = lazyOf(() => import('./pages/LoginPage'), 'LoginPage')
const RegisterPage = lazyOf(() => import('./pages/RegisterPage'), 'RegisterPage')
const AccountPage = lazyOf(() => import('./pages/AccountPage'), 'AccountPage')
const WishlistPage = lazyOf(() => import('./pages/WishlistPage'), 'WishlistPage')

const SellerLayout = lazyOf(() => import('./pages/seller/SellerLayout'), 'SellerLayout')
const SellerHome = lazyOf(() => import('./pages/seller/SellerLayout'), 'SellerHome')
const SellerProducts = lazyOf(() => import('./pages/seller/SellerProducts'), 'SellerProducts')
const SellerOrders = lazyOf(() => import('./pages/seller/SellerOrders'), 'SellerOrders')
const SellerReturns = lazyOf(() => import('./pages/seller/SellerReturns'), 'SellerReturns')
const SellerWallet = lazyOf(() => import('./pages/seller/SellerWallet'), 'SellerWallet')
const SellerAnalytics = lazyOf(() => import('./pages/seller/SellerAnalytics'), 'SellerAnalytics')
const SellerSettings = lazyOf(() => import('./pages/seller/SellerSettings'), 'SellerSettings')
const SellerCoupons = lazyOf(() => import('./pages/seller/SellerCoupons'), 'SellerCoupons')
const SellerLiveStudio = lazyOf(() => import('./pages/seller/SellerLiveStudio'), 'SellerLiveStudio')

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
const AdminPayouts = lazyOf(() => import('./pages/admin/AdminPayouts'), 'AdminPayouts')

const HelpCenterPage = lazyOf(() => import('./pages/HelpCenterPage'), 'HelpCenterPage')
const HelpArticlePage = lazyOf(() => import('./pages/HelpArticlePage'), 'HelpArticlePage')
const FaqPage = lazyOf(() => import('./pages/FaqPage'), 'FaqPage')
const ContactPage = lazyOf(() => import('./pages/ContactPage'), 'ContactPage')
const MyTicketsPage = lazyOf(() => import('./pages/MyTicketsPage'), 'MyTicketsPage')
const TicketDetailPage = lazyOf(() => import('./pages/TicketDetailPage'), 'TicketDetailPage')
const DocsPage = lazyOf(() => import('./pages/DocsPage'), 'DocsPage')
// LegalPage takes props — wrap it so the generic lazy component type works.
const LazyLegal = lazy(() =>
  import('./pages/LegalPage').then((m) => ({ default: m.LegalPage })),
)
const LegalTerms = () => <LazyLegal page="terms" />
const LegalPrivacy = () => <LazyLegal page="privacy" />
const LegalRefund = () => <LazyLegal page="refund-policy" />
const LegalShipping = () => <LazyLegal page="shipping-policy" />
const StorePage = lazyOf(() => import('./pages/StorePage'), 'StorePage')
const NotificationsPage = lazyOf(() => import('./pages/NotificationsPage'), 'NotificationsPage')
const FlashSalePage = lazyOf(() => import('./pages/FlashSalePage'), 'FlashSalePage')
const TrackingPage = lazyOf(() => import('./pages/TrackingPage'), 'TrackingPage')
const VouchersPage = lazyOf(() => import('./pages/VouchersPage'), 'VouchersPage')
const ComparePage = lazyOf(() => import('./pages/ComparePage'), 'ComparePage')
const FollowedStoresPage = lazyOf(() => import('./pages/FollowedStoresPage'), 'FollowedStoresPage')
const VerifyEmailPage = lazyOf(() => import('./pages/VerifyEmailPage'), 'VerifyEmailPage')
const ResetPasswordPage = lazyOf(() => import('./pages/ResetPasswordPage'), 'ResetPasswordPage')
const MyReturnsPage = lazyOf(() => import('./pages/MyReturnsPage'), 'MyReturnsPage')
const WalletPage = lazyOf(() => import('./pages/WalletPage'), 'WalletPage')
const DiscoverFeedPage = lazyOf(() => import('./pages/DiscoverFeedPage'), 'DiscoverFeedPage')
const InviteLandingPage = lazyOf(() => import('./pages/InviteLandingPage'), 'InviteLandingPage')
const LivePage = lazyOf(() => import('./pages/LivePage'), 'LivePage')
const SupportConsole = lazyOf(() => import('./pages/support/SupportConsole'), 'SupportConsole')
const NotFoundPage = lazyOf(() => import('./pages/NotFoundPage'), 'NotFoundPage')

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
      { path: 'live', element: <LivePage /> },
      { path: 'support', element: <SupportConsole /> },
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
      { path: 'terms', element: <LegalTerms /> },
      { path: 'privacy', element: <LegalPrivacy /> },
      { path: 'refund-policy', element: <LegalRefund /> },
      { path: 'shipping-policy', element: <LegalShipping /> },
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
          { path: 'live', element: <SellerLiveStudio /> },
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
          { path: 'payouts', element: <AdminPayouts /> },
        ],
      },
    ],
  },
])
