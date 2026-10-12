import { describe, expect, it } from 'vitest'

import type { Issue, Relation, WorkflowState } from '@/sync/types'

import { findHygieneIssues } from './hygiene'
import { similarIssues, titleWords } from './similar'

const NOW = new Date('2026-10-01T12:00:00Z')
const daysAgo = (days: number) => new Date(NOW.getTime() - days * 86_400_000).toISOString()

const STATES: Record<string, WorkflowState['type']> = { backlog: 'backlog', todo: 'unstarted', doing: 'started', done: 'completed' }
const stateOf = (id: string) => ({ id, teamId: 't', name: id, type: STATES[id]!, color: '#000', position: 0 })

let counter = 0
function issue(overrides: Partial<Issue>): Issue {
  counter++
  return {
    id: `i${counter}`,
    teamId: 't',
    number: counter,
    title: `Issue ${counter}`,
    description: '',
    stateId: 'backlog',
    priority: 0,
    sortOrder: counter,
    labelIds: [],
    subscriberIds: [],
    createdAt: daysAgo(200),
    updatedAt: daysAgo(0),
    ...overrides,
  }
}

describe('findHygieneIssues', () => {
  const find = (issues: Issue[], relations: Relation[] = [], staleDays = 60) =>
    (findHygieneIssues({ issues, relations, stateOf, staleDays: () => staleDays, now: NOW }).get('t') ?? []).map(
      (f) => `${f.issue.id}:${f.kind}:${f.days}`,
    )

  it('flags unstarted issues nobody has touched for longer than the team allows', () => {
    const old = issue({ updatedAt: daysAgo(90) })
    const older = issue({ stateId: 'todo', updatedAt: daysAgo(120) })
    const recent = issue({ updatedAt: daysAgo(59) })
    const finished = issue({ stateId: 'done', updatedAt: daysAgo(400) })
    expect(find([old, older, recent, finished])).toEqual([`${older.id}:stale:120`, `${old.id}:stale:90`])
    // A team can turn the flag off.
    expect(find([old, older], [], 0)).toEqual([])
  })

  it('flags work in progress that is stuck, unowned, blocked or overdue', () => {
    const stuck = issue({ stateId: 'doing', assigneeId: 'ada', updatedAt: daysAgo(20) })
    const moving = issue({ stateId: 'doing', assigneeId: 'ada', updatedAt: daysAgo(3) })
    const unowned = issue({ stateId: 'doing', updatedAt: daysAgo(1) })
    const blocker = issue({ stateId: 'todo' })
    const blocked = issue({ stateId: 'doing', assigneeId: 'ada', updatedAt: daysAgo(9) })
    const unblocked = issue({ stateId: 'doing', assigneeId: 'ada', updatedAt: daysAgo(9) })
    const doneBlocker = issue({ stateId: 'done' })
    const late = issue({ stateId: 'todo', dueDate: '2026-09-28' })
    const relations: Relation[] = [
      { id: 'r1', issueId: blocker.id, relatedIssueId: blocked.id, type: 'blocks' },
      { id: 'r2', issueId: doneBlocker.id, relatedIssueId: unblocked.id, type: 'blocks' },
    ]
    expect(find([stuck, moving, unowned, blocker, blocked, unblocked, doneBlocker, late], relations).sort()).toEqual(
      [`${stuck.id}:stuck:20`, `${unowned.id}:unowned:1`, `${blocked.id}:blocked:9`, `${late.id}:overdue:3`].sort(),
    )
  })
})

describe('similarIssues', () => {
  it('reduces titles to their meaningful words', () => {
    expect(titleWords('Fix the login redirect crashes on Safari!')).toEqual(['login', 'redirect', 'crash', 'safari'])
    expect(titleWords('Add a')).toEqual([])
  })

  it('finds issues about the same thing and ranks the closest first', () => {
    const exact = issue({ title: 'Login redirect loops forever' })
    const close = issue({ title: 'Redirect after login goes to the wrong page on mobile' })
    const other = issue({ title: 'Invoice totals are wrong' })
    const oneWord = issue({ title: 'Login button is too small' })
    expect(similarIssues('Fix login redirect', [other, close, oneWord, exact]).map((i) => i.id)).toEqual([exact.id, close.id])
    // Too little to go on.
    expect(similarIssues('Login', [exact, close])).toEqual([])
  })
})
