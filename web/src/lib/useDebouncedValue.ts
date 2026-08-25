import { useEffect, useState } from 'react'

// Timer-based debounce (useDeferredValue alone doesn't stop the network call).
export function useDebouncedValue<T>(value: T, delayMs = 450): T {
  const [debounced, setDebounced] = useState(value)
  useEffect(() => {
    const t = window.setTimeout(() => setDebounced(value), delayMs)
    return () => clearTimeout(t)
  }, [value, delayMs])
  return debounced
}
