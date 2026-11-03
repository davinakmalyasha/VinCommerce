import { readFileSync } from 'node:fs'
import { join } from 'node:path'
import { describe, expect, it } from 'vitest'

const root = join(__dirname, '..')
const read = (p: string) => readFileSync(join(root, p), 'utf8')

const account = read('pages/AccountPage.tsx')

describe('device sessions UI', () => {
  it('lists sessions from the existing backend endpoint', () => {
    expect(account).toContain("queryKey: ['auth-sessions']")
    expect(account).toContain('/auth/sessions')
  })

  it('revokes one session without touching others', () => {
    expect(account).toContain('/auth/sessions/revoke')
    expect(account).toContain('session_id: sessionId')
    expect(account).toContain("invalidateQueries({ queryKey: ['auth-sessions'] })")
    // Revoke posts a single id; logout-all is the separate bulk path.
    const start = account.indexOf('revokeSession = useMutation')
    const end = account.indexOf('logoutAll = useMutation', start)
    expect(start).toBeGreaterThan(-1)
    expect(end).toBeGreaterThan(start)
    const block = account.slice(start, end)
    expect(block).not.toContain('/auth/logout-all')
  })

  it('offers logout-all as a separate explicit action', () => {
    expect(account).toContain('/auth/logout-all')
    expect(account).toContain('Akhiri semua sesi lain')
  })
})
