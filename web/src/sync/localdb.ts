import { type DBSchema, deleteDB, type IDBPDatabase, openDB } from 'idb'

import type { Mutation } from './mutations'
import { recordKey, type StoredRecord, type SyncAction } from './types'

/** A mutation waiting in the outbox. */
export interface OutboxEntry {
  /** Assigned by the database; orders the outbox. */
  seq: number
  mutation: Mutation
  /** Set once the server accepted it: the sync ID that includes the change. */
  ackSyncId?: number
}

interface Schema extends DBSchema {
  records: { key: [string, string]; value: StoredRecord }
  meta: { key: string; value: number }
  outbox: { key: number; value: OutboxEntry }
  /** IDs of the issues whose discussion (comments, activity) is cached in full. */
  detailed: { key: string; value: true }
}

// Bump when a stored shape changes incompatibly. Upgrading drops the cached
// records, so the client bootstraps again; the outbox is kept.
const VERSION = 11
const LAST_SYNC_ID = 'lastSyncId'

/**
 * The on-disk copy of a workspace: confirmed records, the sync ID they are
 * current as of, and the outbox. Several tabs may share one database.
 */
export class LocalDb {
  private constructor(
    private readonly db: IDBPDatabase<Schema>,
    readonly name: string,
  ) {}

  static async open(name: string): Promise<LocalDb> {
    const db = await openDB<Schema>(name, VERSION, {
      async upgrade(db, oldVersion, _newVersion, transaction) {
        if (db.objectStoreNames.contains('records')) db.deleteObjectStore('records')
        if (db.objectStoreNames.contains('meta')) db.deleteObjectStore('meta')
        if (db.objectStoreNames.contains('detailed')) db.deleteObjectStore('detailed')
        db.createObjectStore('detailed')
        db.createObjectStore('records', { keyPath: ['model', 'key'] })
        db.createObjectStore('meta')
        if (!db.objectStoreNames.contains('outbox')) {
          db.createObjectStore('outbox', { keyPath: 'seq', autoIncrement: true })
        }
        if (oldVersion > 0 && oldVersion < 11) {
          // Preserve offline edits while removing the retired estimate field.
          let cursor = await transaction.objectStore('outbox').openCursor()
          while (cursor) {
            const entry = cursor.value
            const mutation = entry.mutation
            if (mutation.type === 'issue.create' && 'estimate' in mutation.issue) {
              delete (mutation.issue as { estimate?: unknown }).estimate
              await cursor.update(entry)
            } else if (mutation.type === 'issue.update' && 'estimate' in mutation.patch) {
              delete (mutation.patch as { estimate?: unknown }).estimate
              if (Object.keys(mutation.patch).length === 0) await cursor.delete()
              else await cursor.update(entry)
            }
            cursor = await cursor.continue()
          }
        }
      },
      // Another tab wants to delete or upgrade the database (e.g. on sign-out);
      // holding it open would block that tab.
      blocking() {
        db.close()
      },
    })
    return new LocalDb(db, name)
  }

  /** Reads everything; `lastSyncId` is null if the cache has never been filled. */
  async load(): Promise<{ lastSyncId: number | null; records: StoredRecord[]; outbox: OutboxEntry[]; detailed: string[] }> {
    const tx = this.db.transaction(['records', 'meta', 'outbox', 'detailed'])
    const [records, lastSyncId, outbox, detailed] = await Promise.all([
      tx.objectStore('records').getAll(),
      tx.objectStore('meta').get(LAST_SYNC_ID),
      tx.objectStore('outbox').getAll(),
      tx.objectStore('detailed').getAllKeys(),
    ])
    return { lastSyncId: lastSyncId ?? null, records, outbox, detailed }
  }

  /** Replaces the cached records with a bootstrap snapshot. */
  async replaceAll(lastSyncId: number, records: readonly StoredRecord[]) {
    const tx = this.db.transaction(['records', 'meta', 'detailed'], 'readwrite')
    const store = tx.objectStore('records')
    void store.clear()
    // A snapshot has no discussions; they are fetched again as issues are opened.
    void tx.objectStore('detailed').clear()
    for (const record of records) void store.put(record)
    void tx.objectStore('meta').put(lastSyncId, LAST_SYNC_ID)
    await tx.done
  }

  /**
   * Caches an issue's discussion. `records` are as of some sync ID and
   * `replay` holds the later changes already applied on top of them;
   * `lastSyncId` is how far this tab has applied the log.
   */
  async addDetails(issueId: string, records: readonly StoredRecord[], replay: readonly SyncAction[], lastSyncId: number) {
    const tx = this.db.transaction(['records', 'meta', 'detailed'], 'readwrite')
    // Another tab has cached changes this one hasn't seen yet. Writing our
    // older view of these records would undo them, so leave the cache as it
    // is; the discussion is simply fetched again after a reload.
    const stored = (await tx.objectStore('meta').get(LAST_SYNC_ID)) ?? -1
    if (stored <= lastSyncId) {
      const store = tx.objectStore('records')
      for (const record of records) void store.put(record)
      for (const action of replay) {
        const key = recordKey(action.model, action.data)
        if (action.action === 'D') void store.delete([action.model, key])
        else void store.put({ model: action.model, key, value: action.data })
      }
      void tx.objectStore('detailed').put(true, issueId)
    }
    await tx.done
  }

  /** Persists confirmed changes and the sync ID the cache is then current as of. */
  async applyActions(actions: readonly SyncAction[], lastSyncId: number) {
    const tx = this.db.transaction(['records', 'meta'], 'readwrite')
    const store = tx.objectStore('records')
    // Another tab may already have written these, and possibly newer ones;
    // re-applying an older snapshot over a newer one would corrupt the cache.
    const stored = (await tx.objectStore('meta').get(LAST_SYNC_ID)) ?? -1
    for (const action of actions) {
      if (action.syncId <= stored) continue
      const key = recordKey(action.model, action.data)
      const archived = action.model === 'Issue' && 'archivedAt' in action.data && action.data.archivedAt
      if (action.action === 'D' || archived) void store.delete([action.model, key])
      else void store.put({ model: action.model, key, value: action.data })
    }
    if (lastSyncId > stored) void tx.objectStore('meta').put(lastSyncId, LAST_SYNC_ID)
    await tx.done
  }

  /** Appends a mutation to the outbox and returns its entry. */
  async addMutation(mutation: Mutation): Promise<OutboxEntry> {
    // The key is generated on insert, so the value goes in without one.
    const seq = await this.db.add('outbox', { mutation } as OutboxEntry)
    return { seq, mutation }
  }

  getMutation(seq: number): Promise<OutboxEntry | undefined> {
    return this.db.get('outbox', seq)
  }

  async ackMutation(seq: number, ackSyncId: number) {
    const tx = this.db.transaction('outbox', 'readwrite')
    const entry = await tx.store.get(seq)
    if (entry) void tx.store.put({ ...entry, ackSyncId })
    await tx.done
  }

  async deleteMutations(seqs: readonly number[]) {
    const tx = this.db.transaction('outbox', 'readwrite')
    for (const seq of seqs) void tx.store.delete(seq)
    await tx.done
  }

  close() {
    this.db.close()
  }

  /** Closes the database and deletes it from this browser. */
  async destroy() {
    this.db.close()
    await new Promise<void>((resolve, reject) => {
      // If another tab is slow to let go, the deletion still completes once
      // it does; don't keep the caller waiting for that.
      deleteDB(this.name, { blocked: () => resolve() }).then(resolve, reject)
    })
  }
}
