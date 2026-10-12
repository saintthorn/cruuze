const MINUTE = 60_000
const HOUR = 60 * MINUTE
const DAY = 24 * HOUR

/** A short, human reading of how long ago something happened: "5m ago", "Dec 24". */
export function timeAgo(iso: string, now = new Date()): string {
  const then = new Date(iso)
  const elapsed = now.getTime() - then.getTime()
  if (elapsed < MINUTE) return 'just now'
  if (elapsed < HOUR) return `${Math.floor(elapsed / MINUTE)}m ago`
  if (elapsed < DAY) return `${Math.floor(elapsed / HOUR)}h ago`
  if (elapsed < 7 * DAY) return `${Math.floor(elapsed / DAY)}d ago`
  return then.toLocaleDateString('en-US', {
    month: 'short',
    day: 'numeric',
    year: then.getFullYear() === now.getFullYear() ? undefined : 'numeric',
  })
}

/** The full date and time, for tooltips. */
export function fullDateTime(iso: string): string {
  return new Date(iso).toLocaleString('en-US', { dateStyle: 'medium', timeStyle: 'short' })
}
