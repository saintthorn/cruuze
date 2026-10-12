/** A month of a year; `month` counts from 0. */
export interface Month {
  year: number
  month: number
}

const iso = (date: Date) => date.toISOString().slice(0, 10)

export function monthOf(date: string): Month {
  return { year: Number(date.slice(0, 4)), month: Number(date.slice(5, 7)) - 1 }
}

export function shiftMonth({ year, month }: Month, by: number): Month {
  const shifted = new Date(Date.UTC(year, month + by, 1))
  return { year: shifted.getUTCFullYear(), month: shifted.getUTCMonth() }
}

/**
 * The days to show for a month, as YYYY-MM-DD in rows of whole weeks that
 * start on Monday; days of the neighbouring months fill the first and last rows.
 */
export function monthGrid({ year, month }: Month): string[][] {
  const first = new Date(Date.UTC(year, month, 1))
  // getUTCDay counts from Sunday; shift so Monday is 0.
  const lead = (first.getUTCDay() + 6) % 7
  const days = new Date(Date.UTC(year, month + 1, 0)).getUTCDate()
  const weeks: string[][] = []
  for (let offset = -lead; offset < days; offset += 7) {
    weeks.push(Array.from({ length: 7 }, (_, i) => iso(new Date(Date.UTC(year, month, 1 + offset + i)))))
  }
  return weeks
}

export function monthLabel({ year, month }: Month): string {
  return new Date(Date.UTC(year, month, 1)).toLocaleDateString('en-US', { month: 'long', year: 'numeric', timeZone: 'UTC' })
}

export const inMonth = (date: string, { year, month }: Month) => monthOf(date).year === year && monthOf(date).month === month
