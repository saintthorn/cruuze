import type { Issue, Relation, WorkflowState } from '@/sync/types'

export interface FocusItem {
  issue: Issue
  /** Open issues that have to finish before this one can. */
  blockedBy: Issue[]
  /** Open issues that wait on this one. */
  blocks: Issue[]
  overdue: boolean
}

export interface Focus {
  /** What you have started. */
  inProgress: FocusItem[]
  /** What to pick up next, best first. */
  next: FocusItem[]
  /** Yours, but stuck behind something else. */
  waiting: FocusItem[]
  /** How many more of yours are open beyond what `next` shows. */
  later: number
}

export interface FocusContext {
  stateType: (stateId: string) => WorkflowState['type'] | undefined
  issue: (id: string) => Issue | undefined
  relations: readonly Relation[]
  /** Today as YYYY-MM-DD. */
  today: string
  /** Cycles that are running now. */
  currentCycles: ReadonlySet<string>
  /** How many to suggest. */
  limit?: number
}

// "No priority" sorts after low.
const priorityRank = (priority: number) => (priority === 0 ? 5 : priority)

/**
 * Sorts one person's open issues into what they are doing, what to do next
 * and what is stuck. Next is ordered by what unblocks other people, then
 * what is late or due this week, then the running cycle, then priority.
 */
export function buildFocus(mine: readonly Issue[], ctx: FocusContext): Focus {
  const open = (issue: Issue | undefined): issue is Issue => {
    const type = issue && ctx.stateType(issue.stateId)
    return Boolean(issue) && !issue!.archivedAt && type !== 'completed' && type !== 'canceled'
  }
  const blockedBy = new Map<string, Issue[]>()
  const blocks = new Map<string, Issue[]>()
  for (const relation of ctx.relations) {
    if (relation.type !== 'blocks') continue
    const blocker = ctx.issue(relation.issueId)
    const blocked = ctx.issue(relation.relatedIssueId)
    if (!open(blocker) || !open(blocked)) continue
    blockedBy.set(blocked.id, [...(blockedBy.get(blocked.id) ?? []), blocker])
    blocks.set(blocker.id, [...(blocks.get(blocker.id) ?? []), blocked])
  }

  const soon = new Date(Date.parse(`${ctx.today}T00:00:00Z`) + 7 * 24 * 60 * 60 * 1000).toISOString().slice(0, 10)
  const item = (issue: Issue): FocusItem => ({
    issue,
    blockedBy: blockedBy.get(issue.id) ?? [],
    blocks: blocks.get(issue.id) ?? [],
    overdue: issue.dueDate !== undefined && issue.dueDate < ctx.today,
  })
  const rank = (a: FocusItem): (number | string)[] => [
    a.blocks.length > 0 ? 0 : 1,
    a.overdue ? 0 : 1,
    a.issue.dueDate !== undefined && a.issue.dueDate <= soon ? 0 : 1,
    a.issue.cycleId && ctx.currentCycles.has(a.issue.cycleId) ? 0 : 1,
    priorityRank(a.issue.priority),
    a.issue.dueDate ?? '9999',
    a.issue.sortOrder,
  ]
  const byRank = (a: FocusItem, b: FocusItem) => {
    const ra = rank(a)
    const rb = rank(b)
    for (let i = 0; i < ra.length; i++) {
      if (ra[i]! < rb[i]!) return -1
      if (ra[i]! > rb[i]!) return 1
    }
    return 0
  }

  const focus: Focus = { inProgress: [], next: [], waiting: [], later: 0 }
  const candidates: FocusItem[] = []
  for (const issue of mine) {
    if (!open(issue)) continue
    const type = ctx.stateType(issue.stateId)
    const entry = item(issue)
    if (type === 'started') focus.inProgress.push(entry)
    else if (type === 'triage') continue
    else if (entry.blockedBy.length > 0) focus.waiting.push(entry)
    else candidates.push(entry)
  }
  focus.inProgress.sort(byRank)
  focus.waiting.sort(byRank)
  candidates.sort(byRank)
  const limit = ctx.limit ?? 7
  focus.next = candidates.slice(0, limit)
  focus.later = Math.max(0, candidates.length - limit)
  return focus
}
