import { afterEach, describe, expect, it, vi } from 'vitest'
import { Countdown } from './Countdown'
import { advance, render } from '../test/render'

afterEach(() => {
  vi.useRealTimers()
})

describe('Countdown', () => {
  it('formats the remaining time as HH:MM:SS', async () => {
    vi.useFakeTimers()
    vi.setSystemTime(new Date('2026-01-01T00:00:00Z'))
    const view = await render(<Countdown to={Date.now() + (2 * 3_600_000 + 5 * 60_000 + 9_000)} />)
    expect(view.text()).toBe('02:05:09')
    await view.unmount()
  })

  it('ticks down as time passes instead of freezing at mount', async () => {
    vi.useFakeTimers()
    vi.setSystemTime(new Date('2026-01-01T00:00:00Z'))
    const view = await render(<Countdown to={Date.now() + 65_000} />)
    expect(view.text()).toBe('00:01:05')

    await advance(5_000)
    expect(view.text()).toBe('00:01:00')

    await advance(1_000)
    expect(view.text()).toBe('00:00:59')
    await view.unmount()
  })

  it('clamps at zero and swaps in expiredLabel once the deadline passes', async () => {
    vi.useFakeTimers()
    vi.setSystemTime(new Date('2026-01-01T00:00:00Z'))
    const view = await render(
      <Countdown to={Date.now() + 2_000} expiredLabel={<span>Waktu habis</span>} />,
    )
    expect(view.text()).toBe('00:00:02')

    await advance(3_000)
    expect(view.text()).toBe('Waktu habis')
    await view.unmount()
  })

  it('never renders a negative remainder', async () => {
    vi.useFakeTimers()
    vi.setSystemTime(new Date('2026-01-01T00:00:00Z'))
    const view = await render(<Countdown to={Date.now() - 10_000} />)
    expect(view.text()).toBe('00:00:00')
    await view.unmount()
  })

  it('calls onExpire exactly once when the deadline passes', async () => {
    vi.useFakeTimers()
    vi.setSystemTime(new Date('2026-01-01T00:00:00Z'))
    const onExpire = vi.fn()
    const view = await render(<Countdown to={Date.now() + 1_000} onExpire={onExpire} />)
    expect(onExpire).not.toHaveBeenCalled()

    await advance(2_000)
    expect(onExpire).toHaveBeenCalledTimes(1)

    await advance(5_000)
    expect(onExpire).toHaveBeenCalledTimes(1)
    await view.unmount()
  })

  it('exposes the parts to a custom renderer so the ticking stays local', async () => {
    vi.useFakeTimers()
    vi.setSystemTime(new Date('2026-01-01T00:00:00Z'))
    const seen: number[] = []
    const view = await render(
      <Countdown to={Date.now() + 10_000}>
        {({ remaining, expired, clock }) => {
          seen.push(remaining)
          return <span>{expired ? 'selesai' : clock}</span>
        }}
      </Countdown>,
    )
    expect(view.text()).toBe('00:00:10')
    await advance(3_000)
    expect(view.text()).toBe('00:00:07')
    expect(seen.length).toBeGreaterThan(1)
    await view.unmount()
  })

  it('renders the block variant as three zero-padded cells', async () => {
    vi.useFakeTimers()
    vi.setSystemTime(new Date('2026-01-01T00:00:00Z'))
    const view = await render(<Countdown to={Date.now() + 3_723_000} variant="blocks" />)
    expect(view.findAll('span').map((s) => s.textContent)).toEqual(['01', '02', '03'])
    await view.unmount()
  })

  it('renders nothing and starts no interval when there is no deadline', async () => {
    vi.useFakeTimers()
    const setIntervalSpy = vi.spyOn(window, 'setInterval')
    const view = await render(<Countdown to={0} />)
    expect(view.html()).toBe('')
    expect(setIntervalSpy).not.toHaveBeenCalled()
    setIntervalSpy.mockRestore()
    await view.unmount()
  })
})
