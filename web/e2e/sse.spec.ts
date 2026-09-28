import { test, expect } from '@playwright/test'
import { API, BUYER, login } from './fixtures'

/**
 * SSE regression: GET /api/v1/stream/orders must answer 200 with
 * `Content-Type: text/event-stream`.
 *
 * The bug this pins: the Prometheus metrics middleware wrapped the
 * ResponseWriter in `statusRecorder`, which embedded the `http.ResponseWriter`
 * *interface*. That only promotes Header/Write/WriteHeader, so a `w.(http.Flusher)`
 * assertion inside the stream handlers FAILED — and those handlers answer 500
 * "streaming unsupported" on failure. The effect was that every SSE endpoint
 * was dead in production while every non-streaming route looked perfectly
 * healthy, and nothing in the metrics or routing tests noticed. The fix
 * forwards Flusher/Hijacker/Pusher (see internal/metrics/metrics.go, which has
 * its own unit test for it); this asserts the end-to-end path, including the
 * reverse proxy in front of it.
 *
 * The request is made from inside the page rather than with the `request`
 * fixture on purpose: an SSE response never ends, so a client that buffers the
 * body would hang until its own timeout and report nothing. `fetch` resolves as
 * soon as the response *headers* arrive, which is exactly the signal being
 * asserted, and the AbortController stops the stream afterwards.
 */
test.describe('server-sent events', () => {
  test('GET /api/v1/stream/orders returns 200 text/event-stream', async ({ page, request }) => {
    const token = await login(request, BUYER)

    const result = await page.evaluate(
      async ({ url, token }) => {
        const controller = new AbortController()
        const timer = setTimeout(() => controller.abort(), 10_000)
        try {
          const res = await fetch(url, {
            headers: { Authorization: `Bearer ${token}` },
            signal: controller.signal,
          })
          return {
            status: res.status,
            contentType: res.headers.get('content-type'),
            cacheControl: res.headers.get('cache-control'),
            xAccelBuffering: res.headers.get('x-accel-buffering'),
          }
        } catch (e) {
          return { error: String(e) }
        } finally {
          clearTimeout(timer)
        }
      },
      // Same-origin path, so it goes through the Vite dev proxy exactly as the
      // app's own requests do.
      { url: '/api/v1/stream/orders', token },
    )

    expect(result, 'the stream request never returned headers').not.toHaveProperty('error')
    expect(result.status).toBe(200)
    expect(result.contentType).toContain('text/event-stream')
    // The handler sets these; their absence means a proxy in the path is
    // buffering the stream even though the status looks right.
    expect(result.cacheControl).toContain('no-cache')
    expect(result.xAccelBuffering).toBe('no')
  })

  test('the stream actually delivers events, not just an open socket', async ({ page, request }) => {
    const token = await login(request, BUYER)

    // Read the first event off the wire. `response.body` is a ReadableStream
    // that yields as data arrives, so this resolves on the server's first
    // writeEvent without waiting for the stream to close.
    const firstChunk = await page.evaluate(
      async ({ url, token }) => {
        const controller = new AbortController()
        const timer = setTimeout(() => controller.abort(), 15_000)
        try {
          const res = await fetch(url, {
            headers: { Authorization: `Bearer ${token}` },
            signal: controller.signal,
          })
          if (!res.body) return { error: 'no response body' }
          const reader = res.body.getReader()
          const decoder = new TextDecoder()
          let text = ''
          // A couple of reads: the handler writes the `connected` event
          // immediately, so this returns almost at once.
          for (let i = 0; i < 3; i++) {
            const { value, done } = await reader.read()
            if (done) break
            text += decoder.decode(value, { stream: true })
            if (text.includes('\n\n')) break
          }
          await reader.cancel().catch(() => {})
          return { text }
        } catch (e) {
          return { error: String(e) }
        } finally {
          clearTimeout(timer)
        }
      },
      { url: '/api/v1/stream/orders', token },
    )

    expect(firstChunk, 'no stream data arrived').not.toHaveProperty('error')
    // The first event the handler writes is `type: "connected"`.
    expect(firstChunk.text).toContain('event: order')
    expect(firstChunk.text).toContain('stream established')
  })

  test('the stream requires authentication', async ({ request }) => {
    // Without the auth middleware the route is unreachable. A short timeout
    // bounds the failure mode that matters here: if the endpoint wrongly
    // accepted an anonymous client, the response would be an endless stream
    // rather than a status code, and this must report that instead of hanging.
    const timeout = 10_000
    try {
      const res = await request.get(`${API}/api/v1/stream/orders`, {
        headers: { Accept: 'text/event-stream' },
        timeout,
      })
      expect(
        [401, 403],
        `anonymous stream request returned ${res.status()}`,
      ).toContain(res.status())
    } catch (e) {
      // Timing out means the request was ACCEPTED and the body never ended.
      throw new Error(
        `anonymous GET /api/v1/stream/orders did not fail fast; it hung, which means the endpoint served an unauthenticated stream: ${e}`,
      )
    }
  })
})
