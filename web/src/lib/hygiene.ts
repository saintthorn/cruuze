import type { Issue, Relation, WorkflowState } from '@/sync/types'

/**
 * Why an open issue needs a look:
 * - `blocked`: in progress, but waiting on an issue that is still open
 * - `overdue`: its due date has passed
 * - `unowned`: in progress with nobody assigned
 * - `stuck`: in progress, with no activity for a while
 * - `stale`: not started, and untouched for longer than the team allows
 */
export type HygieneKind = 'blocked' | 'overdue' | 'unowned' | 'stuck' | 'stale'

export interface HygieneFinding {
  issue: Issue
  kind: HygieneKind
  /** Days without activity, or days past due for `overdue`. */
  days: number
}

/** In-progress work with no activity for this long counts as stuck. */
export const STUCK_DAYS = 14
/** A blocked issue is only worth a look once it has sat for this long. */
const BLOCKED_DAYS = 7

const DAY_MS = 24 * 60 * 60 * 1000

// Parsing a timestamp per issue on every pass adds up over tens of
// thousands. Issue records are replaced, never mutated, so remember it.
const updatedAtMs = new WeakMap<Issue, number>()

function updatedAt(issue: Issue): number {
  let ms = updatedAtMs.get(issue)
  if (ms === undefined) {
    ms = Date.parse(issue.updatedAt)
    updatedAtMs.set(issue, ms)
  }
  return ms
}

export interface HygieneInput {
  /** Active issues, of any number of teams. */
  issues: readonly Issue[]
  relations: Iterable<Relation>
  stateOf(id: string): WorkflowState | undefined
  /** How long an unstarted issue may sit untouched, per team; 0 or absent means forever. */
  staleDays(teamId: string): number
  now?: Date
}

/**
 * Looks through open issues for the ones that need attention and returns
 * them by team, most neglected first. Each issue is reported once, under the
 * most pressing reason.
 */
export function findHygieneIssues(input: HygieneInput): Map<string, HygieneFinding[]> {
  const now = (input.now ?? new Date()).getTime()
  const today = new Date(now).toISOString().slice(0, 10)
  const open = new Map<string, Issue>()
  const typeOf = new Map<string, WorkflowState['type'] | undefined>()
  for (const issue of input.issues) {
    let type = typeOf.get(issue.stateId)
    if (type === undefined && !typeOf.has(issue.stateId)) {
      type = input.stateOf(issue.stateId)?.type
      typeOf.set(issue.stateId, type)
    }
    if (type !== 'completed' && type !== 'canceled') open.set(issue.id, issue)
  }

  // Issues that something still open blocks.
  const blocked = new Set<string>()
  for (const relation of input.relations) {
    if (relation.type === 'blocks' && open.has(relation.issueId)) blocked.add(relation.relatedIssueId)
  }

  const byTeam = new Map<string, HygieneFinding[]>()
  for (const issue of open.values()) {
    const type = typeOf.get(issue.stateId)
    const idle = Math.floor((now - updatedAt(issue)) / DAY_MS)
    let finding: HygieneFinding | undefined
    if (type === 'started' && blocked.has(issue.id) && idle >= BLOCKED_DAYS) {
      finding = { issue, kind: 'blocked', days: idle }
    } else if (issue.dueDate && issue.dueDate < today) {
      finding = { issue, kind: 'overdue', days: Math.round((Date.parse(today) - Date.parse(issue.dueDate)) / DAY_MS) }
    } else if (type === 'started' && !issue.assigneeId) {
      finding = { issue, kind: 'unowned', days: idle }
    } else if (type === 'started' && idle >= STUCK_DAYS) {
      finding = { issue, kind: 'stuck', days: idle }
    } else if (type !== 'started') {
      const limit = input.staleDays(issue.teamId)
      if (limit > 0 && idle >= limit) finding = { issue, kind: 'stale', days: idle }
    }
    if (!finding) continue
    const list = byTeam.get(issue.teamId)
    if (list) list.push(finding)
    else byTeam.set(issue.teamId, [finding])
  }
  for (const list of byTeam.values()) list.sort((a, b) => b.days - a.days)
  return byTeam
}
