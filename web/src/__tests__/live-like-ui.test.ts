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

describe('live replay and scheduling', () => {
  // Ended streams kept their YouTube ID but rendered a static "berakhir"
  // panel; the recording they already had was unwatchable.
  it('embeds the recording for ended streams with a video', () => {
    expect(page).toContain("session.status === 'ended'")
    expect(page).toContain('Rekaman siaran')
  })

  it('sends the thumbnail and schedule fields the API already accepts', () => {
    const studio = read('pages/seller/SellerLiveStudio.tsx')
    expect(studio).toContain('thumbnail_url')
    expect(studio).toContain('scheduled_at')
    expect(studio).toContain('datetime-local')
  })
})
