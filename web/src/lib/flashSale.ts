import { useQuery } from '@tanstack/react-query'
import { api } from './api'

export interface FlashSaleItem {
  id: string
  variant_id: string
  sale_price: number
  regular_price: number
  initial_stock: number
  sold_count: number
  product_id: string
  product_name: string
  product_slug: string
  image_url?: string
  stock: number
}

export interface ActiveFlashSale {
  flash_sale: { id: string; name: string; description: string; ends_at: string }
  items: FlashSaleItem[]
}

// Shared query: returns null when no flash sale is running (404).
export function useActiveFlashSale() {
  return useQuery({
    queryKey: ['flash-sale-active'],
    queryFn: async () => {
      try {
        const res = await api.get<ActiveFlashSale>('/flash-sales/active')
        return res.data
      } catch {
        return null
      }
    },
    staleTime: 60_000,
  })
}
