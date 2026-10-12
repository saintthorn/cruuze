import type { components } from '@/api/schema'

type Forecast = components['schemas']['Forecast']

export interface ForecastPart {
  label: string
  value: string
  tone?: 'good' | 'warn'
}

export interface ForecastSummary {
  parts: ForecastPart[]
  /** A sentence or two on where the numbers come from, for a tooltip. */
  detail: string
}

function shortDate(date: string): string {
  const [year, month, day] = date.split('-').map(Number)
  const d = new Date(Date.UTC(year!, month! - 1, day!))
  const sameYear = d.getUTCFullYear() === new Date().getUTCFullYear()
  return d.toLocaleDateString('en-US', { month: 'short', day: 'numeric', year: sameYear ? undefined : 'numeric', timeZone: 'UTC' })
}

/** Turns a forecast into the few words shown next to a project or cycle. */
export function describeForecast(forecast: Forecast): ForecastSummary {
  const weeks = forecast.weeklyCompleted
  const pace = weeks.reduce((sum, n) => sum + n, 0) / Math.max(1, weeks.length)
  let detail = `Last ${weeks.length} weeks: ${pace.toFixed(1)} completed a week, ${forecast.remaining} left.`
  const { windows, held } = forecast.calibration
  if (windows > 0) {
    detail += ` 85% forecast held ${held} of ${windows} times.`
  }

  if (forecast.remaining === 0) {
    return { parts: [{ label: '', value: 'done', tone: 'good' }], detail }
  }
  if (!forecast.enoughHistory) {
    return {
      parts: [{ label: '', value: 'not enough history' }],
      detail: 'Needs a few weeks of completed issues.',
    }
  }
  if (forecast.diverging || !forecast.p50) {
    return {
      parts: [{ label: '', value: 'scope is growing faster than it closes', tone: 'warn' }],
      detail,
    }
  }

  const parts: ForecastPart[] = [{ label: '50%', value: shortDate(forecast.p50) }]
  parts.push({ label: '85%', value: forecast.p85 ? shortDate(forecast.p85) : 'never', tone: forecast.p85 ? undefined : 'warn' })
  if (forecast.p95) parts.push({ label: '95%', value: shortDate(forecast.p95) })
  if (forecast.targetDate && forecast.onTimeProbability !== undefined) {
    const chance = Math.round(forecast.onTimeProbability * 100)
    parts.push({
      label: `by ${shortDate(forecast.targetDate)}`,
      value: `${chance}% likely`,
      tone: chance >= 85 ? 'good' : chance < 50 ? 'warn' : undefined,
    })
  }
  return { parts, detail }
}

/** `2.5d`, `3w`: a duration in days, in the fewest characters that say it. */
export function formatDays(days: number): string {
  if (days < 1) return `${Math.max(1, Math.round(days * 24))}h`
  if (days < 14) return `${days.toFixed(days < 10 && days % 1 !== 0 ? 1 : 0)}d`
  return `${Math.round(days / 7)}w`
}
