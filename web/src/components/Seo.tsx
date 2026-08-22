import { useEffect } from 'react'

interface SeoProps {
  title: string
  description?: string
  path?: string
}

function setMeta(attr: 'name' | 'property', key: string, content: string) {
  let el = document.head.querySelector<HTMLMetaElement>(`meta[${attr}="${key}"]`)
  if (!el) {
    el = document.createElement('meta')
    el.setAttribute(attr, key)
    document.head.appendChild(el)
  }
  el.setAttribute('content', content)
}

export function Seo({ title, description, path }: SeoProps) {
  useEffect(() => {
    document.title = title
    if (description) setMeta('name', 'description', description)
    setMeta('property', 'og:title', title)
    if (description) setMeta('property', 'og:description', description)
    if (path) setMeta('property', 'og:url', window.location.origin + path)
  }, [title, description, path])

  return null
}

// Defaults applied per-route when a page doesn't provide its own <Seo>.
export const DEFAULT_TITLES: Record<string, string> = {
  '/': 'VinCommerce — Belanja Online Marketplace',
  '/search': 'Cari Produk — VinCommerce',
  '/cart': 'Keranjang Belanja — VinCommerce',
  '/orders': 'Pesanan Saya — VinCommerce',
  '/account': 'Akun Saya — VinCommerce',
  '/wishlist': 'Wishlist — VinCommerce',
  '/followed-stores': 'Toko Diikuti — VinCommerce',
  '/help': 'Pusat Bantuan — VinCommerce',
  '/faq': 'FAQ — VinCommerce',
  '/flash-sales': 'Flash Sale — VinCommerce',
  '/vouchers': 'Kupon Saya — VinCommerce',
}
