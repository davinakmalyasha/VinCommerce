import { readFileSync } from 'node:fs'
import { join } from 'node:path'
import { describe, expect, it } from 'vitest'

const src = readFileSync(join(__dirname, '..', 'pages', 'admin', 'AdminOpsPages.tsx'), 'utf8')

function adminOrders(): string {
  const at = src.indexOf('export function AdminOrders()')
  expect(at, 'AdminOrders is gone').toBeGreaterThan(-1)
  const end = src.indexOf('\nexport function ', at + 1)
  return end > 0 ? src.slice(at, end) : src.slice(at)
}

describe('admin escrow release', () => {
  // The endpoint existed and NOTHING called it, so an order whose automatic
  // release failed sat at `delivered` for ever with no way for an operator to
  // clear it. Reaching it at all is the point.
  it('calls the real endpoint', () => {
    expect(adminOrders()).toContain('`/payments/orders/${orderId}/release`')
  })

  // Releasing escrow PAYS the seller and cannot be undone. This is an admin's
  // retry lever, not a way to pay early, so it is offered only where the release
  // is supposed to have happened already.
  it('is offered only on delivered orders', () => {
    const body = adminOrders()
    const at = body.indexOf('Lepas escrow')
    expect(at, 'the release button is gone').toBeGreaterThan(-1)

    // The button must be inside a `status === 'delivered'` guard. Scoped to the
    // block that renders it rather than the whole file, so a guard elsewhere
    // cannot satisfy this.
    const before = body.slice(0, at)
    const guardAt = before.lastIndexOf("o.status === 'delivered'")
    expect(guardAt, 'the release button is not guarded on order status').toBeGreaterThan(-1)
    // And the guard must be recent -- not from an unrelated earlier row.
    expect(at - guardAt, 'the status guard is too far from the button to be its guard')
      .toBeLessThan(2000)
    for (const other of ['pending', 'paid', 'shipped', 'completed']) {
      expect(before.slice(guardAt), `release is offered on ${other}`)
        .not.toContain(`o.status === '${other}'`)
    }
  })

  // One click would move money. Confirmed, like buying a carrier label.
  it('needs a confirmation step before it fires', () => {
    const body = adminOrders()
    expect(body).toContain('setConfirmRelease(')
    expect(body).toContain('Tidak bisa dibatalkan')

    // The meaningful check, and it took a surviving mutant to find it: assert the
    // HANDLER that enters the confirmation does not itself fire the release.
    //
    // Two earlier versions of this were positional and both were wrong. Comparing
    // the first button's offset against the mutate call's offset checks JSX layout,
    // because the else-branch is written after the confirm-branch -- and a mutant
    // that made the first button call `release.mutate` directly still passed, since
    // its mutate call landed after the guard in the file.
    const enterAt = body.indexOf('setConfirmRelease(o.id)')
    expect(enterAt, 'nothing enters the confirmation state any more').toBeGreaterThan(-1)
    const handler = body.slice(enterAt, body.indexOf('\n', enterAt))
    expect(handler, 'the button that asks for confirmation fires the release itself')
      .not.toContain('release.mutate')

    // And the mutation must still sit inside the confirmed branch.
    const guardAt = body.indexOf('confirmRelease === o.id')
    const mutateAt = body.indexOf('release.mutate(o.id)')
    expect(guardAt, 'the confirmation guard is gone').toBeGreaterThan(-1)
    expect(mutateAt, 'the release is never called').toBeGreaterThan(-1)
    expect(mutateAt, 'the mutation fires outside the confirmation branch')
      .toBeGreaterThan(guardAt)
  })

  // A failed release must be visible and must reset the confirmation, or the
  // operator is left with a button that appears to have done nothing.
  it('surfaces a failure and clears the confirmation', () => {
    const body = adminOrders()
    expect(body).toContain('setError(e.message)')
    expect(body).toContain('role="alert"')
    const onErrorAt = body.indexOf('onError:')
    expect(onErrorAt).toBeGreaterThan(-1)
    expect(body.slice(onErrorAt, onErrorAt + 200)).toContain('setConfirmRelease(null)')
  })
})
