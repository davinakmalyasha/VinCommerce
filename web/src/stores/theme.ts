import { create } from 'zustand'

interface ThemeState {
  dark: boolean
  toggle: () => void
}

const stored = typeof localStorage !== 'undefined' && localStorage.getItem('vc_dark') === '1'

export const useTheme = create<ThemeState>((set) => ({
  dark: stored,
  toggle: () =>
    set((s) => {
      const next = !s.dark
      localStorage.setItem('vc_dark', next ? '1' : '0')
      document.documentElement.classList.toggle('dark', next)
      return { dark: next }
    }),
}))

export function initTheme() {
  if (stored) document.documentElement.classList.add('dark')
}
