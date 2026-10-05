# VinCommerce — web

The storefront and seller console: React 19 + TypeScript + Vite, styled with Tailwind 4.

This file replaces the Vite template README that was still sitting here, which
described a starting point rather than the application.

## What this app is

A multi-vendor marketplace UI in Indonesian, covering both sides of a trade:

- **Buyer** — catalogue, cart, checkout, orders, returns, loyalty, and dispute filing.
- **Seller** — storefront setup, KYC, products and bulk import, stock, orders,
  fulfilment, seller hold and payout visibility, and returns.
- **Admin** — KYC and store review, refunds, the double-entry ledger (trial balance,
  reconciliation, held balances), fee and payout-lag configuration, feature flags,
  role grants, and impersonation.

## Stack

| Concern | Choice |
| --- | --- |
| UI | React 19, TypeScript (strict) |
| Routing | react-router-dom |
| Server state | TanStack Query |
| Client state | Zustand |
| HTTP | axios |
| Styling | Tailwind CSS 4 |
| Lint | oxlint |
| Unit tests | Vitest + jsdom |
| E2E tests | Playwright |

`tsconfig.json` is strict, and the codebase carries no `any`, no `@ts-ignore`, and no
`eslint-disable` / `oxlint-disable` suppressions outside generated code. That is
enforced by `npm run typecheck` and `npm run lint` in CI, not by convention.

## Commands

| Command | Purpose |
| --- | --- |
| `npm run dev` | Vite dev server |
| `npm run build` | Type-check and build to `dist/` |
| `npm run typecheck` | `tsc --noEmit` |
| `npm run lint` | oxlint |
| `npm test` | Vitest, single run |
| `npm run test:watch` | Vitest in watch mode |
| `npm run coverage` | Vitest with V8 coverage → `coverage/` |
| `npm run preview` | Serve the built output |
| `npm run size` | Report bundle sizes (`scripts/measure-bundle.cjs`) |
| `npm run check:nginx` | Statically validate the nginx config for this SPA |
| `npm run test:nginx` | Self-test that checker |

Everything here runs offline once dependencies are installed. None of these scripts
start a container; `check-nginx.test.mjs` only asserts against the repo's own nginx
config file.

## Configuration

No secrets belong in this app. These are the four variables the code actually reads,
and they are exactly the four declared in [`.env.example`](.env.example):

| Variable | Purpose |
| --- | --- |
| `VITE_API_URL` | Backend base URL. Empty means same-origin, which is what you want behind the nginx proxy. |
| `VITE_API_DOCS_URL` | Where the "API docs" link points. `.env.example` sets `http://localhost:8080/api-docs`. |
| `VITE_MIDTRANS_CLIENT_KEY` | Public Snap client key. Public by design, but still per-environment. |
| `VITE_MIDTRANS_SNAP_URL` | Snap script URL. `.env.example` points at the sandbox host. |

Anything prefixed `VITE_` is **inlined into the JavaScript bundle** at build time and
is therefore public. Changing one requires a rebuild, not just a container restart. An
admin token must never be placed in a `VITE_` variable — that is what the un-prefixed
backend configuration is for.

Note that `VITE_MIDTRANS_CLIENT_KEY` being public is expected: Midtrans Snap is a
client-side flow and the key is a publishable credential. It is not a secret, and it
is also not a substitute for verifying anything server-side.

## Testing notes

Unit tests run under jsdom and cover the pure logic worth covering — money
arithmetic, order and return state machines, and the query-key and cache-invalidation
helpers. Playwright covers the flows that only fail when wired together.

Two areas are deliberately thin, and the gap is recorded rather than hidden:

- There is no visual-regression baseline.
- The E2E suite and the docs link must agree on the API prefix. `VITE_API_DOCS_URL`
  points at `/api-docs`, while the REST surface is under `/api/v1`; a mismatch between
  nginx, the docs route and the suite produces a 404 that reads like an application bug.

## Relationship to the backend

The backend is Go, in [`../backend`](../backend), and owns all money and all
authorisation. This app renders what the API returns and never decides whether an
action is permitted — a seller-only action is unavailable here and refused there. The
consequence for a reader of this code is that hiding a button is a UX affordance, not
a security control.

See the [repository README](../README.md) for the architecture, the Quickstart, and
the list of known gaps.
