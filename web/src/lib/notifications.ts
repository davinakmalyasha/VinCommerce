// notificationHref resolves a notification payload to an in-app route.
export function notificationHref(data: Record<string, unknown>): string | null {
  if (typeof data.order_id === 'string' && data.order_id) return `/orders/${data.order_id}`
  if (typeof data.product_slug === 'string' && data.product_slug) return `/product/${data.product_slug}`
  if (typeof data.cart_url === 'string' && data.cart_url) return data.cart_url
  if (typeof data.product_id === 'string' && data.product_id) return `/product/${data.product_id}`
  return null
}
