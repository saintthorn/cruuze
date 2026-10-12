import { describe, expect, it } from 'vitest'

import { describeForecast, formatDays } from './forecast'

const base = {
  remaining: 6,
  completed: 17,
  weeklyCompleted: [0, 0, 0, 2, 2, 2, 2, 2, 2, 2, 2, 1],
  enoughHistory: true,
  diverging: false,
  calibration: { windows: 4, held: 3 },
}

describe('describeForecast', () => {
  const values = (f: Parameters<typeof describeForecast>[0]) => describeForecast(f).parts.map((p) => `${p.label} ${p.value}`.trim())

  it('gives a date for each level of confidence, and the odds of the target', () => {
    const summary = describeForecast({ ...base, p50: '2026-11-03', p85: '2026-11-20', p95: '2026-12-04', targetDate: '2026-11-10', onTimeProbability: 0.62 })
    expect(summary.parts.map((p) => `${p.label} ${p.value}`)).toEqual(['50% Nov 3', '85% Nov 20', '95% Dec 4', 'by Nov 10 62% likely'])
    expect(summary.detail).toContain('1.4 completed a week')
    expect(summary.detail).toContain('held 3 of 4 times')
  })

  it('says so when there is nothing to forecast, or nothing to forecast from', () => {
    expect(values({ ...base, remaining: 0 })).toEqual(['done'])
    expect(values({ ...base, enoughHistory: false })).toEqual(['not enough history'])
    expect(values({ ...base, diverging: true })[0]).toContain('scope is growing')
    // A median exists but the confident dates don't.
    expect(values({ ...base, p50: '2027-12-09' })).toEqual(['50% Dec 9, 2027', '85% never'])
  })

  it('flags a target that is unlikely', () => {
    const late = describeForecast({ ...base, p50: '2026-11-03', p85: '2026-11-20', targetDate: '2026-10-20', onTimeProbability: 0.08 })
    expect(late.parts[late.parts.length - 1]).toMatchObject({ value: '8% likely', tone: 'warn' })
  })
})

describe('formatDays', () => {
  it('uses the unit that fits', () => {
    expect(formatDays(0.25)).toBe('6h')
    expect(formatDays(2)).toBe('2d')
    expect(formatDays(2.46)).toBe('2.5d')
    expect(formatDays(12.3)).toBe('12d')
    expect(formatDays(30)).toBe('4w')
  })
})
