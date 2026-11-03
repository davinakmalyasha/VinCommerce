import { readFileSync } from 'node:fs'
import { join } from 'node:path'
import { describe, expect, it } from 'vitest'

const root = join(__dirname, '..')
const read = (p: string) => readFileSync(join(root, p), 'utf8')
const readBackend = (p: string) =>
  readFileSync(join(__dirname, '..', '..', '..', 'backend', p), 'utf8')

const page = read('pages/HistoryPage.tsx')

describe('browsing history UI', () => {
  it('reads the server history for members and keeps local for guests', () => {
    expect(page).toContain('/history?limit=30')
    expect(page).toContain('vc_recent')
    expect(page).toContain("queryKey: ['browsing-history']")
  })

  it('is reachable from the account and the router', () => {
    expect(read('router.tsx')).toContain("path: 'history'")
    expect(read('pages/AccountPage.tsx')).toContain("'/history'")
  })
})

describe('browsing history backend', () => {
  // product_views already logs user_id on every beacon; history is a read over
  // it, newest product first, with card fields joined in one query.
  it('serves recent views behind auth', () => {
    const repo = readBackend(join('internal', 'repository', 'analytics_repo.go'))
    expect(repo).toContain('RecentViews')
    expect(repo).toContain('GROUP BY')
    const router = readBackend(join('internal', 'httpapi', 'router.go'))
    expect(router).toContain('Get("/history", analyticsH.History)')
  })
})
