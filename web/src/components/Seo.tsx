import { useEffect } from 'react'

interface SeoProps {
  title: string
  description?: string
  path?: string
  image?: string
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

function setLink(rel: string, href: string) {
  let el = document.head.querySelector<HTMLLinkElement>(`link[rel="${rel}"]`)
  if (!el) {
    el = document.createElement('link')
    el.setAttribute('rel', rel)
    document.head.appendChild(el)
  }
  el.setAttribute('href', href)
}

export function Seo({ title, description, path, image }: SeoProps) {
  useEffect(() => {
    document.title = title
    if (description) setMeta('name', 'description', description)
    const url = window.location.origin + (path ?? window.location.pathname)
    setMeta('property', 'og:title', title)
    if (description) setMeta('property', 'og:description', description)
    setMeta('property', 'og:url', url)
    if (image) {
      setMeta('property', 'og:image', image)
      setMeta('name', 'twitter:card', 'summary_large_image')
      setMeta('name', 'twitter:image', image)
    }
    setLink('canonical', url)
  }, [title, description, path, image])

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
  '/vouchers': 'Kupon & Voucher — VinCommerce',
}
