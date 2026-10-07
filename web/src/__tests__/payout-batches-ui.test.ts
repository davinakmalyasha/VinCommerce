import { readFileSync } from 'node:fs'
import { join } from 'node:path'
import { describe, expect, it } from 'vitest'

const root = join(__dirname, '..')
const read = (p: string) => readFileSync(join(root, p), 'utf8')

describe('payout-batch UI', () => {
  // Wired to a route and reachable from the nav. A page that exists but is not
  // registered is a page no operator can open -- which is exactly the defect this
  // series started from (endpoints with no UI at all).
  it('is reachable from the admin nav', () => {
    expect(read('router.tsx')).toContain("path: 'payout-batches'")
    expect(read('router.tsx')).toContain("import('./pages/admin/AdminPayoutBatches')")
    expect(read('pages/admin/AdminLayout.tsx')).toContain("to: '/admin/payout-batches'")
  })

  // THE invariant the backend enforces.
  //
  // PayoutRemittanceCSV refuses a batch that is not approved, so an unattended
  // job cannot produce a payment instruction. If the UI offered the button on a
  // draft batch anyway, the operator would click a plausible-looking control and
  // get an error -- and would reasonably conclude the feature is broken rather
  // than that the batch needed approving first.
  it('does not offer remittance for a batch the backend would refuse', () => {
    const src = read('pages/admin/AdminPayoutBatches.tsx')
    const at = src.indexOf('const canRemit')
    expect(at, 'canRemit helper is missing').toBeGreaterThan(-1)
    const body = src.slice(at, src.indexOf('\n', src.indexOf('\n', at) + 1) + 1)

    expect(body, 'canRemit must exclude draft').toMatch(/'draft'/)
    expect(body, 'canRemit must exclude cancelled').toMatch(/'cancelled'/)
    expect(body).toContain('!== ')
    // The button itself must be gated on canRemit, not rendered unconditionally.
    expect(src).toContain('canRemit(b.status)')
  })

  // A bare <a href> cannot send the Bearer header, so an admin CSV link 401s for
  // anyone who does not also hold the refresh cookie. Every authenticated file in
  // this app goes through downloadFile, which fetches the blob and revokes it.
  it('downloads the remittance through downloadFile, not a bare link', () => {
    const src = read('pages/admin/AdminPayoutBatches.tsx')
    expect(src).toContain('downloadFile(')
    // No anchor wired straight to a URL: that is the shape that 401s.
    expect(src).not.toMatch(/<a\s[^>]*href=\{[`'"][^`'"]*remittance/i)
  })

  // The batch endpoint returns `{ batches }`; a wrong key is an empty list that
  // reads as "there are no batches", which is the same lie the backend's BAD_STATUS
  // guard exists to avoid.
  it('reads the batches key the API actually returns', () => {
    const src = read('pages/admin/AdminPayoutBatches.tsx')
    expect(src).toContain('batches: PayoutBatch[]')
    expect(src).toContain('.data.batches')
  })
})
