import { readFileSync } from 'node:fs'
import { join } from 'node:path'
import { describe, expect, it } from 'vitest'

const root = join(__dirname, '..')
const read = (p: string) => readFileSync(join(root, p), 'utf8')
const readBackend = (p: string) =>
  readFileSync(join(__dirname, '..', '..', '..', 'backend', p), 'utf8')

const page = read('pages/MyReturnsPage.tsx')

describe('return withdraw UI', () => {
  it('offers withdrawal only on undecided claims with confirmation', () => {
    expect(page).toContain('Batalkan pengajuan ini')
    expect(page).toContain('confirmWithdraw')
    expect(page).toContain('Ya, batalkan')
  })

  it('withdraws through DELETE /returns/{id}', () => {
    expect(page).toContain('api.delete(`/returns/${returnId}`)')
    const router = readBackend(join('internal', 'httpapi', 'router.go'))
    expect(router).toContain('r.Delete("/{id}", buyerReturns.Withdraw)')
  })
})
