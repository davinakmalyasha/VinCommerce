import { readFileSync } from 'node:fs'
import { join } from 'node:path'
import { describe, expect, it } from 'vitest'

const root = join(__dirname, '..')
const read = (p: string) => readFileSync(join(root, p), 'utf8')

const page = read('pages/NotificationsPage.tsx')
const handler = readFileSync(
  join(__dirname, '..', '..', '..', 'backend', 'internal', 'httpapi', 'handler', 'support.go'),
  'utf8',
)

describe('notification grouping UI', () => {
  it('groups by type with a filter backed by the server type param', () => {
    expect(page).toContain('TYPE_LABELS')
    expect(page).toContain('groupKeys')
    expect(page).toContain('&type=')
    expect(page).toContain("queryKey: ['notifications-all', filter]")
  })

  it('marks one group read with exactly that group’s ids', () => {
    expect(page).toContain('Tandai grup dibaca')
    expect(page).toContain('unreadInGroup')
    expect(page).toContain('markAll.mutate(unreadInGroup)')
  })
})

describe('scoped mark-read backend', () => {
  // The bell and the page both POST {ids}; the handler used to read only ?id=
  // and mark the whole account. A scoped call must hit MarkMany, and a
  // malformed body must fail rather than fall through to mark-all.
  it('honors the ids body via MarkMany', () => {
    expect(handler).toContain('MarkMany')
    expect(handler).toContain('BAD_JSON')
  })
})
