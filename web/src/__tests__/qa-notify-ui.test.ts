import { readFileSync } from 'node:fs'
import { join } from 'node:path'
import { describe, expect, it } from 'vitest'

const root = join(__dirname, '..')
const read = (p: string) => readFileSync(join(root, p), 'utf8')
const readBackend = (p: string) =>
  readFileSync(join(__dirname, '..', '..', '..', 'backend', p), 'utf8')

describe('Q&A notifications', () => {
  // The seller inbox and the product Q&A list existed, but neither side was
  // ever pinged: questions waited unread, answers waited unseen.
  it('notifies the seller on ask and the asker on answer', () => {
    const svc = readBackend(join('internal', 'service', 'market_service.go'))
    expect(svc).toContain('SellerForProduct')
    expect(svc).toContain('"qa"')
    expect(svc).toContain('Pertanyaan baru di produkmu')
    expect(svc).toContain('Penjual menjawab pertanyaanmu')
  })

  it('never fails the question or answer when notify fails', () => {
    const svc = readBackend(join('internal', 'service', 'market_service.go'))
    const askStart = svc.indexOf('func (s *MarketService) AskQuestion(')
    const askEnd = svc.indexOf('// AnswerQuestion records', askStart)
    expect(svc.slice(askStart, askEnd)).toContain('_ = s.notifs.Notify')
    expect(svc).toContain('if s.notifs != nil {')
  })

  it('is mutable in prefs and grouped in the feed', () => {
    const repo = readBackend(join('internal', 'repository', 'notification_repo.go'))
    expect(repo).toContain('"qa",')
    expect(read('components/NotificationPrefsCard.tsx')).toContain("qa: 'Tanya jawab produk'")
    expect(read('pages/NotificationsPage.tsx')).toContain("qa: 'Tanya jawab'")
  })
})
