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
import { SellerLayout, SellerHome } from './pages/seller/SellerLayout'
import { SellerProducts } from './pages/seller/SellerProducts'
import { SellerOrders } from './pages/seller/SellerOrders'
import { SellerReturns } from './pages/seller/SellerReturns'
import { SellerWallet } from './pages/seller/SellerWallet'
import { SellerAnalytics } from './pages/seller/SellerAnalytics'
import { SellerSettings } from './pages/seller/SellerSettings'
import { SellerCoupons } from './pages/seller/SellerCoupons'
import { AdminLayout } from './pages/admin/AdminLayout'
import { AdminStores, AdminOverview } from './pages/admin/AdminStores'
import { AdminAnalytics } from './pages/admin/AdminAnalytics'
import { AdminCoupons } from './pages/admin/AdminCoupons'
import { AdminUsers } from './pages/admin/AdminUsers'
import { AdminArticles } from './pages/admin/AdminArticles'
import { AdminFlags } from './pages/admin/AdminFlags'
import { AdminTickets } from './pages/admin/AdminTickets'
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
import { AdminReviews } from './pages/admin/AdminReviews'
import { AdminReports } from './pages/admin/AdminReports'
import { AdminReturns } from './pages/admin/AdminReturns'
import { AdminShipping } from './pages/admin/AdminShipping'
import { AdminOrders, AdminAudit, AdminCommission, AdminCatalog, AdminFlashSales, AdminDisputes } from './pages/admin/AdminOpsPages'
import { TrackingPage } from './pages/TrackingPage'
import { VouchersPage } from './pages/VouchersPage'
import { ComparePage } from './pages/ComparePage'
import { FollowedStoresPage } from './pages/FollowedStoresPage'
import { NotFoundPage } from './pages/NotFoundPage'

export const router = createBrowserRouter([
  {
    path: '/',
    element: <RootLayout />,
    children: [
      { index: true, element: <HomePage /> },
      { path: 'search', element: <SearchPage /> },
      { path: 'product/:slug', element: <ProductPage /> },
      { path: 'store/:slug', element: <StorePage /> },
      { path: 'cart', element: <CartPage /> },
      { path: 'checkout', element: <CheckoutPage /> },
      { path: 'orders', element: <OrdersPage /> },
      { path: 'orders/:id', element: <OrderDetailPage /> },
      { path: 'login', element: <LoginPage /> },
      { path: 'register', element: <RegisterPage /> },
      { path: 'account', element: <AccountPage /> },
      { path: 'wishlist', element: <WishlistPage /> },
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
        element: <SellerLayout />,
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
        element: <AdminLayout />,
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
          { path: 'orders', element: <AdminOrders /> },
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
