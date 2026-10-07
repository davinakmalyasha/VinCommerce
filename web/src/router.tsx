import { lazy, Suspense, type ComponentType, type ReactNode } from 'react'
import { createBrowserRouter } from 'react-router-dom'
import { RootLayout } from './components/layout/RootLayout'
import { ErrorBoundary, RouteSkeleton } from './components/ErrorBoundary'

/**
 * Every page is a separate chunk. Previously HomePage, SearchPage,
 * ProductPage and CartPage were imported eagerly, which pulled ProductPage
 * (35 KB of source) into the entry bundle: the entry was a single 371 KB file
 * containing react-dom, the router, react-query, zustand, axios AND the app
 * code, so any app change invalidated vendor code in every visitor's cache.
 */
const lazyOf = (loader: () => Promise<Record<string, unknown>>, name: string) =>
  lazy(() => loader().then((m) => ({ default: m[name] as ComponentType })))

const HomePage = lazyOf(() => import('./pages/HomePage'), 'HomePage')
const SearchPage = lazyOf(() => import('./pages/SearchPage'), 'SearchPage')
const ProductPage = lazyOf(() => import('./pages/ProductPage'), 'ProductPage')
const CartPage = lazyOf(() => import('./pages/CartPage'), 'CartPage')
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
const AdminPayoutBatches = lazyOf(() => import('./pages/admin/AdminPayoutBatches'), 'AdminPayoutBatches')

const HelpCenterPage = lazyOf(() => import('./pages/HelpCenterPage'), 'HelpCenterPage')
const HelpArticlePage = lazyOf(() => import('./pages/HelpArticlePage'), 'HelpArticlePage')
const FaqPage = lazyOf(() => import('./pages/FaqPage'), 'FaqPage')
const ContactPage = lazyOf(() => import('./pages/ContactPage'), 'ContactPage')
const MyTicketsPage = lazyOf(() => import('./pages/MyTicketsPage'), 'MyTicketsPage')
const TicketDetailPage = lazyOf(() => import('./pages/TicketDetailPage'), 'TicketDetailPage')
const DocsPage = lazyOf(() => import('./pages/DocsPage'), 'DocsPage')
const ApiQuickstartPage = lazyOf(() => import('./pages/ApiQuickstartPage'), 'ApiQuickstartPage')
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

/**
 * Wraps a route element in a Suspense boundary AND an error boundary.
 *
 * Without a boundary, a cold navigation to any lazy route suspends all the
 * way to the root: React holds the root suspended and the user gets a blank
 * white page with no header or footer for the duration of the chunk fetch.
 * The previous code only wrapped /seller and /admin, leaving ~45 routes
 * without a fallback.
 */
function route(node: ReactNode, label: string) {
  return (
    <ErrorBoundary label={label}>
      <Suspense fallback={<RouteSkeleton label={label} />}>{node}</Suspense>
    </ErrorBoundary>
  )
}

const SellerLayoutLazy = () => route(<SellerLayout />, 'Seller Center')
const AdminLayoutLazy = () => route(<AdminLayout />, 'Admin Console')

