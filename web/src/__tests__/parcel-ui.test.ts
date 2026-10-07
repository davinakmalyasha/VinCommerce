import { readFileSync } from 'node:fs'
import { join } from 'node:path'
import { describe, expect, it } from 'vitest'

const root = join(__dirname, '..')
const read = (p: string) => readFileSync(join(root, p), 'utf8')

const orders = read('pages/seller/SellerOrders.tsx')
const panel = read('pages/seller/SellerParcelPanel.tsx')
const returns = read('pages/seller/SellerReturns.tsx')

describe('seller parcel UI', () => {
  // Seven seller endpoints had no UI at all. A component nobody imports is the same
  // thing as no component, so the wiring is part of what has to hold.
  it('is rendered from the orders page', () => {
    expect(orders).toContain("import { SellerParcelPanel } from './SellerParcelPanel'")
    expect(orders).toContain('<SellerParcelPanel')
    expect(orders).toContain('orderId={o.id}')
    expect(orders).toContain('setParcelFor(')
  })

  // BUYING A LABEL SPENDS MONEY AND IS OFTEN IRREVERSIBLE AT THE CARRIER.
  //
  // Handing the box over is free. If the two were one button, a seller who bought a
  // label and then could not get the parcel to the depot has paid for nothing.
  //
  // Pinned three ways, because any one of them alone is easy to break:
  //   - label and dispatch are separate endpoints
  //   - the label button is gated behind a confirmation state
  //   - dispatch never mentions /label
  it('keeps label purchase separate from dispatch', () => {
    expect(panel).toContain('/seller/parcels/${id}/label')
    expect(panel).toContain('/seller/parcels/${id}/dispatch')
    expect(panel).toContain('setConfirmLabel(')
    expect(panel).toContain('confirmLabel === p.id')

    const dStart = panel.indexOf('const dispatch = useMutation')
    // Bounded on the next declaration, not `})` -- the parameter list contains `})`
    // and would have made this an empty window, i.e. an absence check that passes
    // unconditionally.
    const dEnd = panel.indexOf('const totalQty', dStart)
    expect(dStart).toBeGreaterThan(-1)
    expect(dEnd).toBeGreaterThan(dStart)
    const dispatchBlock = panel.slice(dStart, dEnd)
    expect(dispatchBlock, 'dispatch must not also buy a label').not.toContain('/label')
  })

  // The server rejects a quantity over the order line. A form that lets you type 99
  // into a field whose max is 3 and then bounces the request is a form with a bug in
  // it, and the bug reads as "the app is broken".
  it('clamps the quantity to what was ordered', () => {
    expect(panel).toContain('Math.min(it.quantity')
    expect(panel).toContain('max={it.quantity}')
    // And the empty parcel the server rejects outright is not submittable.
    expect(panel).toMatch(/disabled=\{create\.isPending \|\| totalQty === 0\}/)
  })

  // GET returns only the order being worked on, not one query per row on a page that
  // renders 20 orders.
  it('fetches parcels for the open order only', () => {
    expect(orders).toContain('{parcelFor === o.id && (')
    expect(panel).toContain(`queryKey: key`)
    expect(panel).toContain(`/seller/orders/\${orderId}/parcels`)
  })
})

describe('seller return UI', () => {
  // NoteReturnArrived moves return_requests.status and NOTHING ELSE -- no escrow
  // release, no journal, no wallet debit. The refund is a separate seller action.
  //
  // If this button were wired to a refund call, the UI would be promising the seller
  // something the endpoint deliberately does not do.
  it('marks arrival without touching money', () => {
    expect(returns).toContain('/seller/returns/${id}/arrived')
    // Bounded to the mutation itself. An earlier 700-char window overran into the
    // statusStyle map and matched `refunded:` there -- which would be my test lying
    // about the product, and exactly the failure mode this suite exists to avoid.
    const start = returns.indexOf('noteArrived = useMutation')
    // NOT indexOf('})': the parameter list `async ({ id }: { id: string }) =>` already
    // contains `})`, so that window ended before the body and would have let a
    // refund-calling mutant through. Bound on the next top-level declaration instead.
    const end = returns.indexOf('const statusStyle', start)
    expect(start).toBeGreaterThan(-1)
    expect(end).toBeGreaterThan(start)
    const arrivedBlock = returns.slice(start, end)
    expect(arrivedBlock, 'arrived must not also refund').not.toMatch(
      /refund|escrow|release/i,
    )
    // And the copy says so, so the seller does not read it as "this paid me back".
    expect(returns).toContain('hanya mencatat penerimaan barang')
  })

  // The seller issues the return label; the buyer creates the parcel. Offering the
  // seller a "create parcel" control would contradict the ownership split the
  // backend enforces and fail on submit.
  it('issues the label but never creates the return parcel', () => {
    expect(returns).toContain('/seller/returns/${id}/return-label')
    expect(returns).toContain('/seller/returns/${expanded}/parcel')
    expect(returns, 'return parcel is created by the buyer, not here').not.toMatch(
      /api\.post\(`\/seller\/returns\/\$\{[^}]+\}\/parcel`/,
    )
  })

  // A label URL is an absolute signed link for some carriers and a same-origin path
  // for others. Feeding an absolute URL to downloadFile prefixes it with the API base
  // and 404s.
  // Assert the BRANCH, not the regex's presence: `if (false && /^https?:\/\//...)`
  // contains the exact substring and is a dead branch, which is what a presence check
  // would happily pass. Match the call sitting immediately inside the `if (`.
  it('opens a label without mangling an absolute URL', () => {
    for (const src of [returns, panel]) {
      expect(src).toContain('window.open(')
      expect(src).toMatch(/if \(\s*\/\^https\?:\\\/\\\//)
    }
    expect(returns).not.toMatch(/if \(false/)
    expect(panel).not.toMatch(/if \(false/)
  })
})
