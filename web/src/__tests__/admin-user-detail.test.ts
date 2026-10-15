import { readFileSync } from 'node:fs'
import { join } from 'node:path'
import { describe, expect, it } from 'vitest'

const src = readFileSync(join(__dirname, '..', 'pages', 'admin', 'AdminUsers.tsx'), 'utf8')

describe('admin user detail', () => {
  // GET /admin/users/{id} returned activity stats that nothing in the UI ever
  // fetched: the list showed the account and never what it had done.
  it('fetches the detail endpoint it claims to', () => {
    expect(src).toContain('`/admin/users/${detailFor}`')
  })

  // One query for the row that was opened, not one per row. `/admin/users` can be
  // 20+ rows, and a query without an `enabled` guard would fire on every render
  // with a null id -- a request per row for data nobody opened.
  it('does not fetch for every row', () => {
    expect(src).toContain('enabled: !!detailFor')
    // And the key includes the id, so opening a different user refetches rather
    // than serving the previous user's stats from cache.
    expect(src).toContain("queryKey: ['admin-user-detail', detailFor]")
  })

  // The stats are the point. If the endpoint's fields and the display drift, the
  // panel shows blanks and looks like a user with no activity -- which is a
  // different, and wrong, answer.
  it('shows every stat the endpoint returns', () => {
    for (const field of [
      'order_count',
      'total_spent',
      'ticket_count',
      'return_count',
      'wallet_balance',
    ]) {
      expect(src, `the panel does not show ${field}`).toContain(`detail.${field}`)
    }
  })
})