export const router = createBrowserRouter([
  {
    path: '/',
    element: <RootLayout />,
    errorElement: route(<NotFoundPage />, 'Halaman'),
    children: [
      { index: true, element: route(<HomePage />, 'Beranda') },
      { path: 'discover', element: route(<DiscoverFeedPage />, 'Discover') },
      { path: 'search', element: route(<SearchPage />, 'Pencarian') },
      { path: 'product/:slug', element: route(<ProductPage />, 'Produk') },
      { path: 'store/:slug', element: route(<StorePage />, 'Toko') },
      { path: 'cart', element: route(<CartPage />, 'Keranjang') },
      { path: 'checkout', element: route(<CheckoutPage />, 'Checkout') },
      { path: 'orders', element: route(<OrdersPage />, 'Pesanan') },
      { path: 'orders/:id', element: route(<OrderDetailPage />, 'Detail pesanan') },
      { path: 'login', element: route(<LoginPage />, 'Masuk') },
      { path: 'register', element: route(<RegisterPage />, 'Daftar') },
      { path: 'verify-email', element: route(<VerifyEmailPage />, 'Verifikasi email') },
      { path: 'reset-password', element: route(<ResetPasswordPage />, 'Reset kata sandi') },
      { path: 'forgot-password', element: route(<ResetPasswordPage />, 'Lupa kata sandi') },
      { path: 'invite/:code', element: route(<InviteLandingPage />, 'Undangan') },
      { path: 'account', element: route(<AccountPage />, 'Akun') },
      { path: 'wishlist', element: route(<WishlistPage />, 'Favorit') },
      { path: 'wallet', element: route(<WalletPage />, 'Dompet') },
      { path: 'returns', element: route(<MyReturnsPage />, 'Retur') },
      { path: 'followed-stores', element: route(<FollowedStoresPage />, 'Toko diikuti') },
      { path: 'notifications', element: route(<NotificationsPage />, 'Notifikasi') },
      { path: 'flash-sales', element: route(<FlashSalePage />, 'Flash sale') },
      { path: 'live', element: route(<LivePage />, 'Live') },
      { path: 'support', element: route(<SupportConsole />, 'Bantuan') },
      { path: 'tracking/:number', element: route(<TrackingPage />, 'Lacak pesanan') },
      { path: 'vouchers', element: route(<VouchersPage />, 'Voucher') },
      { path: 'compare', element: route(<ComparePage />, 'Bandingkan') },
      { path: 'help', element: route(<HelpCenterPage />, 'Pusat bantuan') },
      { path: 'help/:slug', element: route(<HelpArticlePage />, 'Artikel') },
      { path: 'faq', element: route(<FaqPage />, 'FAQ') },
      { path: 'contact', element: route(<ContactPage />, 'Kontak') },
      { path: 'account/tickets', element: route(<MyTicketsPage />, 'Tiket saya') },
      { path: 'account/tickets/:id', element: route(<TicketDetailPage />, 'Detail tiket') },
      { path: 'docs', element: route(<DocsPage />, 'Dokumentasi') },
      // This used to render HelpArticlePage, which reads a :slug param this
      // route does not provide, so the "API Quickstart" link 404'd with
      // GET /help/articles/undefined.
      { path: 'docs/api', element: route(<ApiQuickstartPage />, 'API Quickstart') },
      { path: 'terms', element: route(<LegalTerms />, 'Syarat & ketentuan') },
      { path: 'privacy', element: route(<LegalPrivacy />, 'Kebijakan privasi') },
      { path: 'refund-policy', element: route(<LegalRefund />, 'Kebijakan retur') },
      { path: 'shipping-policy', element: route(<LegalShipping />, 'Kebijakan pengiriman') },
      { path: '*', element: route(<NotFoundPage />, 'Halaman tidak ditemukan') },
      {
        path: 'seller',
        element: <SellerLayoutLazy />,
        children: [
          { index: true, element: route(<SellerHome />, 'Dashboard seller') },
          { path: 'products', element: route(<SellerProducts />, 'Produk saya') },
          { path: 'orders', element: route(<SellerOrders />, 'Pesanan masuk') },
          { path: 'returns', element: route(<SellerReturns />, 'Retur masuk') },
          { path: 'wallet', element: route(<SellerWallet />, 'Dompet seller') },
          { path: 'analytics', element: route(<SellerAnalytics />, 'Analitik seller') },
          { path: 'coupons', element: route(<SellerCoupons />, 'Kupon toko') },
          { path: 'live', element: route(<SellerLiveStudio />, 'Studio live') },
          { path: 'settings', element: route(<SellerSettings />, 'Pengaturan toko') },
        ],
      },
      {
        path: 'admin',
        element: <AdminLayoutLazy />,
        children: [
          { index: true, element: route(<AdminOverview />, 'Admin overview') },
          { path: 'stores', element: route(<AdminStores />, 'Kelola toko') },
          { path: 'tickets', element: route(<AdminTickets />, 'Antrean tiket') },
          { path: 'reviews', element: route(<AdminReviews />, 'Moderasi ulasan') },
          { path: 'reports', element: route(<AdminReports />, 'Laporan produk') },
          { path: 'returns', element: route(<AdminReturns />, 'Antrean retur') },
          { path: 'coupons', element: route(<AdminCoupons />, 'Kupon platform') },
          { path: 'shipping', element: route(<AdminShipping />, 'Metode pengiriman') },
          { path: 'users', element: route(<AdminUsers />, 'Pengguna') },
          { path: 'articles', element: route(<AdminArticles />, 'Artikel bantuan') },
          { path: 'flags', element: route(<AdminFlags />, 'Feature flags') },
          { path: 'analytics', element: route(<AdminAnalytics />, 'Analitik platform') },
          { path: 'orders', element: route(<AdminOps />, 'Cari pesanan') },
          { path: 'audit', element: route(<AdminAudit />, 'Audit log') },
          { path: 'commission', element: route(<AdminCommission />, 'Komisi platform') },
          { path: 'catalog', element: route(<AdminCatalog />, 'Katalog') },
          { path: 'flash-sales', element: route(<AdminFlashSales />, 'Flash sale') },
          { path: 'disputes', element: route(<AdminDisputes />, 'Sengketa') },
          { path: 'payouts', element: route(<AdminPayouts />, 'Pencairan dana') },
          {
            path: 'payout-batches',
            element: route(<AdminPayoutBatches />, 'Batch pencairan'),
          },
        ],
      },
    ],
  },
])
