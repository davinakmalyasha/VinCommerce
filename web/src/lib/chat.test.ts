import { describe, expect, it } from 'vitest'
import { hasUnreadAgentReply } from './chat'

const AGENT_AT = '2026-11-02T10:00:00Z'

describe('hasUnreadAgentReply', () => {
  it('dots an agent reply newer than the last panel open', () => {
    expect(
      hasUnreadAgentReply(
        [{ id: 's1', status: 'open', last_message_at: AGENT_AT, last_message_role: 'agent' }],
        Date.parse('2026-11-02T09:00:00Z'),
      ),
    ).toBe(true)
  })

  it('clears once the panel has been opened after the reply', () => {
    expect(
      hasUnreadAgentReply(
        [{ id: 's1', status: 'open', last_message_at: AGENT_AT, last_message_role: 'agent' }],
        Date.parse('2026-11-02T10:05:00Z'),
      ),
    ).toBe(false)
  })

  it('ignores closed sessions, customer messages, and missing timestamps', () => {
    const sessions = [
      { id: 's1', status: 'closed', last_message_at: AGENT_AT, last_message_role: 'agent' },
      { id: 's2', status: 'open', last_message_at: AGENT_AT, last_message_role: 'customer' },
      { id: 's3', status: 'open', last_message_at: AGENT_AT, last_message_role: 'system' },
      { id: 's4', status: 'open', last_message_at: null, last_message_role: 'agent' },
      { id: 's5', status: 'open', last_message_at: 'not-a-date', last_message_role: 'agent' },
    ]
    expect(hasUnreadAgentReply(sessions, 0)).toBe(false)
  })

  it('is false with no sessions', () => {
    expect(hasUnreadAgentReply([], 0)).toBe(false)
    expect(hasUnreadAgentReply(null, 0)).toBe(false)
    expect(hasUnreadAgentReply(undefined, 0)).toBe(false)
  })
})
