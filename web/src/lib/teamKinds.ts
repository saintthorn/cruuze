import type { TeamRecord } from '@/sync/types'

export type TeamKind = TeamRecord['kind']

/**
 * The kinds of team. A kind decides the statuses a new team starts with (on
 * the server) and, here, what its work is called and which pages it is offered.
 */
export const TEAM_KINDS: readonly { value: TeamKind; label: string; description: string; noun: string }[] = [
  { value: 'engineering', label: 'Engineering', description: 'Backlog, cycles, pull requests.', noun: 'Issues' },
  { value: 'support', label: 'Support', description: 'Ticket queue with triage.', noun: 'Tickets' },
  { value: 'marketing', label: 'Marketing', description: 'Ideas to published, with a calendar.', noun: 'Work' },
  { value: 'design', label: 'Design', description: 'Exploring to approved, with review.', noun: 'Work' },
  { value: 'general', label: 'General', description: 'To do, doing, done.', noun: 'Tasks' },
]

export function kindInfo(kind: TeamKind | undefined) {
  return TEAM_KINDS.find((k) => k.value === kind) ?? TEAM_KINDS[0]!
}

/** Whether the team's work lives in code: branch names and pull requests are offered. */
export const worksInCode = (kind: TeamKind | undefined) => (kind ?? 'engineering') === 'engineering'

/** Whether the team plans by date, so the calendar is among its pages. */
export const plansByDate = (kind: TeamKind | undefined) => !worksInCode(kind)
