import { describe, expect, it } from 'vitest'

import type { Issue, TeamRecord, UserRecord, WorkflowState } from '@/sync/types'

import { formatDateRange, formatDueDate, groupIssues, isOverdue, issueIdentifier, progressOf, sortIssues } from './issues'

const state = (id: string, type: WorkflowState['type'], position: number, name = id): WorkflowState => ({
  id,
  teamId: 't',
  name,
  type,
  color: '#000',
  position,
})
const STATES = [state('Backlog', 'backlog', 0), state('Todo', 'unstarted', 1), state('In Progress', 'started', 2), state('Done', 'completed', 3)]
const ADA: UserRecord = { id: 'ada', name: 'Ada', email: 'ada@example.com', role: 'admin', joinedAt: '' }

let n = 0
function issue(overrides: Partial<Issue>): Issue {
  n++
  return {
    id: `i${n}`,
    teamId: 't',
    number: n,
    title: `Issue ${n}`,
    description: '',
    stateId: 'Backlog',
    priority: 0,
    sortOrder: n,
    labelIds: [],
    subscriberIds: [],
    createdAt: `2026-01-${String(n).padStart(2, '0')}`,
    updatedAt: `2026-01-${String(n).padStart(2, '0')}`,
    ...overrides,
  }
}

const ctx = {
  state: (id: string) => STATES.find((s) => s.id === id),
  user: (id: string | undefined) => (id === 'ada' ? ADA : undefined),
  project: (id: string | undefined) => (id === 'p1' ? { id: 'p1', name: 'Launch', description: '', status: 'started' as const, color: '', createdAt: '' } : undefined),
}

describe('sortIssues', () => {
  it('orders by priority with urgent first and none last, then manually', () => {
    const issues = [
      issue({ id: 'none', priority: 0, sortOrder: 1 }),
      issue({ id: 'low', priority: 4, sortOrder: 2 }),
      issue({ id: 'urgent-b', priority: 1, sortOrder: 4 }),
      issue({ id: 'urgent-a', priority: 1, sortOrder: 3 }),
    ]
    expect(sortIssues(issues, 'priority').map((i) => i.id)).toEqual(['urgent-a', 'urgent-b', 'low', 'none'])
  })

  it('puts issues without a due date last', () => {
    const issues = [issue({ id: 'none' }), issue({ id: 'late', dueDate: '2026-06-01' }), issue({ id: 'soon', dueDate: '2026-02-01' })]
    expect(sortIssues(issues, 'dueDate').map((i) => i.id)).toEqual(['soon', 'late', 'none'])
  })

  it('orders newest first and leaves the input alone', () => {
    const issues = [issue({ id: 'old', createdAt: '2026-01-01' }), issue({ id: 'new', createdAt: '2026-03-01' })]
    expect(sortIssues(issues, 'created').map((i) => i.id)).toEqual(['new', 'old'])
    expect(issues.map((i) => i.id)).toEqual(['old', 'new'])
  })
})

describe('groupIssues', () => {
  it('groups by status with work in progress first and empty groups dropped', () => {
    const issues = [issue({ stateId: 'Backlog' }), issue({ stateId: 'Done' }), issue({ stateId: 'In Progress' }), issue({ stateId: 'Backlog' })]
    const groups = groupIssues(issues, 'status', ctx)
    expect(groups.map((g) => [g.label, g.issues.length])).toEqual([
      ['In Progress', 1],
      ['Backlog', 2],
      ['Done', 1],
    ])
    expect(groups[0]!.state?.type).toBe('started')
  })

  it('groups by priority and by assignee', () => {
    const issues = [issue({ priority: 0 }), issue({ priority: 2, assigneeId: 'ada' }), issue({ priority: 1 })]
    expect(groupIssues(issues, 'priority', ctx).map((g) => g.label)).toEqual(['Urgent', 'High', 'No priority'])
    expect(groupIssues(issues, 'assignee', ctx).map((g) => [g.label, g.issues.length])).toEqual([
      ['Ada', 1],
      ['No assignee', 2],
    ])
  })

  it('returns one unlabeled group, or none, without grouping', () => {
    expect(groupIssues([issue({})], 'none', ctx)).toHaveLength(1)
    expect(groupIssues([], 'none', ctx)).toEqual([])
  })
})

describe('progress and projects', () => {
  it('groups by project with unassigned issues last', () => {
    const issues = [issue({}), issue({ projectId: 'p1' }), issue({ projectId: 'deleted' })]
    expect(groupIssues(issues, 'project', ctx).map((g) => [g.label, g.issues.length])).toEqual([
      ['Launch', 1],
      ['No project', 2],
    ])
  })

  it('measures completion, leaving canceled issues out', () => {
    const states = [...STATES, state('Canceled', 'canceled', 4)]
    const stateOf = (id: string) => states.find((s) => s.id === id)
    const issues = [issue({ stateId: 'Done' }), issue({ stateId: 'In Progress' }), issue({ stateId: 'Backlog' }), issue({ stateId: 'Canceled' })]
    expect(progressOf(issues, stateOf)).toEqual({ total: 3, done: 1, started: 1, percent: 33 })
    expect(progressOf([], stateOf).percent).toBe(0)
  })

  it('formats a date range', () => {
    expect(formatDateRange('2026-09-28', '2026-10-11')).toBe('Sep 28 – Oct 11')
  })
})

describe('issueIdentifier', () => {
  it('uses the team key, with a placeholder until the server assigns a number', () => {
    // Only the fields under test; the rest of a team record doesn't matter here.
    const team = { id: 't', name: 'Engineering', key: 'ENG' } as TeamRecord
    expect(issueIdentifier(issue({ number: 12 }), team)).toBe('ENG-12')
    expect(issueIdentifier(issue({ number: 0 }), team)).toBe('ENG-…')
  })
})

describe('due dates', () => {
  const now = new Date(2026, 9, 1, 15, 30) // Oct 1, 2026, mid-afternoon

  it('reads relative to today', () => {
    expect(formatDueDate('2026-10-01', now)).toBe('Today')
    expect(formatDueDate('2026-10-02', now)).toBe('Tomorrow')
    expect(formatDueDate('2026-09-30', now)).toBe('Yesterday')
    expect(formatDueDate('2026-12-24', now)).toBe('Dec 24')
    expect(formatDueDate('2027-01-15', now)).toBe('Jan 15, 2027')
  })

  it('is overdue only once the day has passed', () => {
    expect(isOverdue('2026-09-30', now)).toBe(true)
    expect(isOverdue('2026-10-01', now)).toBe(false)
    expect(isOverdue('2026-10-02', now)).toBe(false)
  })
})
