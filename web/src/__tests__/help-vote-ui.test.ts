import { readFileSync } from 'node:fs'
import { join } from 'node:path'
import { describe, expect, it } from 'vitest'

const root = join(__dirname, '..')
const read = (p: string) => readFileSync(join(root, p), 'utf8')

const page = read('pages/HelpArticlePage.tsx')

describe('help article vote UI', () => {
  it('votes through the authenticated endpoint and refreshes the article', () => {
    expect(page).toContain('/help/articles/${slug}/helpful')
    expect(page).toContain("invalidateQueries({ queryKey: ['help-article', slug] })")
  })

  it('gates voting behind login instead of failing silently', () => {
    expect(page).toContain('Masuk')
    expect(page).toContain('helpful_count')
  })
})
