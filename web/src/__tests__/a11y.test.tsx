import { describe, expect, it } from 'vitest'
import axe from 'axe-core'
import { Modal } from '../components/Modal'
import { QueryState } from '../components/QueryState'
import { render } from '../test/render'

/**
 * AN AUTOMATED ACCESSIBILITY GATE, which this project did not have.
 *
 * The backlog recorded that modals, forms and tables had been brought up to
 * keyboard/screen-reader basics "but there is no automated a11y gate in CI" --
 * meaning every one of those improvements could regress silently, and the only
 * thing standing between a missing label and production was somebody noticing.
 *
 * axe over jsdom is not the whole story. There is no layout and no real assistive
 * technology, so it cannot see focus order, reading order or whether an aria-label
 * is USEFUL. It reliably catches the mechanical half: missing accessible names,
 * bad roles, invalid aria attributes, images without alt text.
 *
 * That half is worth a gate precisely because it is mechanical and easy to lose in
 * a refactor. What it cannot check is recorded in docs/BACKLOG.md rather than
 * implied to be covered by this file.
 */

/** Rules that need real layout, so jsdom can only produce false positives. */
const NEEDS_LAYOUT = {
  // Computed colours require a layout engine; jsdom returns defaults for
  // everything, so every element "fails" contrast for reasons that are not real.
  'color-contrast': { enabled: false },
  // Same reason: these measure rendered geometry.
  'target-size': { enabled: false },
  'scrollable-region-focusable': { enabled: false },
} as const

async function violations(): Promise<string[]> {
  const results = await axe.run(document.body, { rules: NEEDS_LAYOUT as never })
  return results.violations.map(
    (v) => `${v.id}: ${v.help} [${v.impact}] -- ${v.nodes.length} node(s)`,
  )
}

describe('accessibility gate', () => {
  it('a modal with a form has no mechanical violations', async () => {
    const r = await render(
      <>
        <button type="button">Buka</button>
        <Modal open onClose={() => {}} title="Laporkan Produk" description="Pilih alasan">
          <label htmlFor="a11y-alasan">Alasan</label>
          <input id="a11y-alasan" />
          <button type="button">Kirim</button>
        </Modal>
      </>,
    )
    const found = await violations()
    await r.unmount()
    expect(found, `axe violations:\n${found.join('\n')}`).toEqual([])
  })

  it('a query state with an error has no mechanical violations', async () => {
    // QueryState takes the react-query result object, not individual props.
    const failed = {
      isLoading: false,
      isError: true,
      error: new Error('gagal'),
      refetch: () => {},
    }
    const r = await render(
      <QueryState query={failed} label="produk">
        <p>tidak dipakai</p>
      </QueryState>
    )
    const found = await violations()
    await r.unmount()
    expect(found, `axe violations:\n${found.join('\n')}`).toEqual([])
  })

  // The gate is worthless if axe is not actually running. A misconfiguration that
  // returns no violations from a document that DEFINITELY has one would make every
  // assertion above pass for the wrong reason -- which is the failure mode this
  // whole project keeps finding in its own checkers.
  it('actually detects a violation when there is one', async () => {
    const r = await render(
      <div>
        {/* An image with no alt text is the canonical mechanical violation. */}
        {/* eslint-disable-next-line jsx-a11y/alt-text */}
        <img src="data:image/gif;base64,R0lGODlhAQABAAAAACw=" />
      </div>,
    )
    const found = await violations()
    await r.unmount()
    expect(found.length, 'axe found nothing in a document with a known violation, ' +
      'so the assertions above prove nothing').toBeGreaterThan(0)
  })
})
