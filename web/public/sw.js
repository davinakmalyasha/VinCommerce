/**
 * VinCommerce service worker.
 *
 * Security note — why /api/ is never cached:
 * Cache Storage keys are URL-only. Request headers (Authorization,
 * X-Session-Key) are NOT part of the key, and the API does not send
 * `Vary: Authorization`. The previous version cached every GET /api/*
 * response into one origin-wide bucket and served it from `.catch()`, so on
 * a shared device a logged-out visitor (or the next user) could be handed the
 * previous user's /orders, /wallet, /account/addresses and /notifications
 * from Cache Storage. Guest carts were the same: guest identity lives in the
 * X-Session-Key *header*, which the cache ignores entirely.
 *
 * The correct boundary is: cache only immutable, content-hashed build output
 * and the app shell. Everything else goes to the network.
 */

const VERSION = 'v2'
const ASSET_CACHE = `vincommerce-assets-${VERSION}`
const SHELL_CACHE = `vincommerce-shell-${VERSION}`
const KEEP = new Set([ASSET_CACHE, SHELL_CACHE])

// The offline fallback document, precached on install.
const OFFLINE_URL = '/offline.html'

self.addEventListener('install', (event) => {
  event.waitUntil(
    (async () => {
      const cache = await caches.open(SHELL_CACHE)
      // Only ever store responses we have verified are usable.
      await cache.add(new Request(OFFLINE_URL, { cache: 'reload' })).catch(() => {})
      await self.skipWaiting()
    })(),
  )
})

self.addEventListener('activate', (event) => {
  event.waitUntil(
    (async () => {
      const keys = await caches.keys()
      await Promise.all(keys.filter((k) => !KEEP.has(k)).map((k) => caches.delete(k)))
      // Take over open clients immediately so a new SW version does not
      // leave a stale one serving authenticated requests.
      await self.clients.claim()
    })(),
  )
})

/**
 * Explicit cache purge. The app posts this on logout / account switch so any
 * previously stored user data is dropped immediately rather than lingering
 * until the next version bump.
 */
self.addEventListener('message', (event) => {
  const data = event.data
  if (!data || data.type !== 'CLEAR_USER_CACHE') return
  event.waitUntil(
    (async () => {
      const names = await caches.keys()
      await Promise.all(names.map((n) => caches.delete(n)))
    })(),
  )
})

// Hashed, content-addressed build output: safe to serve from cache forever.
const isImmutableAsset = (url) =>
  url.pathname.startsWith('/assets/') || url.pathname.startsWith('/icons/')

// Anything under /api/ is user-scoped. Never read it from, or write it to,
// the cache. Same for uploads that may belong to another user.
const isUserScoped = (url) => url.pathname.startsWith('/api/') || url.pathname.startsWith('/uploads/')

async function cacheFirst(req, cacheName) {
  const cached = await caches.match(req)
  if (cached) return cached
  const res = await fetch(req)
  // Never persist an error response: a cached 401/403/404 outlives the
  // condition that produced it.
  if (res && res.ok && res.status === 200 && res.type === 'basic') {
    const cache = await caches.open(cacheName)
    cache.put(req, res.clone()).catch(() => {})
  }
  return res
}

self.addEventListener('fetch', (event) => {
  const req = event.request
  if (req.method !== 'GET') return

  let url
  try {
    url = new URL(req.url)
  } catch {
    return
  }

  // Never proxy cross-origin traffic (Midtrans, YouTube, analytics).
  if (url.origin !== self.location.origin) return

  // User-scoped data: pure network passthrough. No cache read, no cache
  // write, no offline fallback. A 401 must reach the app so it can refresh
  // or log out, and a response must never be replayed to a different user.
  if (isUserScoped(url)) return

  if (isImmutableAsset(url)) {
    event.respondWith(
      cacheFirst(req, ASSET_CACHE).catch(() => caches.match(OFFLINE_URL)),
    )
    return
  }

  // SPA navigations: network-first so a deploy is picked up immediately,
  // falling back to the cached shell and then to the offline document.
  if (req.mode === 'navigate') {
    event.respondWith(
      (async () => {
        try {
          const res = await fetch(req)
          if (res && res.ok) {
            const cache = await caches.open(SHELL_CACHE)
            cache.put('/', res.clone()).catch(() => {})
          }
          return res
        } catch {
          return (await caches.match('/')) || (await caches.match(OFFLINE_URL)) || Response.error()
        }
      })(),
    )
    return
  }

  // Any remaining same-origin GET (manifest, icons, etc.): network-first with
  // a cached fallback, but only for successful basic responses.
  event.respondWith(
    (async () => {
      try {
        const res = await fetch(req)
        if (res && res.ok && res.status === 200 && res.type === 'basic') {
          const cache = await caches.open(SHELL_CACHE)
          cache.put(req, res.clone()).catch(() => {})
        }
        return res
      } catch {
        const hit = await caches.match(req)
        if (hit) return hit
        throw new Error('offline and not cached: ' + url.pathname)
      }
    })(),
  )
})
