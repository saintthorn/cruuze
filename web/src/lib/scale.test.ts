import { describe, expect, it } from 'vitest'

import type { Issue, WorkflowState } from '@/sync/types'

import { emptyFilter, matchesFilter } from './filter'
import { groupIssues, sortIssues } from './issues'

// What every list does on every change: filter, sort, group. These budgets
// are generous multiples of what it takes on a laptop; they are here to
// catch an accidental quadratic, not to benchmark.
describe('at 50,000 issues', () => {
  const STATES: WorkflowState[] = ['backlog', 'unstarted', 'started', 'completed', 'canceled'].map((type, position) => ({
    id: `s${position}`,
    teamId: 't',
    name: type,
    type: type as WorkflowState['type'],
    color: '#000',
    position,
  }))
  const stateOf = (id: string) => STATES[Number(id.slice(1))]

  const issues: Issue[] = Array.from({ length: 50_000 }, (_, n) => ({
    id: `issue-${n}`,
    teamId: 't',
    number: n + 1,
    title: `Issue ${n}`,
    description: '',
    stateId: `s${n % 5}`,
    priority: n % 5,
    sortOrder: (n * 7919) % 50_000,
    labelIds: n % 3 === 0 ? ['bug'] : [],
    subscriberIds: [],
    createdAt: '2026-01-01T00:00:00Z',
    updatedAt: '2026-01-01T00:00:00Z',
  }))

  function timed(run: () => unknown): number {
    const start = performance.now()
    run()
    return performance.now() - start
  }

  it('filters, sorts and groups a list in well under a frame budget of slack', () => {
    const filter = { ...emptyFilter(), priorities: [1, 2], labels: ['bug'] }
    let shown: Issue[] = []
    const filtering = timed(() => (shown = issues.filter((issue) => matchesFilter(issue, filter, stateOf))))
    expect(shown.length).toBeGreaterThan(1000)
    expect(filtering).toBeLessThan(250)

    let sorted: Issue[] = []
    expect(timed(() => (sorted = sortIssues(issues, 'manual')))).toBeLessThan(500)
    expect(sorted[0]!.sortOrder).toBe(0)

    const grouping = timed(() => {
      const groups = groupIssues(sorted, 'status', { state: stateOf, user: () => undefined, project: () => undefined })
      expect(groups).toHaveLength(5)
      expect(groups[0]!.issues).toHaveLength(10_000)
    })
    expect(grouping).toBeLessThan(500)
  })
})
