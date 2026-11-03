import { readFileSync } from 'node:fs'
import { join } from 'node:path'
import { describe, expect, it } from 'vitest'

const root = join(__dirname, '..')
const read = (p: string) => readFileSync(join(root, p), 'utf8')
const readBackend = (p: string) =>
  readFileSync(join(__dirname, '..', '..', '..', 'backend', p), 'utf8')

const contact = read('pages/ContactPage.tsx')
const detail = read('pages/TicketDetailPage.tsx')
const admin = read('pages/admin/AdminTickets.tsx')
const console_ = read('pages/support/SupportConsole.tsx')

describe('ticket attachments UI', () => {
  it('lets buyers attach photos when opening and replying', () => {
    expect(contact).toContain('attachments')
    expect(contact).toContain('attachments.length < 3')
    expect(detail).toContain('attachments')
    expect(detail).toContain('Lampiran tiket')
  })

  it('shows attachments to staff in the admin queue', () => {
    expect(admin).toContain('m.attachments')
    expect(admin).toContain('Lampiran tiket')
  })

  it('validates attachments like return evidence, not as free text', () => {
    const svc = readBackend(join('internal', 'service', 'support_service.go'))
    expect(svc).toContain('NormalizeEvidenceURLs')
  })

  it('rates only resolved tickets with a 1-5 score', () => {
    const svc = readBackend(join('internal', 'service', 'support_service.go'))
    expect(svc).toContain('RateTicket')
    expect(svc).toContain('NOT_RESOLVED')
    const page = read('pages/TicketDetailPage.tsx')
    expect(page).toContain('/rating')
    expect(page).toContain('Nilai ${s} dari 5')
  })
})

describe('staff reply wiring', () => {
  // The handler decodes {body, attachments, internal} with DisallowUnknownFields,
  // so a console posting is_internal failed every reply with BAD_JSON.
  it('posts the field name the handler decodes', () => {
    expect(console_).toContain('{ body, internal }')
    expect(console_).not.toContain('is_internal: internal')
  })

  it('offers self-service actions for the linked order', () => {
    const contact = read('pages/ContactPage.tsx')
    expect(contact).toContain('Banyak masalah selesai tanpa tiket')
    expect(contact).toContain('/tracking/${selected.tracking_number}')
    expect(contact).toContain('openDocument(`/orders/${selected.id}/invoice`)')
  })

  it('shows the SLA deadline and flags overdue tickets', () => {
    for (const p of ['pages/MyTicketsPage.tsx', 'pages/TicketDetailPage.tsx', 'pages/admin/AdminTickets.tsx']) {
      expect(read(p)).toContain('sla_due_at')
    }
    expect(read('pages/MyTicketsPage.tsx')).toContain('Terlambat')
    expect(read('pages/admin/AdminTickets.tsx')).toContain('SLA lewat')
    const svc = readBackend(join('internal', 'service', 'support_service.go'))
    expect(svc).toContain('slaDueFor')
  })
})
