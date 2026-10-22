/// <reference types="node" />
// Reads the source tree off disk, so it needs Node's types. The `src` tsconfig
// deliberately omits them -- application code has no business touching `fs` -- so they
// are requested here rather than by widening that config for every app file.
import { describe, it, expect } from 'vitest'
import { readFileSync, readdirSync, statSync } from 'node:fs'
import { join } from 'node:path'

function walk(dir: string, out: string[] = []): string[] {
  for (const name of readdirSync(dir)) {
    const p = join(dir, name)
    if (statSync(p).isDirectory()) walk(p, out)
    else if (/\.tsx?$/.test(name)) out.push(p)
  }
  return out
}

// A TanStack Query cache key must identify a REQUEST, not a topic.
//
// Two queries sharing a key is the most benign-looking cache bug there is: nothing
// throws, no type errors appear, and both components render. Whichever mounted first
// fills the cache entry and the second is served the wrong response, silently, because
// `useQuery` treats a cache hit as success and never runs its own `queryFn`.

const sellerBundles = readFileSync(join(process.cwd(), 'src/pages/seller/SellerBundles.tsx'), 'utf-8')
const sellerProducts = readFileSync(join(process.cwd(), 'src/pages/seller/SellerProducts.tsx'), 'utf-8')

describe('the seller product list and the bundle variant picker', () => {
  // These are different requests: the list is paginated, the picker asks for 50 items.
  it('do not share a cache key', () => {
    // SellerProducts keys the list ['seller-products', page], and page 1 is 1. Keying the
    // picker the same way meant a seller who opened Products then navigated to Bundles
    // got the paginated page in the picker, and one who opened Bundles first got a
    // 50-item response with no pagination metadata in their product list. Neither
    // crashes, so it presents as quietly wrong data rather than an error.
    expect(sellerBundles).not.toMatch(/queryKey:\s*\[\s*'seller-products'\s*,\s*1\s*\]/)
    expect(sellerProducts).toMatch(/queryKey:\s*\[\s*'seller-products'\s*,\s*page\s*\]/)
  })

  it('gives the picker a key of its own', () => {
    expect(sellerBundles).toMatch(/queryKey:\s*\[\s*'seller-products'\s*,\s*'bundle-picker'/)
  })

  it('still asks for the wider page it needs', () => {
    // The fix must not quietly drop page_size=50, which is the whole reason the picker
    // is a separate request from the list.
    expect(sellerBundles).toContain('/seller/products?page_size=50')
  })
})

// A guard, scoped to what can be asserted reliably.
//
// An earlier version of this tried to detect every collision generically by matching
// `queryKey: [...]` across the tree and reading the `api.*` call that follows. It was
// dropped: the window needed to reach the call was wide enough to run into the NEXT
// query, so it reported ['cart'], ['orders'], ['wishlist'] and seven others as
// collisions when every one of them was a legitimate shared cache entry for a single
// endpoint. A checker that cries wolf gets deleted rather than trusted, and a
// false-positive-prone assertion is more damaging than no assertion.
//
// What is asserted below is therefore narrow and exact. Extending it means writing a
// real parser over the source, not widening a regex window.
describe('shared cache keys are deliberate where they remain', () => {
  const cases: Array<[string, string, string]> = [
    // file, endpoint, why sharing one key is correct
    ['src/pages/WalletPage.tsx', '/wallet', 'buyer wallet'],
    ['src/pages/seller/SellerWallet.tsx', '/wallet', 'seller wallet is the same endpoint'],
  ]
  for (const [file, endpoint, why] of cases) {
    it(`${file} fetches ${endpoint} (${why})`, () => {
      const text = readFileSync(join(process.cwd(), file), 'utf-8')
      expect(text).toContain(`queryKey: ['wallet']`)
      expect(text).toContain(endpoint)
    })
  }

  it('both wallet consumers agree on the response shape', () => {
    // Both read data.wallet.balance and data.transactions. If either had been typed as
    // the wallet itself rather than the envelope, one of them would read
    // undefined.balance and render Rp0 while looking perfectly healthy.
    for (const file of ['src/pages/WalletPage.tsx', 'src/pages/seller/SellerWallet.tsx']) {
      const text = readFileSync(join(process.cwd(), file), 'utf-8')
      expect(text).toMatch(/\.wallet\.balance|wallet:\s*\{/)
      expect(text).toMatch(/transactions/)
    }
  })
})

describe('the source tree is walkable from the test', () => {
  it('finds the components this file reasons about', () => {
    const files = walk(join(process.cwd(), 'src'))
    expect(files.length).toBeGreaterThan(100)
    expect(files.some((f) => f.endsWith('SellerBundles.tsx'))).toBe(true)
  })
})

// The cart badge in the header and the cart page itself share the ['cart'] key.
//
// react-query stores ONE value per key, and `useQuery` treats a cache hit as
// success without ever running its own queryFn. The two consumers therefore have to
// agree on what that value IS. They did not: the header cached the lines ARRAY and
// the page cached an OBJECT, so the page read `data.lines` off an array, got
// undefined, and rendered "Keranjangmu masih kosong." -- on the same page, at the
// same moment, where the header badge read the very same cache entry and showed 1.
//
// This is narrow and exact on purpose: a general collision detector was tried and
// deleted for crying wolf (see the note above).
describe('the header badge and the cart page share one cart cache entry', () => {
  const header = readFileSync(join(process.cwd(), 'src/components/layout/Header.tsx'), 'utf-8')
  const cartPage = readFileSync(join(process.cwd(), 'src/pages/CartPage.tsx'), 'utf-8')

  it('both cache the response envelope rather than a reshaped subset', () => {
    expect(header).toMatch(/queryKey:\s*\[\s*'cart'\s*\]/)
    expect(cartPage).toMatch(/const CART_KEY = \['cart'\]/)

    // The bug: one side returned the lines and the other returned an object.
    expect(header).not.toMatch(/return res\.data\.lines/)
    expect(cartPage).not.toMatch(/lines:\s*\(res\.lines \?\? \[\]\)/)
    expect(cartPage).not.toMatch(/freeShip:\s*res\.free_shipping/)
  })

  it('both read the same fields off that envelope', () => {
    expect(header).toMatch(/cartData\?\.lines/)
    expect(cartPage).toMatch(/cartQuery\.data\?\.lines/)
  })
})
