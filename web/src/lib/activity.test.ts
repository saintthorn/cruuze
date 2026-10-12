import { describe, expect, it } from 'vitest'

import type { Activity } from '@/sync/types'

import { describeActivity, describeNotification } from './activity'
import { timeAgo } from './time'

const ADA = { id: 'ada', name: 'Ada', email: 'ada@example.com', role: 'admin' as const, joinedAt: '' }
const ctx = {
  user: (id: string | undefined) => (id === 'ada' ? ADA : undefined),
  state: (id: string) =>
    ({ todo: 'Todo', done: 'Done' })[id] ? { id, teamId: 't', name: { todo: 'Todo', done: 'Done' }[id]!, type: 'unstarted' as const, color: '', position: 0 } : undefined,
  label: (id: string) => (id === 'bug' ? { id, name: 'Bug', color: '' } : undefined),
  projectName: (id: string) => (id === 'launch' ? 'Launch' : undefined),
  cycleName: (id: string) => (id === 'c2' ? 'Cycle 2' : undefined),
  milestoneName: (id: string) => (id === 'beta' ? 'Beta' : undefined),
  issueName: (id: string) => (id === 'p' ? 'ENG-1' : undefined),
}

function activity(overrides: Partial<Activity>): Activity {
  return { id: 'a', issueId: 'i', type: 'created', createdAt: '', actorId: 'bob', ...overrides }
}

describe('describeActivity', () => {
  it('reads as a sentence fragment after the actor', () => {
    expect(describeActivity(activity({}), ctx)).toBe('created the issue')
    expect(describeActivity(activity({ type: 'state', from: 'todo', to: 'done' }), ctx)).toBe('changed status from Todo to Done')
    expect(describeActivity(activity({ type: 'priority', from: '0', to: '1' }), ctx)).toBe('set priority to Urgent')
    expect(describeActivity(activity({ type: 'title', from: 'Old', to: 'New' }), ctx)).toBe('changed the title from “Old” to “New”')
  })

  it('distinguishes assigning, self-assigning and unassigning', () => {
    expect(describeActivity(activity({ type: 'assignee', to: 'ada' }), ctx)).toBe('assigned the issue to Ada')
    expect(describeActivity(activity({ type: 'assignee', to: 'bob' }), ctx)).toBe('self-assigned the issue')
    expect(describeActivity(activity({ type: 'assignee', from: 'ada' }), ctx)).toBe('removed the assignee')
  })

  it('names labels, including ones deleted since', () => {
    expect(describeActivity(activity({ type: 'labels', addedLabelIds: ['bug'] }), ctx)).toBe('added label Bug')
    expect(describeActivity(activity({ type: 'labels', addedLabelIds: ['bug'], removedLabelIds: ['gone'] }), ctx)).toBe(
      'added label Bug and removed label a deleted label',
    )
  })

  it('handles cleared values and states that no longer exist', () => {
    expect(describeActivity(activity({ type: 'state', from: 'gone', to: 'done' }), ctx)).toBe('changed the status')
  })
})

describe('planning activities', () => {
  it('names the project, cycle and parent, or says they were removed', () => {
    expect(describeActivity(activity({ type: 'project', to: 'launch' }), ctx)).toBe('added the issue to Launch')
    expect(describeActivity(activity({ type: 'project', from: 'launch' }), ctx)).toBe('removed the issue from its project')
    expect(describeActivity(activity({ type: 'cycle', to: 'c2' }), ctx)).toBe('moved the issue to Cycle 2')
    expect(describeActivity(activity({ type: 'parent', to: 'p' }), ctx)).toBe('made this a sub-issue of ENG-1')
    expect(describeActivity(activity({ type: 'project', to: 'deleted' }), ctx)).toBe('added the issue to a project')
    expect(describeActivity(activity({ type: 'milestone', to: 'beta' }), ctx)).toBe('moved the issue to milestone Beta')
    expect(describeActivity(activity({ type: 'milestone', from: 'beta' }), ctx)).toBe('removed the issue from its milestone')
  })
})

describe('describeNotification', () => {
  it('names the actor, or "Someone" if they are gone', () => {
    const n = { id: 'n', issueId: 'i', createdAt: '', type: 'mentioned' as const }
    expect(describeNotification(n, ADA)).toBe('Ada mentioned you')
    expect(describeNotification({ ...n, type: 'assigned' }, undefined)).toBe('Someone assigned this to you')
  })

  it('says a status change without an actor was automatic', () => {
    const n = { id: 'n', issueId: 'i', createdAt: '', type: 'status' as const }
    expect(describeNotification({ ...n, actorId: 'ada' }, ADA)).toBe('Ada changed the status')
    expect(describeNotification(n, undefined)).toBe('The status changed')
  })
})

describe('timeAgo', () => {
  const now = new Date('2026-10-01T12:00:00Z')

  it('uses the largest sensible unit', () => {
    expect(timeAgo('2026-10-01T11:59:40Z', now)).toBe('just now')
    expect(timeAgo('2026-10-01T11:15:00Z', now)).toBe('45m ago')
    expect(timeAgo('2026-10-01T07:00:00Z', now)).toBe('5h ago')
    expect(timeAgo('2026-09-28T12:00:00Z', now)).toBe('3d ago')
  })

  it('switches to a date after a week', () => {
    expect(timeAgo('2026-09-01T12:00:00Z', now)).toBe('Sep 1')
    expect(timeAgo('2025-09-01T12:00:00Z', now)).toBe('Sep 1, 2025')
  })
})
