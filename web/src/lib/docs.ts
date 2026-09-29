/**
 * Where the API's own documentation lives.
 *
 * The API mounts Swagger UI and the OpenAPI document at /api-docs, not /docs.
 * The SPA owns a /docs route (router.tsx -> DocsPage) and a /docs/api quickstart
 * page, and web/nginx.conf used to proxy /docs to the API -- which made both
 * unreachable behind the edge in every containerised deployment. It looked
 * correct in development because the Vite dev proxy only forwards /api and
 * /uploads, so locally the SPA owned /docs. That gap is why a commit fixed the
 * dev-mode 404 while leaving the production path shadowed; check-nginx.mjs now
 * fails the build if a /docs location ever proxies to the API again.
 *
 * This is a single constant so the base path cannot be spelled three different
 * ways in three files, which is how it ended up as `http://localhost:8080/docs`
 * in .env.example, a hardcoded string in ApiQuickstartPage, and a relative
 * href in DocsPage.
 */
const DEV_DEFAULT = 'http://localhost:8080/api-docs'

export const API_DOCS_BASE = (
  import.meta.env.VITE_API_DOCS_URL ?? DEV_DEFAULT
).replace(/\/+$/, '')

/**
 * Build an absolute link to a path under the API's docs.
 *
 * The API and the SPA are usually on different origins (the SPA is served by
 * nginx on :5173 in development, and the API is on :8080), so a relative
 * `/api-docs/...` would 404 against the SPA's own origin. When
 * VITE_API_DOCS_URL is set to a same-origin value this still works, because the
 * base is then just a path.
 */
export const apiDocsUrl = (path = ''): string =>
  `${API_DOCS_BASE}${path.startsWith('/') ? path : `/${path}`}`
