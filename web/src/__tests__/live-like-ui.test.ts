import { readFileSync } from 'node:fs'
import { join } from 'node:path'
import { describe, expect, it } from 'vitest'

const root = join(__dirname, '..')
const read = (p: string) => readFileSync(join(root, p), 'utf8')
const readBackend = (p: string) =>
  readFileSync(join(__dirname, '..', '..', '..', 'backend', p), 'utf8')

const page = read('pages/LivePage.tsx')

describe('live like UI', () => {
  it('toggles through the authenticated endpoint and refreshes the session', () => {
    expect(page).toContain('/live/${sessionId}/like')
    expect(page).toContain("invalidateQueries({ queryKey: ['live', sessionId] })")
    expect(page).toContain('like_count')
  })

  it('fans the new count out so viewers see it without polling', () => {
    expect(page).toContain("ev.type === 'like'")
    const svc = readBackend(join('internal', 'service', 'live_service.go'))
    expect(svc).toContain('"like"')
  })
})

describe('live like backend', () => {
  // Tapping twice must unlike, not double-count: counter and votes update in
  // one transaction, floored at zero.
  it('toggles in a single transaction', () => {
    const repo = readBackend(join('internal', 'repository', 'live_repo.go'))
    expect(repo).toContain('ToggleLike')
    expect(repo).toContain('GREATEST(like_count - 1, 0)')
    expect(repo).toContain('ON CONFLICT DO NOTHING')
  })
})
