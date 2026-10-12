import 'fake-indexeddb/auto'

import { openDB } from 'idb'
import { expect, it } from 'vitest'

import { LocalDb } from './localdb'

it('removes cached estimates while preserving other queued offline edits', async () => {
  const name = 'upgrade-estimates-' + crypto.randomUUID()
  const old = await openDB(name, 10, {
    upgrade(db) {
      db.createObjectStore('records', { keyPath: ['model', 'key'] })
      db.createObjectStore('meta')
      db.createObjectStore('detailed')
      db.createObjectStore('outbox', { keyPath: 'seq', autoIncrement: true })
    },
  })
  await old.put('records', { model: 'Issue', key: 'i1', value: { id: 'i1', estimate: 3 } })
  await old.put('records', { model: 'Activity', key: 'a1', value: { id: 'a1', type: 'estimate' } })
  await old.put('meta', 42, 'lastSyncId')
  await old.put('detailed', true, 'i1')
  await old.put('outbox', { seq: 1, mutation: { type: 'issue.create', issue: { id: 'new', title: 'Offline issue', estimate: 5 } } })
  await old.put('outbox', { seq: 2, ackSyncId: 43, mutation: { type: 'issue.update', issueId: 'i1', patch: { title: 'Keep this edit', estimate: 8 } } })
  await old.put('outbox', { seq: 3, mutation: { type: 'issue.update', issueId: 'i1', patch: { estimate: null } } })
  await old.put('outbox', { seq: 4, mutation: { type: 'issue.update', issueId: 'i1', patch: { dueDate: '2026-12-24' } } })
  old.close()

  const upgraded = await LocalDb.open(name)
  try {
    const state = await upgraded.load()
    expect(state.records).toEqual([])
    expect(state.lastSyncId).toBeNull()
    expect(state.detailed).toEqual([])
    expect(state.outbox).toEqual([
      { seq: 1, mutation: { type: 'issue.create', issue: { id: 'new', title: 'Offline issue' } } },
      { seq: 2, ackSyncId: 43, mutation: { type: 'issue.update', issueId: 'i1', patch: { title: 'Keep this edit' } } },
      { seq: 4, mutation: { type: 'issue.update', issueId: 'i1', patch: { dueDate: '2026-12-24' } } },
    ])
  } finally {
    await upgraded.destroy()
  }
})
