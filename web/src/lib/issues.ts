import type { Issue, Project, TeamRecord, UserRecord, WorkflowState } from '@/sync/types'

/** Issue priorities in the order they are listed in menus. */
export const PRIORITIES = [
  { value: 0, label: 'No priority' },
  { value: 1, label: 'Urgent' },
  { value: 2, label: 'High' },
  { value: 3, label: 'Medium' },
  { value: 4, label: 'Low' },
] as const

export function priorityLabel(priority: number): string {
  return PRIORITIES.find((p) => p.value === priority)?.label ?? 'No priority'
}

export type Grouping = 'status' | 'priority' | 'assignee' | 'project' | 'none'
export type Ordering = 'manual' | 'priority' | 'updated' | 'created' | 'dueDate'

export const GROUPINGS: { value: Grouping; label: string }[] = [
  { value: 'status', label: 'Status' },
  { value: 'priority', label: 'Priority' },
  { value: 'assignee', label: 'Assignee' },
  { value: 'project', label: 'Project' },
  { value: 'none', label: 'No grouping' },
]

export const ORDERINGS: { value: Ordering; label: string }[] = [
  { value: 'manual', label: 'Manual' },
  { value: 'priority', label: 'Priority' },
  { value: 'updated', label: 'Last updated' },
  { value: 'created', label: 'Last created' },
  { value: 'dueDate', label: 'Due date' },
]

/** `ENG-12`, or `ENG-…` for an issue the server hasn't numbered yet. */
export function issueIdentifier(issue: Issue, team: TeamRecord | undefined): string {
  const key = team?.key ?? '?'
  return issue.number > 0 ? `${key}-${issue.number}` : `${key}-…`
}

// Urgent first, "no priority" last.
function priorityRank(priority: number): number {
  return priority === 0 ? 5 : priority
}

const ORDER: Record<Ordering, (a: Issue, b: Issue) => number> = {
  manual: (a, b) => a.sortOrder - b.sortOrder,
  priority: (a, b) => priorityRank(a.priority) - priorityRank(b.priority),
  updated: (a, b) => b.updatedAt.localeCompare(a.updatedAt),
  created: (a, b) => b.createdAt.localeCompare(a.createdAt),
  // Issues without a due date go last.
  dueDate: (a, b) => (a.dueDate ?? '9999').localeCompare(b.dueDate ?? '9999'),
}

/** A sorted copy of `issues`. Ties fall back to manual order, so the result is stable. */
export function sortIssues(issues: readonly Issue[], ordering: Ordering): Issue[] {
  return [...issues].sort((a, b) => ORDER[ordering](a, b) || ORDER.manual(a, b))
}

export interface IssueGroup {
  key: string
  label: string
  /** Set when grouping by status, for the group's icon. */
  state?: WorkflowState
  /** Set when grouping by priority. */
  priority?: number
  /** Set when grouping by assignee; undefined for "No assignee". */
  user?: UserRecord
  /** Set when grouping by project; undefined for "No project". */
  project?: Project
  issues: Issue[]
}

export interface GroupContext {
  state(id: string): WorkflowState | undefined
  user(id: string | undefined): UserRecord | undefined
  project(id: string | undefined): Project | undefined
}

// Work in flight first, then what's next, then what's finished.
const STATE_TYPE_RANK: Record<WorkflowState['type'], number> = {
  triage: -1,
  started: 0,
  unstarted: 1,
  backlog: 2,
  completed: 3,
  canceled: 4,
}

/**
 * Splits issues into ordered groups, dropping empty ones. Issues keep the
 * order they arrive in, so sort them first.
 */
export function groupIssues(issues: readonly Issue[], grouping: Grouping, ctx: GroupContext): IssueGroup[] {
  if (grouping === 'none') {
    return issues.length > 0 ? [{ key: 'all', label: '', issues: [...issues] }] : []
  }

  type Ranked = IssueGroup & { rank: number }
  const groups = new Map<string, Ranked>()

  // What the group of an issue looks like. Worked out once per distinct
  // status, priority, project or assignee rather than once per issue: with
  // tens of thousands of issues, the per-issue work has to be a map lookup.
  const describe = (issue: Issue): Omit<Ranked, 'issues'> => {
    if (grouping === 'status') {
      const state = ctx.state(issue.stateId)
      // Keyed by type and name, so same-named states of different teams share a group.
      const key = state ? `${state.type}:${state.name.toLowerCase()}` : 'unknown'
      const rank = state ? STATE_TYPE_RANK[state.type] * 1000 + state.position : 9999
      return { key, label: state?.name ?? 'Unknown', state, rank }
    }
    if (grouping === 'priority') {
      return { key: String(issue.priority), label: priorityLabel(issue.priority), priority: issue.priority, rank: priorityRank(issue.priority) }
    }
    if (grouping === 'project') {
      const project = ctx.project(issue.projectId)
      return { key: project?.id ?? 'none', label: project?.name ?? 'No project', project, rank: project ? 0 : 1 }
    }
    const user = ctx.user(issue.assigneeId)
    return { key: user?.id ?? 'none', label: user ? user.name || user.email : 'No assignee', user, rank: user ? 0 : 1 }
  }
  const bucketOf = (issue: Issue): string | number =>
    grouping === 'status'
      ? issue.stateId
      : grouping === 'priority'
        ? issue.priority
        : grouping === 'project'
          ? (issue.projectId ?? '')
          : (issue.assigneeId ?? '')

  const byBucket = new Map<string | number, Ranked>()
  for (const issue of issues) {
    const bucket = bucketOf(issue)
    let group = byBucket.get(bucket)
    if (!group) {
      const described = describe(issue)
      group = groups.get(described.key)
      if (!group) {
        group = { ...described, issues: [] }
        groups.set(described.key, group)
      }
      byBucket.set(bucket, group)
    }
    group.issues.push(issue)
  }

  return [...groups.values()]
    .sort((a, b) => a.rank - b.rank || a.label.localeCompare(b.label))
    .map(({ rank: _rank, ...group }) => group)
}

