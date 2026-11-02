export interface ChatSessionSignal {
  id: string
  status: string
  last_message_at?: string | null
  last_message_role?: string | null
}

/**
 * Whether the chat button should show an unread dot.
 *
 * The server sends each session's newest message (role + timestamp); the
 * client remembers when the panel was last opened. An agent message newer
 * than that moment is "unread". Customer/system messages never count -- the
 * user wrote one side and the other is not a person waiting.
 *
 * Pure so the rule is unit-testable: the widget itself is a polling query
 * plus localStorage, neither of which belongs in an assertion.
 */
export function hasUnreadAgentReply(
  sessions: ChatSessionSignal[] | undefined | null,
  seenAtMs: number,
): boolean {
  if (!sessions) return false
  return sessions.some((s) => {
    if (s.status === 'closed') return false
    if (s.last_message_role !== 'agent') return false
    if (!s.last_message_at) return false
    const at = Date.parse(s.last_message_at)
    if (Number.isNaN(at)) return false
    return at > seenAtMs
  })
}

export const CHAT_SEEN_KEY = 'vc_chat_seen'

export function readChatSeenAt(): number {
  try {
    return Number(localStorage.getItem(CHAT_SEEN_KEY)) || 0
  } catch {
    return 0
  }
}

export function writeChatSeenAt(nowMs = Date.now()): void {
  try {
    localStorage.setItem(CHAT_SEEN_KEY, String(nowMs))
  } catch {
    // Private mode: the badge simply recomputes from epoch next load.
  }
}
