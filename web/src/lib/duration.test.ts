import { describe, expect, it } from 'vitest'

import type { Activity, Issue, WorkflowState } from '@/sync/types'

import { formatClock, formatDuration, parseDuration, timeInProgress } from './duration'

describe('parseDuration', () => {
  it.each([
    ['1h 30m', 5400],
    ['1h30m', 5400],
    ['1.5h', 5400],
    ['90m', 5400],
    ['90', 5400],
    ['1:30', 5400],
    ['2 hours 5 min', 7500],
    ['45 minutes', 2700],
    ['1d', 28800],
    ['30s', 30],
    [' 2H ', 7200],
  ])('reads %s', (text, seconds) => {
    expect(parseDuration(text)).toBe(seconds)
  })

  it.each(['', 'soon', '0', '0m', '1h later', '-5', '1:75', 'm'])('refuses %j', (text) => {
    expect(parseDuration(text)).toBeNull()
  })
})

describe('formatDuration', () => {
  it.each([
    [0, '0m'],
    [20, '<1m'],
    [60, '1m'],
    [2700, '45m'],
    [3600, '1h'],
    [5400, '1h 30m'],
    [86400, '1d'],
    [97200, '1d 3h'],
  ])('%d seconds', (seconds, text) => {
    expect(formatDuration(seconds)).toBe(text)
  })

  it('formats a clock', () => {
    expect(formatClock(249)).toBe('0:04:09')
    expect(formatClock(3 * 3600 + 61)).toBe('3:01:01')
  })
})

describe('timeInProgress', () => {
  const states: Record<string, WorkflowState['type']> = { todo: 'unstarted', doing: 'started', review: 'started', done: 'completed' }
  const state = (id: string | undefined) => (id && states[id] ? ({ id, type: states[id] } as WorkflowState) : undefined)
  const at = (hour: number) => new Date(Date.UTC(2026, 0, 1, hour)).toISOString()
  const issue = (stateId: string) => ({ id: 'i', stateId, createdAt: at(0) }) as Issue
  const move = (from: string, to: string, hour: number) => ({ type: 'state', from, to, createdAt: at(hour) }) as Activity
  const now = Date.parse(at(10))

  it('is nothing for an issue that never started', () => {
    expect(timeInProgress(issue('todo'), [], state, now)).toBe(0)
  })

  it('counts from creation for an issue created in progress', () => {
    expect(timeInProgress(issue('doing'), [], state, now)).toBe(10 * 3600)
  })

  it('adds up every stay, including the current one', () => {
    const changes = [move('todo', 'doing', 1), move('doing', 'review', 2), move('review', 'todo', 4), move('todo', 'doing', 9)]
    expect(timeInProgress(issue('doing'), changes, state, now)).toBe(4 * 3600)
  })

  it('stops at completion and ignores deleted statuses', () => {
    expect(timeInProgress(issue('done'), [move('todo', 'doing', 1), move('doing', 'done', 3)], state, now)).toBe(2 * 3600)
    expect(timeInProgress(issue('done'), [move('todo', 'gone', 1), move('gone', 'done', 3)], state, now)).toBe(0)
  })
})
