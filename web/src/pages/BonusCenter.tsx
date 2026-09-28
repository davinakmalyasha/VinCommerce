import { useState } from 'react'
import { useMutation, useQuery, useQueryClient } from '@tanstack/react-query'
import { api } from '../lib/api'

interface CheckInStatus {
  checked_in_today: boolean
  streak: number
  dates: string[]
}

interface SpinResult {
  type: 'coupon' | 'points'
  label: string
  points?: number
}

const SEGMENTS = ['+20', '+40', '+60', '+80', '+100', '🎁', '+30', '+50']

export function BonusCenter() {
  const queryClient = useQueryClient()
  const [wheelAngle, setWheelAngle] = useState(0)
  const [spinning, setSpinning] = useState(false)
  const [spinResult, setSpinResult] = useState<SpinResult | null>(null)

  const { data: status } = useQuery({
    queryKey: ['checkin-status'],
    queryFn: async () => (await api.get<CheckInStatus>('/engagement/checkin/status')).data,
  })

  const { data: gameStatus } = useQuery({
    queryKey: ['games-status'],
    queryFn: async () => (await api.get<{ can_spin: boolean }>('/games/status')).data,
  })

  const checkIn = useMutation({
    mutationFn: async () => (await api.post<{ streak: number; points_awarded: number }>('/engagement/checkin')).data,
    onSuccess: () => {
      queryClient.invalidateQueries({ queryKey: ['checkin-status'] })
      queryClient.invalidateQueries({ queryKey: ['loyalty'] })
      queryClient.invalidateQueries({ queryKey: ['games-status'] })
    },
  })

  const spin = useMutation({
    mutationFn: async () => (await api.post<SpinResult>('/games/spin')).data,
    onSuccess: (res) => {
      setSpinResult(res)
      queryClient.invalidateQueries({ queryKey: ['games-status'] })
      queryClient.invalidateQueries({ queryKey: ['loyalty'] })
      queryClient.invalidateQueries({ queryKey: ['voucher-claims'] })
    },
    onError: () => setSpinResult(null),
  })

  const doSpin = () => {
    if (!gameStatus?.can_spin || spinning) return
    setSpinResult(null)
    setSpinning(true)
    const turns = 5 * 360
    setWheelAngle((a) => a + turns + Math.floor(Math.random() * 360))
    setTimeout(() => {
      spin.mutate()
      setSpinning(false)
    }, 2600)
  }

  // Calendar keys must be built from LOCAL date parts. `toISOString()` is UTC,
  // so for a WIB (UTC+7) user anything after 17:00 local produced TOMORROW's
  // key — while `daysInMonth` below is a local `getDate()`. The two disagreed
  // and the grid could render a "2026-09-31" cell (31 days in a 30-day month)
  // and highlight the wrong day.
  const nowLocal = new Date()
  const year = nowLocal.getFullYear()
  const month = nowLocal.getMonth()
  const monthKey = `${year}-${String(month + 1).padStart(2, '0')}`
  const today = `${monthKey}-${String(nowLocal.getDate()).padStart(2, '0')}`
  const daysInMonth = new Date(year, month + 1, 0).getDate()
  const checkedSet = new Set(status?.dates ?? [])

  return (
    <div className="grid grid-cols-1 md:grid-cols-2 gap-5">
      <div className="bg-white dark:bg-gray-900 border border-gray-200 dark:border-gray-700 rounded-xl p-5 space-y-3">
        <div className="flex items-center justify-between">
          <h2 className="font-bold">📅 Check-in Harian</h2>
          <span className="text-xs px-2 py-1 rounded-full bg-orange-100 text-orange-700 font-medium">
            🔥 Streak {status?.streak ?? 0}
          </span>
        </div>
        <p className="text-xs text-gray-500">
          Hadir tiap hari untuk poin: hari ke-n bernilai 10×n poin (maks ×7 per sikap).
        </p>
        <div className="grid grid-cols-7 gap-1.5">
          {Array.from({ length: daysInMonth }, (_, i) => {
            const d = `${monthKey}-${String(i + 1).padStart(2, '0')}`
            const checked = checkedSet.has(d)
            const isToday = d === today
            return (
              <div
                key={d}
                className={`aspect-square flex items-center justify-center rounded-lg text-xs ${
                  checked ? 'bg-green-500 text-white' : isToday ? 'border-2 border-amber-400' : 'bg-gray-100 dark:bg-gray-800 text-gray-400'
                }`}
              >
                {checked ? '✓' : i + 1}
              </div>
            )
          })}
        </div>
        <button
          type="button"
          onClick={() => checkIn.mutate()}
          disabled={checkIn.isPending || status?.checked_in_today}
          className={`w-full py-3 rounded-xl font-semibold text-sm ${
            status?.checked_in_today
              ? 'bg-green-100 text-green-700 cursor-default'
              : 'bg-orange-500 text-white hover:bg-orange-600 disabled:opacity-50'
          }`}
        >
          {checkIn.isPending
            ? 'Memproses...'
            : status?.checked_in_today
              ? // The server awards 10 × min(streak, 7) for the check-in that
                // just happened. The button labels what the NEXT one will pay,
                // which is 10 × min(streak + 1, 7) — the old code showed the
                // current day's award, so the number never appeared to move.
                `✓ Sudah check-in. Besok +${10 * Math.min((status?.streak ?? 1) + 1, 7)} poin`
              : 'Check-in Sekarang'}
        </button>
      </div>

      <div className="bg-white dark:bg-gray-900 border border-gray-200 dark:border-gray-700 rounded-xl p-5 space-y-3">
        <h2 className="font-bold">🎡 Roda Keberuntungan</h2>
        <p className="text-xs text-gray-500">Satx putaran gratis setiap hari — hadiah poin atau kupon spesial!</p>
        <div className="flex justify-center py-2">
          <div className="relative">
            <div
              className="w-44 h-44 rounded-full border-8 border-amber-500 relative overflow-hidden transition-transform duration-[2500ms] ease-out"
              style={{
                transform: `rotate(${wheelAngle}deg)`,
                background:
                  'conic-gradient(#fbbf24 0deg 45deg, #f59e0b 45deg 90deg, #fbbf24 90deg 135deg, #ef4444 135deg 180deg, #fbbf24 180deg 225deg, #f59e0b 225deg 270deg, #fbbf24 270deg 315deg, #f59e0b 315deg 360deg)',
              }}
            >
              {SEGMENTS.map((label, i) => (
                <span
                  key={i}
                  className="absolute left-1/2 top-1/2 text-xs font-bold text-white drop-shadow"
                  style={{
                    transform: `rotate(${i * 45 + 22}deg) translate(-50%, -70px)`,
                    transformOrigin: '0 0',
                  }}
                >
                  {label}
                </span>
              ))}
            </div>
            <div className="absolute -top-1 left-1/2 -translate-x-1/2 w-0 h-0 border-l-8 border-r-8 border-t-[14px] border-l-transparent border-r-transparent border-t-gray-800" />
          </div>
        </div>
        <button
          type="button"
          onClick={doSpin}
          disabled={!gameStatus?.can_spin || spinning || spin.isPending}
          className={`w-full py-3 rounded-xl font-semibold text-sm ${
            gameStatus?.can_spin && !spinning
              ? 'bg-red-600 text-white hover:bg-red-700'
              : 'bg-gray-200 dark:bg-gray-700 text-gray-500 cursor-not-allowed'
          }`}
        >
          {spinning ? 'Berputar...' : !gameStatus?.can_spin ? 'Kembali besok untuk putar lagi' : 'PUTAR SEKARANG!'}
        </button>
        {spinResult && (
          <p className="text-center text-sm font-bold text-green-600 animate-bounce">
            🎉 Selamat! Kamu mendapat: {spinResult.label}
          </p>
        )}
      </div>
    </div>
  )
}
