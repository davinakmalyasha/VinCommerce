export interface User {
  id: string
  email: string
  full_name: string
  phone?: string
  roles: string[]
  status: string
  two_factor_enabled: boolean
  email_verified_at: string | null
  avatar_url?: string
}

export interface Category {
  id: string
  parent_id?: string
  name: string
  slug: string
  path: string
  depth: number
  children?: Category[]
}

export interface Variant {
  id: string
  sku: string
  name: string
  price: number
  compare_at_price?: number
  stock: number
  weight_grams: number
  image_url?: string
  attributes: Record<string, string>
  is_active: boolean
}

export interface Product {
  id: string
  seller_id: string
  name: string
  slug: string
  description: string
  status: string
  avg_rating: number
  rating_count: number
  sold_count: number
  created_at: string
  category?: { id: string; name: string }
  brand?: { id: string; name: string }
  seller?: { id: string; name: string }
  variants?: Variant[]
  images?: { url: string; is_primary?: boolean }[]
  attributes?: Record<string, string>
}

export interface SearchResult {
  items: Product[]
  total: number
  page: number
  page_size: number
}

export interface CartLine {
  id: string
  variant_id: string
  product_id: string
  product_name: string
  variant_name: string
  sku: string
  image_url: string
  price: number
  subtotal: number
  stock: number
  seller_id: string
  seller_name: string
  weight_grams: number
  quantity: number
}

export interface Order {
  id: string
  order_number: string
  buyer_id: string
  seller_id: string
  status: string
  subtotal: number
  discount_amount: number
  shipping_fee: number
  total_amount: number
  payment_status: string
  coupon_code?: string
  shipping_method?: string
  shipping_address: Record<string, string>
  placed_at: string
  items: OrderItem[]
  seller?: { id: string; name: string }
}

export interface OrderItem {
  id: string
  product_id: string
  variant_id: string
  product_name: string
  variant_name: string
  sku: string
  unit_price: number
  quantity: number
  weight_grams: number
  total: number
  image_url?: string
  status: string
}

export interface Address {
  id: string
  recipient: string
  phone: string
  address_line1: string
  address_line2?: string
  city: string
  province: string
  postal_code: string
  country: string
  label: string
  is_default: boolean
}

export interface Wallet {
  balance: number
  held_balance: number
}

export interface Store {
  id: string
  owner_id: string
  name: string
  slug: string
  description: string
  logo_url?: string
  banner_url?: string
  status: string
  rating: number
  rating_count: number
  products_count: number
  follower_count?: number
  is_following?: boolean
  is_verified?: boolean
  joined_at: string
}

export interface HelpCategory {
  id: string
  name: string
  slug: string
  position: number
  is_active: boolean
}

export interface HelpArticle {
  id: string
  category_id?: string
  title: string
  slug: string
  excerpt: string
  content: string
  section: string
  is_published: boolean
  view_count: number
  published_at?: string
  created_at: string
  updated_at: string
  category?: { name: string; slug: string }
}