const DAY_MS = 24 * 60 * 60 * 1000

/** Midnight local time for a `YYYY-MM-DD` date. */
function localDate(date: string): Date {
  const [year, month, day] = date.split('-').map(Number)
  return new Date(year!, month! - 1, day!)
}

/** How a due date should read in a list: "Today", "Tomorrow", "Dec 24", "Dec 24, 2027". */
export function formatDueDate(date: string, now = new Date()): string {
  const due = localDate(date)
  const today = new Date(now.getFullYear(), now.getMonth(), now.getDate())
  const days = Math.round((due.getTime() - today.getTime()) / DAY_MS)
  if (days === 0) return 'Today'
  if (days === 1) return 'Tomorrow'
  if (days === -1) return 'Yesterday'
  return due.toLocaleDateString('en-US', {
    month: 'short',
    day: 'numeric',
    year: due.getFullYear() === today.getFullYear() ? undefined : 'numeric',
  })
}

/** Whether a due date has passed. */
export function isOverdue(date: string, now = new Date()): boolean {
  const today = new Date(now.getFullYear(), now.getMonth(), now.getDate())
  return localDate(date).getTime() < today.getTime()
}

/** Colors offered when creating a label. */
export const LABEL_COLORS = ['#eb5757', '#f2994a', '#f2c94c', '#4cb782', '#26b5ce', '#5e6ad2', '#bb87fc', '#95a2b3'] as const

/** Project statuses in the order they are listed in menus. */
export const PROJECT_STATUSES = [
  { value: 'backlog', label: 'Backlog' },
  { value: 'planned', label: 'Planned' },
  { value: 'started', label: 'In progress' },
  { value: 'paused', label: 'Paused' },
  { value: 'completed', label: 'Completed' },
  { value: 'canceled', label: 'Canceled' },
] as const satisfies readonly { value: Project['status']; label: string }[]

export function projectStatusLabel(status: Project['status']): string {
  return PROJECT_STATUSES.find((s) => s.value === status)?.label ?? status
}

export interface Progress {
  total: number
  /** Completed issues. */
  done: number
  /** Issues in progress. */
  started: number
  /** Share of non-canceled issues that are completed, 0-100. */
  percent: number
}

/** How far along a set of issues is. Canceled issues don't count either way. */
export function progressOf(issues: readonly Issue[], stateOf: (id: string) => WorkflowState | undefined): Progress {
  let total = 0
  let done = 0
  let started = 0
  for (const issue of issues) {
    const type = stateOf(issue.stateId)?.type
    if (type === 'canceled') continue
    total++
    if (type === 'completed') done++
    else if (type === 'started') started++
  }
  return { total, done, started, percent: total === 0 ? 0 : Math.round((done / total) * 100) }
}

/** "Sep 28 – Oct 11", for a cycle or a project. */
export function formatDateRange(start: string, end: string): string {
  const format = (date: string) => {
    const [year, month, day] = date.split('-').map(Number)
    return new Date(year!, month! - 1, day!).toLocaleDateString('en-US', { month: 'short', day: 'numeric' })
  }
  return `${format(start)} – ${format(end)}`
}

/** Today as `YYYY-MM-DD` in UTC, the calendar cycles are kept in. */
export function todayUTC(now = new Date()): string {
  return now.toISOString().slice(0, 10)
}

/** Longest branch name suggested for an issue. */
const MAX_BRANCH_LENGTH = 60

/** Lowercase words joined by dashes, without accents or punctuation. */
function slug(text: string): string {
  return text
    .normalize('NFKD')
    .replace(/\p{M}/gu, '')
    .toLowerCase()
    .replace(/[^a-z0-9]+/g, '-')
    .replace(/^-+|-+$/g, '')
}

/**
 * A git branch name for working on an issue, e.g. `ada/eng-12-fix-login`.
 * It contains the issue's identifier, which is what links the branch's pull
 * request back to the issue.
 */
export function branchName(identifier: string, title: string, userName: string): string {
  const owner = slug(userName.split(/[\s@]/)[0] ?? '')
  const name = `${identifier.toLowerCase()}-${slug(title)}`.slice(0, MAX_BRANCH_LENGTH).replace(/-+$/, '')
  return owner ? `${owner}/${name}` : name
}
