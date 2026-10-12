import { describe, expect, it } from 'vitest'

import { inMonth, monthGrid, monthLabel, monthOf, shiftMonth } from './calendar'

describe('monthGrid', () => {
  it('lays a month out in whole weeks from Monday', () => {
    // October 2026 starts on a Thursday and ends on a Saturday.
    const weeks = monthGrid({ year: 2026, month: 9 })
    expect(weeks).toHaveLength(5)
    expect(weeks[0]).toEqual(['2026-09-28', '2026-09-29', '2026-09-30', '2026-10-01', '2026-10-02', '2026-10-03', '2026-10-04'])
    expect(weeks.at(-1)!.at(-1)).toBe('2026-11-01')
    expect(weeks.every((week) => week.length === 7)).toBe(true)
  })

  it('needs no filler when the month starts on a Monday', () => {
    expect(monthGrid({ year: 2027, month: 1 })).toHaveLength(4) // February 2027: Monday the 1st, 28 days
    expect(monthGrid({ year: 2027, month: 1 })[0]![0]).toBe('2027-02-01')
  })

  it('takes six rows when it must', () => {
    expect(monthGrid({ year: 2026, month: 7 })).toHaveLength(6) // August 2026: starts on a Saturday
  })

  it('handles leap years', () => {
    expect(monthGrid({ year: 2028, month: 1 }).flat()).toContain('2028-02-29')
  })
})

describe('months', () => {
  it('moves across years', () => {
    expect(shiftMonth({ year: 2026, month: 11 }, 1)).toEqual({ year: 2027, month: 0 })
    expect(shiftMonth({ year: 2026, month: 0 }, -1)).toEqual({ year: 2025, month: 11 })
  })

  it('reads and names them', () => {
    expect(monthOf('2026-10-01')).toEqual({ year: 2026, month: 9 })
    expect(monthLabel({ year: 2026, month: 9 })).toBe('October 2026')
    expect(inMonth('2026-09-30', { year: 2026, month: 9 })).toBe(false)
    expect(inMonth('2026-10-31', { year: 2026, month: 9 })).toBe(true)
  })
})
