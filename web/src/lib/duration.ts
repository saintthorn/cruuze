import type { Activity, Issue, WorkflowState } from '@/sync/types'

/**
 * Reads a length of time as people type it: "1h 30m", "1.5h", "90m", "1:30",
 * or a bare number of minutes. Returns seconds, or null if it isn't one.
 */
export function parseDuration(text: string): number | null {
  const input = text.trim().toLowerCase()
  if (!input) return null
  const clock = /^(\d{1,3}):([0-5]\d)$/.exec(input)
  if (clock) return positive(Number(clock[1]) * 3600 + Number(clock[2]) * 60)
  if (/^\d+(\.\d+)?$/.test(input)) return positive(Math.round(Number(input) * 60))

  let seconds = 0
  let rest = input
  for (const [unit, size] of [['d', 8 * 3600], ['h', 3600], ['m', 60], ['s', 1]] as const) {
    const match = new RegExp(`^(\\d+(?:\\.\\d+)?)\\s*${unit}[a-z]*\\s*`).exec(rest)
    if (!match) continue
    seconds += Number(match[1]) * size
    rest = rest.slice(match[0].length)
  }
  return rest === '' ? positive(Math.round(seconds)) : null
}

function positive(seconds: number): number | null {
  return seconds > 0 ? seconds : null
}

/** "1h 30m", "45m", "2d 3h": a length of time to the nearest minute. */
export function formatDuration(seconds: number): string {
  const minutes = Math.round(seconds / 60)
  if (minutes < 1) return seconds > 0 ? '<1m' : '0m'
  const days = Math.floor(minutes / (24 * 60))
  const hours = Math.floor(minutes / 60) % 24
  const mins = minutes % 60
  if (days > 0) return hours > 0 ? `${days}d ${hours}h` : `${days}d`
  if (hours > 0) return mins > 0 ? `${hours}h ${mins}m` : `${hours}h`
  return `${mins}m`
}

/** A running clock: "0:04:09". */
export function formatClock(seconds: number): string {
  const s = Math.max(0, Math.floor(seconds))
  const pad = (n: number) => String(n).padStart(2, '0')
  return `${Math.floor(s / 3600)}:${pad(Math.floor(s / 60) % 60)}:${pad(s % 60)}`
}

/**
 * How long an issue has spent in started statuses, in seconds, read from its
 * status changes. Needs the issue's activity to be loaded. A status that has
 * since been deleted counts as not started.
 */
export function timeInProgress(
  issue: Issue,
  activities: readonly Activity[],
  state: (id: string | undefined) => WorkflowState | undefined,
  now: number,
): number {
  const started = (id: string | undefined) => state(id)?.type === 'started'
  const changes = activities.filter((a) => a.type === 'state').sort((a, b) => a.createdAt.localeCompare(b.createdAt))
  let total = 0
  let since = started(changes.length > 0 ? changes[0]!.from : issue.stateId) ? Date.parse(issue.createdAt) : null
  for (const change of changes) {
    const at = Date.parse(change.createdAt)
    if (since !== null) total += at - since
    since = started(change.to) ? at : null
  }
  if (since !== null) total += Math.max(0, now - since)
  return Math.max(0, Math.round(total / 1000))
}
