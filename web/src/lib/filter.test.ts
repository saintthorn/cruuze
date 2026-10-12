import { describe, expect, it } from 'vitest'

import type { Issue, WorkflowState } from '@/sync/types'

import { emptyFilter, filterCount, isClosed, matchesFilter, NO_ASSIGNEE, NO_PROJECT, parseFilter, stateKey } from './filter'

const STATES: WorkflowState[] = [
  { id: 's-todo', teamId: 't', name: 'Todo', type: 'unstarted', color: '', position: 1 },
  { id: 's-done', teamId: 't', name: 'Done', type: 'completed', color: '', position: 3 },
  { id: 's-todo-2', teamId: 't2', name: 'TODO', type: 'unstarted', color: '', position: 1 },
]
const stateOf = (id: string) => STATES.find((s) => s.id === id)

function issue(overrides: Partial<Issue>): Issue {
  return {
    id: 'i',
    teamId: 't',
    number: 1,
    title: '',
    description: '',
    stateId: 's-todo',
    priority: 0,
    sortOrder: 0,
    labelIds: [],
    subscriberIds: [],
    createdAt: '',
    updatedAt: '',
    ...overrides,
  }
}

describe('matchesFilter', () => {
  it('matches everything when empty', () => {
    expect(matchesFilter(issue({}), emptyFilter(), stateOf)).toBe(true)
  })

  it('treats same-named states of different teams as one status', () => {
    const filter = { ...emptyFilter(), states: [stateKey({ type: 'unstarted', name: 'Todo' })] }
    expect(matchesFilter(issue({ stateId: 's-todo' }), filter, stateOf)).toBe(true)
    expect(matchesFilter(issue({ stateId: 's-todo-2' }), filter, stateOf)).toBe(true)
    expect(matchesFilter(issue({ stateId: 's-done' }), filter, stateOf)).toBe(false)
  })

  it('needs any value within a property and every property', () => {
    const filter = { ...emptyFilter(), priorities: [1, 2], labels: ['bug', 'perf'] }
    expect(matchesFilter(issue({ priority: 2, labelIds: ['perf'] }), filter, stateOf)).toBe(true)
    expect(matchesFilter(issue({ priority: 2, labelIds: ['docs'] }), filter, stateOf)).toBe(false)
    expect(matchesFilter(issue({ priority: 3, labelIds: ['bug'] }), filter, stateOf)).toBe(false)
    expect(filterCount(filter)).toBe(2)
  })

  it('can match unassigned issues', () => {
    const filter = { ...emptyFilter(), assignees: [NO_ASSIGNEE, 'ada'] }
    expect(matchesFilter(issue({}), filter, stateOf)).toBe(true)
    expect(matchesFilter(issue({ assigneeId: 'ada' }), filter, stateOf)).toBe(true)
    expect(matchesFilter(issue({ assigneeId: 'bob' }), filter, stateOf)).toBe(false)
  })
})

describe('project filter', () => {
  it('matches issues in the chosen projects, or in none', () => {
    const filter = { ...emptyFilter(), projects: ['p1', NO_PROJECT] }
    expect(matchesFilter(issue({ projectId: 'p1' }), filter, stateOf)).toBe(true)
    expect(matchesFilter(issue({}), filter, stateOf)).toBe(true)
    expect(matchesFilter(issue({ projectId: 'p2' }), filter, stateOf)).toBe(false)
  })
})

describe('isClosed', () => {
  it('is true for completed and canceled states only', () => {
    expect(isClosed(issue({ stateId: 's-done' }), stateOf)).toBe(true)
    expect(isClosed(issue({ stateId: 's-todo' }), stateOf)).toBe(false)
  })
})

describe('parseFilter', () => {
  it('keeps well-formed values and drops the rest', () => {
    expect(parseFilter({ states: ['a', 3], priorities: [1, 'x'], assignees: 'nope', projects: ['p'], extra: true })).toEqual({
      states: ['a'],
      priorities: [1],
      assignees: [],
      labels: [],
      projects: ['p'],
    })
    expect(parseFilter(null)).toEqual(emptyFilter())
  })
})
