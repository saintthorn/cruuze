import type { Issue, WorkflowState } from '@/sync/types'

/**
 * Which issues a list shows. An empty list for a property means "any"; an
 * issue must match every property that has values.
 */
export interface IssueFilter {
  /** Status keys, see {@link stateKey}. */
  states: string[]
  priorities: number[]
  /** User IDs; {@link NO_ASSIGNEE} matches unassigned issues. */
  assignees: string[]
  /** An issue matches if it has any of these labels. */
  labels: string[]
  /** Project IDs; {@link NO_PROJECT} matches issues in no project. */
  projects: string[]
}

export const NO_ASSIGNEE = 'none'
export const NO_PROJECT = 'none'

/** The properties a filter can constrain. */
export const FILTER_PROPERTIES = ['states', 'priorities', 'assignees', 'labels', 'projects'] as const

export function emptyFilter(): IssueFilter {
  return { states: [], priorities: [], assignees: [], labels: [], projects: [] }
}

export function isEmptyFilter(filter: IssueFilter): boolean {
  return filterCount(filter) === 0
}

/** How many properties the filter constrains. */
export function filterCount(filter: IssueFilter): number {
  return FILTER_PROPERTIES.filter((property) => filter[property].length > 0).length
}

/**
 * Identifies a status across teams: each team has its own states, so the
 * same-named state of the same type counts as one status in lists that mix teams.
 */
export function stateKey(state: Pick<WorkflowState, 'type' | 'name'>): string {
  return `${state.type}:${state.name.toLowerCase()}`
}

export function matchesFilter(
  issue: Issue,
  filter: IssueFilter,
  stateOf: (id: string) => WorkflowState | undefined,
): boolean {
  if (filter.states.length > 0) {
    const state = stateOf(issue.stateId)
    if (!state || !filter.states.includes(stateKey(state))) return false
  }
  if (filter.priorities.length > 0 && !filter.priorities.includes(issue.priority)) return false
  if (filter.assignees.length > 0 && !filter.assignees.includes(issue.assigneeId ?? NO_ASSIGNEE)) return false
  if (filter.labels.length > 0 && !issue.labelIds.some((id) => filter.labels.includes(id))) return false
  if (filter.projects.length > 0 && !filter.projects.includes(issue.projectId ?? NO_PROJECT)) return false
  return true
}

/** Whether an issue is finished: completed or canceled. */
export function isClosed(issue: Issue, stateOf: (id: string) => WorkflowState | undefined): boolean {
  const type = stateOf(issue.stateId)?.type
  return type === 'completed' || type === 'canceled'
}

/** Coerces untrusted stored JSON into a filter, dropping anything malformed. */
export function parseFilter(value: unknown): IssueFilter {
  const source = (typeof value === 'object' && value !== null ? value : {}) as Record<string, unknown>
  const strings = (v: unknown) => (Array.isArray(v) ? v.filter((x): x is string => typeof x === 'string') : [])
  const numbers = (v: unknown) => (Array.isArray(v) ? v.filter((x): x is number => typeof x === 'number') : [])
  return {
    states: strings(source.states),
    priorities: numbers(source.priorities),
    assignees: strings(source.assignees),
    labels: strings(source.labels),
    projects: strings(source.projects),
  }
}
