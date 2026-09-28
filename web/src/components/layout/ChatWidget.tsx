import { useEffect, useRef, useState } from 'react'
import { useMutation, useQuery, useQueryClient } from '@tanstack/react-query'
import { api } from '../../lib/api'
import { useSession } from '../../stores/session'
import { formatDate } from '../../lib/format'

interface ChatMessage {
  id: number
  session_id: string
  sender_role: string
  sender_name: string
  body: string
  created_at: string
}

interface AiAnswer {
  answer: string
  mode: string
  sources?: { title: string }[]
}

export function ChatWidget() {
  const { user } = useSession()
  const queryClient = useQueryClient()
  const [open, setOpen] = useState(false)
  const [sessionId, setSessionId] = useState<string | null>(null)
  const [input, setInput] = useState('')
  const [aiMode, setAiMode] = useState(true)
  const [thinking, setThinking] = useState(false)
  const bottomRef = useRef<HTMLDivElement>(null)

  const { data: sessions } = useQuery({
    queryKey: ['chat-sessions'],
    queryFn: async () => (await api.get<{ sessions: { id: string; status: string }[] }>('/chat/sessions')).data.sessions,
    enabled: !!user && open,
  })

  const { data: messages } = useQuery({
    queryKey: ['chat-messages', sessionId],
    queryFn: async () => (await api.get<{ messages: ChatMessage[] }>(`/chat/sessions/${sessionId}`)).data.messages,
    enabled: !!user && open && !!sessionId,
  })

  useEffect(() => {
    bottomRef.current?.scrollIntoView({ behavior: 'smooth' })
  }, [messages, thinking])

  const openSession = useMutation({
    mutationFn: async () => {
      const res = await api.post<{ session: { id: string } }>('/chat/sessions', { source: 'widget' })
      return res.data.session.id
    },
    onSuccess: (id) => {
      setSessionId(id)
      setAiMode(false)
    },
  })

  const send = useMutation({
    mutationFn: async () => {
      await api.post(`/chat/sessions/${sessionId}/messages`, { body: input })
      setInput('')
    },
    onSuccess: () => queryClient.invalidateQueries({ queryKey: ['chat-messages', sessionId] }),
  })

  const askAi = useMutation({
    mutationFn: async () => {
      setThinking(true)
      try {
        const res = await api.post<AiAnswer>('/ai/ask', { question: input })
        setInput('')
        return res.data
      } finally {
        setThinking(false)
      }
    },
  })

  if (!user) return null

  return (
    <>
      <button
        type="button"
        onClick={() => setOpen(!open)}
        className="fixed bottom-6 right-4 sm:right-6 z-50 w-14 h-14 rounded-full bg-amber-500 text-white shadow-lg hover:bg-amber-600 flex items-center justify-center text-2xl"
        title="Bantuan"
        aria-expanded={open}
        aria-haspopup="dialog"
      >
        {open ? '✕' : '💬'}
      </button>


      {open && (
        // `w-96` + `right-6` is 408px, wider than a 375px viewport: the panel
        // hung off the right edge and the close button was unreachable.
        <div className="fixed bottom-24 right-4 sm:right-6 z-50 w-[calc(100vw-2rem)] sm:w-96 max-w-full h-[min(32rem,70vh)] bg-white dark:bg-gray-900 rounded-2xl shadow-2xl border border-gray-200 dark:border-gray-700 flex flex-col overflow-hidden">

          <div className="bg-amber-500 text-white px-4 py-3 flex items-center justify-between">
            <div>
              <p className="font-bold text-sm">Bantuan VinCommerce</p>
              <p className="text-xs text-amber-100">
                {aiMode ? 'Asisten AI · coba tanya apa saja' : 'Chat dengan agen'}
              </p>
            </div>
            <div className="flex items-center gap-2 text-xs">
              <button
                type="button"
                onClick={() => setAiMode(true)}
                className={`px-2 py-1 rounded-lg ${aiMode ? 'bg-white text-amber-600' : 'hover:bg-amber-600'}`}
              >
                🤖 AI
              </button>
              <button
                type="button"
                onClick={() => {

                  if (sessionId) {
                    setAiMode(false)
                  } else {
                    openSession.mutate()
                  }
                }}
                className={`px-2 py-1 rounded-lg ${!aiMode ? 'bg-white text-amber-600' : 'hover:bg-amber-600'}`}
              >
                👤 Agen
              </button>
            </div>
          </div>

          <div className="flex-1 overflow-y-auto p-4 space-y-2 bg-gray-50 dark:bg-gray-800">
            {aiMode && (
              <div className="bg-white dark:bg-gray-900 border border-gray-200 dark:border-gray-700 rounded-xl p-3 text-sm text-gray-600 dark:text-gray-300">
                Halo! Saya asisten AI VinCommerce. Tanyakan apa saja tentang pesanan, escrow, retur, atau pengiriman.
              </div>
            )}
            {!aiMode && messages?.map((m) => (
              <div
                key={m.id}
                className={`max-w-[85%] rounded-xl p-3 text-sm ${
                  m.sender_role === 'customer'
                    ? 'ml-auto bg-amber-500 text-white'
                    : m.sender_role === 'system'
                      ? 'mx-auto bg-gray-200 dark:bg-gray-700 text-gray-600 dark:text-gray-300 text-xs'
                      : 'bg-white dark:bg-gray-900 border border-gray-200 dark:border-gray-700'
                }`}
              >
                {m.body}
                <p className="text-[10px] opacity-70 mt-1">{formatDate(m.created_at)}</p>
              </div>
            ))}
            {!aiMode && !sessionId && (
              <p className="text-center text-xs text-gray-400 py-10">
                {sessions?.some((s) => s.status === 'open')
                  ? 'Kamu punya sesi chat aktif.'
                  : 'Klik tombol agen untuk memulai chat.'}
              </p>
            )}
            {askAi.data && (
              <div className="bg-white dark:bg-gray-900 border border-gray-200 dark:border-gray-700 rounded-xl p-3 text-sm">
                <p className="text-xs text-gray-400 mb-1">
                  🤖 {askAi.data.mode === 'llm' ? 'AI (LLM)' : 'AI'} {askAi.data.sources?.length ? `· ${askAi.data.sources.length} sumber` : ''}
                </p>
                <p className="whitespace-pre-line">{askAi.data.answer}</p>
                {askAi.data.sources && askAi.data.sources.length > 0 && (
                  <div className="mt-2 space-y-1">
                    {askAi.data.sources.map((s, i) => (
                      <p key={i} className="text-xs text-amber-600">📄 {s.title}</p>
                    ))}
                  </div>
                )}
                <button
                  type="button"
                  onClick={() => openSession.mutate()}
                  className="mt-2 text-xs text-amber-600 hover:underline"
                >

                  Masih butuh bantuan? Chat dengan agen →
                </button>
              </div>
            )}
            {thinking && <p className="text-xs text-gray-400">AI sedang berpikir...</p>}
            <div ref={bottomRef} />
          </div>

          <div className="p-3 border-t border-gray-200 dark:border-gray-700 flex gap-2">
            <label htmlFor="chat-input" className="sr-only">Pesan</label>
            <input
              id="chat-input"
              value={input}
              onChange={(e) => setInput(e.target.value)}
              onKeyDown={(e) => {
                if (e.key === 'Enter' && input.trim()) {
                  if (aiMode) askAi.mutate()
                  else if (sessionId) send.mutate()
                }
              }}
              placeholder={aiMode ? 'Tanya AI...' : 'Tulis pesan...'}
              className="flex-1 min-w-0 px-3 py-2 border rounded-xl text-sm outline-none focus:border-amber-400 dark:bg-gray-800 dark:border-gray-600"
            />
            <button
              type="button"
              onClick={() => {
                if (aiMode) askAi.mutate()
                else if (sessionId) send.mutate()
              }}
              disabled={!input.trim() || (!aiMode && !sessionId)}
              className="px-4 py-2 rounded-xl bg-amber-500 text-white text-sm disabled:opacity-40"
            >
              Kirim
            </button>
          </div>

        </div>
      )}
    </>
  )
}

