import { readFileSync } from 'node:fs'
import { join } from 'node:path'
import { describe, expect, it } from 'vitest'

const src = readFileSync(join(__dirname, '..', 'pages', 'seller', 'SellerProducts.tsx'), 'utf8')

describe('seller CSV import template', () => {
  // GET /seller/products/import-template existed and nothing could reach it, so
  // the import UI asked sellers to produce a CSV whose columns were documented
  // only in the handler.
  it('offers the template the backend actually serves', () => {
    expect(src).toContain('/seller/products/import-template')
  })

  // The route sits behind the seller auth group, and a bare <a href> cannot send
  // the Bearer header -- it would 401 for anyone without the refresh cookie.
  it('downloads it authenticated, not through a bare link', () => {
    expect(src).toContain('downloadFile(')
    expect(src).toContain("from '../../lib/api'")
    expect(src).not.toMatch(/<a\s[^>]*href=\{[`'"][^`'"]*import-template/i)
  })

  // The template's own header is only documentation: the importer reads columns
  // POSITIONALLY and skips row 0 via `HasPrefix(records[0][0], "name")`. If the
  // served header stopped starting with "name", the header row would be imported
  // as a product instead of skipped -- so the two ends have to agree.
  it('keeps the header the importer knows how to skip', () => {
    const handler = readFileSync(
      join(__dirname, '..', '..', '..', 'backend', 'internal', 'httpapi', 'handler', 'seller.go'),
      'utf8',
    )
    const at = handler.indexOf('func (h *Seller) ImportTemplate(')
    expect(at, 'ImportTemplate is gone').toBeGreaterThan(-1)
    const body = handler.slice(at, at + 400)

    const header = body.match(/w\.Write\(\[\]byte\("([a-z_,]+)\\n/)
    expect(header, 'the template has no header row').not.toBeNull()
    expect(header![1].split(',')[0], 'the template header no longer starts with "name", ' +
      'so the importer will import it as a product').toBe('name')

    // And the importer must still skip on that prefix.
    expect(handler).toContain('strings.HasPrefix(strings.ToLower(records[0][0]), "name")')
  })
})
