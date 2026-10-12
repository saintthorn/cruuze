import { describe, expect, it } from 'vitest'

import type { Issue, Relation, WorkflowState } from '@/sync/types'

import { buildFocus, type FocusContext } from './focus'

const TYPES: Record<string, WorkflowState['type']> = { todo: 'unstarted', backlog: 'backlog', doing: 'started', done: 'completed', triage: 'triage' }
const all = new Map<string, Issue>()
let order = 0
const issue = (id: string, stateId: string, fields: Partial<Issue> = {}) => {
  const made = { id, stateId, priority: 0, sortOrder: order++, ...fields } as Issue
  all.set(id, made)
  return made
}
const blocks = (a: string, b: string) => ({ issueId: a, relatedIssueId: b, type: 'blocks' }) as Relation
const context = (relations: Relation[] = [], extra: Partial<FocusContext> = {}): FocusContext => ({
  stateType: (id) => TYPES[id],
  issue: (id) => all.get(id),
  relations,
  today: '2026-10-01',
  currentCycles: new Set(['c1']),
  ...extra,
})
const ids = (items: { issue: Issue }[]) => items.map((i) => i.issue.id)

describe('buildFocus', () => {
  it('separates what is started from what is next, and drops what is closed or in triage', () => {
    const mine = [issue('a', 'doing'), issue('b', 'todo'), issue('c', 'done'), issue('d', 'triage'), issue('e', 'backlog'), issue('f', 'todo', { archivedAt: '2026-01-01' })]
    const focus = buildFocus(mine, context())
    expect(ids(focus.inProgress)).toEqual(['a'])
    expect(ids(focus.next)).toEqual(['b', 'e'])
    expect(focus.waiting).toEqual([])
  })

  it('puts first what unblocks others, then late, due soon, this cycle, priority', () => {
    const mine = [
      issue('plain', 'todo', { priority: 2 }),
      issue('urgent', 'todo', { priority: 1 }),
      issue('cycle', 'todo', { cycleId: 'c1', priority: 4 }),
      issue('soon', 'todo', { dueDate: '2026-10-05' }),
      issue('late', 'todo', { dueDate: '2026-09-20' }),
      issue('unblocks', 'todo'),
      issue('far', 'todo', { dueDate: '2026-12-01', priority: 1 }),
    ]
    issue('theirs', 'todo')
    const focus = buildFocus(mine, context([blocks('unblocks', 'theirs')]))
    expect(ids(focus.next)).toEqual(['unblocks', 'late', 'soon', 'cycle', 'far', 'urgent', 'plain'])
    expect(focus.next[0]!.blocks.map((i) => i.id)).toEqual(['theirs'])
    expect(focus.next[1]!.overdue).toBe(true)
  })

  it('sets aside what is stuck behind open work, but not behind finished work', () => {
    const mine = [issue('stuck', 'todo'), issue('free', 'todo')]
    issue('open-blocker', 'doing')
    issue('done-blocker', 'done')
    const focus = buildFocus(mine, context([blocks('open-blocker', 'stuck'), blocks('done-blocker', 'free')]))
    expect(ids(focus.waiting)).toEqual(['stuck'])
    expect(focus.waiting[0]!.blockedBy.map((i) => i.id)).toEqual(['open-blocker'])
    expect(ids(focus.next)).toEqual(['free'])
  })

  it('suggests only so many and counts the rest', () => {
    const mine = Array.from({ length: 10 }, (_, i) => issue(`n${i}`, 'todo'))
    const focus = buildFocus(mine, context([], { limit: 3 }))
    expect(focus.next).toHaveLength(3)
    expect(focus.later).toBe(7)
  })
})
