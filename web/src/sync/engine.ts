import { reactive, ref, shallowRef } from 'vue'

import type { LocalDb } from './localdb'
import { type Mutation, MutationRejected } from './mutations'
import { ObjectPool } from './pool'
import {
  type Issue,
  type IssueDetails,
  type ModelName,
  recordKey,
  type StoredRecord,
  type SyncAction,
  type SyncBootstrap,
  type SyncMessage,
} from './types'

export interface SyncSocket {
  close(): void
}

export interface SocketHandlers {
  onOpen(): void
  onMessage(message: SyncMessage): void
  onClose(): void
}

/** Thrown by a transport when the user can no longer see the workspace. */
export class AccessLost extends Error {
  constructor() {
    super('You no longer have access to this workspace.')
    this.name = 'AccessLost'
  }
}

/** How the engine reaches the server. Swapped out in tests. */
export interface Transport {
  /** Rejects with AccessLost if the user is no longer a member. */
  bootstrap(): Promise<SyncBootstrap>
  /** An issue's comments, reactions and activity, which the bootstrap omits. */
  issueDetails(issueId: string): Promise<IssueDetails>
  /** One issue by ID or KEY-number, archived or not; null if there is none to see. */
  lookupIssue(ref: string): Promise<Issue | null>
  /** Opens the sync socket, which replays the log after the given sync ID. */
  connect(after: number, handlers: SocketHandlers): SyncSocket
  /**
   * Sends a mutation and resolves to the sync ID that includes it. Rejects
   * with MutationRejected if the server refused it for good.
   */
  send(mutation: Mutation): Promise<number>
  /**
   * Sends several issue updates as one request that applies all or none of
   * them. Optional: without it, updates go one at a time.
   */
  sendIssueUpdates?(mutations: IssueUpdate[]): Promise<number>
}

type IssueUpdate = Extract<Mutation, { type: 'issue.update' }>

export interface EngineHooks {
  /** A mutation was refused by the server and has been rolled back. */
  onRejected(mutation: Mutation, message: string): void
  /** The socket closed before it ever opened, e.g. because the session ended. */
  onConnectFailed?(): void
  /** The user was removed from the workspace; the engine has stopped. */
  onAccessLost?(): void
}

/**
 * - `loading`: reading the local cache or bootstrapping
 * - `connecting`: opening the socket for the first time
 * - `live`: receiving changes as they happen
 * - `offline`: the socket dropped; retrying in the background
 */
export type SyncStatus = 'loading' | 'connecting' | 'live' | 'offline'

interface Pending {
  /** Outbox key; unset until the entry has been written to IndexedDB. */
  seq?: number
  mutation: Mutation
  ackSyncId?: number
  /** Settles when the write to IndexedDB has finished, successfully or not. */
  saved: Promise<void>
}

const MAX_RETRY_DELAY = 30_000
/** How many issue updates go into one request. */
const MAX_BATCH = 100
/** How many applied changes are remembered for replaying over late-arriving details. */
const RECENT_ACTIONS = 2000
/** How many received changes `feed` keeps. */
const FEED_LENGTH = 5
/** The models that make up an issue's discussion and are loaded on demand. */
const DETAIL_MODELS: readonly ModelName[] = ['Comment', 'Reaction', 'Activity']

/** Delay before retry number `attempt` (from 0): exponential with jitter. */
function retryDelay(attempt: number): number {
  const ceiling = Math.min(MAX_RETRY_DELAY, 500 * 2 ** attempt)
  return ceiling / 2 + Math.random() * (ceiling / 2)
}

const BOOTSTRAP_MODELS = {
  users: 'User',
  teams: 'Team',
  teamMembers: 'TeamMember',
  workflowStates: 'WorkflowState',
  labels: 'Label',
  issues: 'Issue',
  comments: 'Comment',
  attachments: 'Attachment',
  notifications: 'Notification',
  projects: 'Project',
  milestones: 'Milestone',
  cycles: 'Cycle',
  relations: 'Relation',
  templates: 'Template',
  views: 'View',
  favorites: 'Favorite',
  pullRequests: 'PullRequest',
  timeEntries: 'TimeEntry',
  initiatives: 'Initiative',
  projectDependencies: 'ProjectDependency',
  projectUpdates: 'ProjectUpdate',
  customerRequests: 'CustomerRequest',
} as const satisfies Record<Exclude<keyof SyncBootstrap, 'lastSyncId'>, ModelName>

function detailRecords(details: IssueDetails): StoredRecord[] {
  return [
    ...details.comments.map((value): StoredRecord => ({ model: 'Comment', key: recordKey('Comment', value), value })),
    ...details.reactions.map((value): StoredRecord => ({ model: 'Reaction', key: recordKey('Reaction', value), value })),
    ...details.activities.map((value): StoredRecord => ({ model: 'Activity', key: recordKey('Activity', value), value })),
  ]
}

function bootstrapRecords(snapshot: SyncBootstrap): StoredRecord[] {
  const records: StoredRecord[] = []
  for (const [field, model] of Object.entries(BOOTSTRAP_MODELS) as [keyof typeof BOOTSTRAP_MODELS, ModelName][]) {
    for (const value of snapshot[field]) records.push({ model, key: recordKey(model, value), value })
  }
  return records
}

/**
 * Keeps a local copy of one workspace in step with the server.
 *
 * Reads come from the pool, which is filled from IndexedDB on start and kept
 * current by the sync socket. Writes are applied to the pool at once, queued
 * in the outbox, and sent in order; each stays "pending" until the socket has
 * delivered the change that confirms it.
 */
export class SyncEngine {
  readonly pool = new ObjectPool()
  readonly status = ref<SyncStatus>('loading')
  /** Mutations not yet confirmed by the server. */
  readonly pendingCount = ref(0)
  /**
   * IDs of the issues whose discussion is in the pool in full. Reactive.
   * Other issues may still have some of their comments here: the ones that
   * arrived over the socket, and the ones the inbox quotes.
   */
  readonly detailed = reactive(new Set<string>())
  /** The sync ID the pool is current as of, for display. */
  readonly cursor = ref(0)
  /** The last few changes received, newest last, for display. */
  readonly feed = shallowRef<Pick<SyncAction, 'syncId' | 'model' | 'action'>[]>([])

  private lastSyncId = 0
  /** Detail fetches in flight, so an issue is only asked for once at a time. */
  private loadingDetails = new Map<string, Promise<void>>()
  /** The changes applied most recently, oldest first. */
  private recent: SyncAction[] = []
  /** Every change after this sync ID that has been applied is in `recent`. */
  private recentFloor = 0
  /** Counts bootstraps, to spot details fetched for a pool since replaced. */
  private generation = 0
  private outbox: Pending[] = []
  private socket: SyncSocket | null = null
  private needsBootstrap = false
  private stopped = false

  /** Serializes handling of socket messages. */
  private work: Promise<void> = Promise.resolve()
  private connectAttempt = 0
  private connectTimer: ReturnType<typeof setTimeout> | undefined
  private flushing = false
  private flushAttempt = 0
  private flushTimer: ReturnType<typeof setTimeout> | undefined

  constructor(
    private readonly db: LocalDb,
    private readonly transport: Transport,
    private readonly hooks: EngineHooks,
  ) {}

  /**
   * Resolves once local data is ready to show: straight from the cache if
   * there is one, otherwise after a bootstrap (which needs the network).
   */
  async start() {
    const cached = await this.db.load()
    this.outbox = cached.outbox.map((entry) => ({ ...entry, saved: Promise.resolve() }))
    if (cached.lastSyncId === null) {
      await this.bootstrap()
    } else {
      this.lastSyncId = this.recentFloor = this.cursor.value = cached.lastSyncId
      this.pool.load(cached.records)
      for (const issueId of cached.detailed) this.detailed.add(issueId)
    }
    this.refreshPending()
    if (this.stopped) return

    globalThis.addEventListener?.('online', this.onOnline)
    this.status.value = 'connecting'
    void this.connect()
    void this.flush()
  }

  stop() {
    this.stopped = true
    globalThis.removeEventListener?.('online', this.onOnline)
    clearTimeout(this.connectTimer)
    clearTimeout(this.flushTimer)
    const socket = this.socket
    this.socket = null
    socket?.close()
  }

  /** Applies a change locally right away and queues it for the server. */
  mutate(mutation: Mutation) {
    const entry: Pending = { mutation, saved: Promise.resolve() }
    entry.saved = this.db.addMutation(mutation).then(
      (stored) => {
        entry.seq = stored.seq
      },
      (err) => {
        // Still sent from memory; it just won't survive a reload.
        console.warn('sync: could not persist mutation', err)
      },
    )
    this.outbox.push(entry)
    this.refreshPending()
    void this.flush()
  }

  // --- reading ---------------------------------------------------------

  /**
   * Makes sure an issue's comments, reactions and activity are in the pool.
   * They aren't part of the bootstrap; once fetched, the socket keeps them
   * current like everything else. Rejects if the server can't be reached.
   */
  loadDetails(issueId: string): Promise<void> {
    if (this.detailed.has(issueId)) return Promise.resolve()
    let loading = this.loadingDetails.get(issueId)
    if (!loading) {
      loading = this.fetchDetails(issueId).finally(() => this.loadingDetails.delete(issueId))
      this.loadingDetails.set(issueId, loading)
    }
    return loading
  }

  /**
   * Fetches an issue that isn't in the local copy (archived issues aren't)
   * and adds it to the pool for this session. Resolves to undefined if the
   * server has no such issue for this user.
   */
  async fetchIssue(ref: string): Promise<Issue | undefined> {
    const issue = await this.transport.lookupIssue(ref)
    if (!issue || this.stopped) return undefined
    await this.enqueue(() => this.pool.merge([{ model: 'Issue', key: issue.id, value: issue }]))
    return issue
  }

  private async fetchDetails(issueId: string) {
    for (let attempt = 0; attempt < 3; attempt++) {
      const generation = this.generation
      const details = await this.transport.issueDetails(issueId)
      // In line with socket messages, so the pool can't change mid-merge.
      if (await this.enqueue(() => this.applyDetails(issueId, details, generation))) return
    }
    throw new Error('sync: could not load issue details')
  }

  /** Merges fetched details into the pool; false means they must be fetched again. */
  private applyDetails(issueId: string, details: IssueDetails, generation: number): boolean {
    if (this.stopped) return true
    // The details are as of details.syncId. Changes after that which already
    // reached the pool have to be applied again on top, or the older details
    // would undo them. That needs those changes to still be remembered, and
    // the pool to be the one the details were fetched for.
    if (generation !== this.generation || details.syncId < this.recentFloor) return false

    const records = detailRecords(details)
    const replay = this.recent.filter((a) => a.syncId > details.syncId && DETAIL_MODELS.includes(a.model))
    this.pool.merge(records)
    this.pool.apply(replay)
    this.detailed.add(issueId)
    void this.db.addDetails(issueId, records, replay, this.lastSyncId).catch((err) => {
      console.warn('sync: could not cache issue details', err)
    })
    return true
  }

  /** Runs `fn` after the socket messages already queued, and before later ones. */
  private enqueue<T>(fn: () => T | Promise<T>): Promise<T> {
    const result = this.work.then(fn)
    this.work = result.then(
      () => {},
      () => {},
    )
    return result
  }

  private remember(actions: readonly SyncAction[]) {
    this.recent.push(...actions)
    const excess = this.recent.length - RECENT_ACTIONS
    if (excess > 0) {
      this.recentFloor = this.recent[excess - 1]!.syncId
      this.recent.splice(0, excess)
    }
  }

  private async bootstrap() {
    const snapshot = await this.transport.bootstrap()
    const records = bootstrapRecords(snapshot)
    this.generation++
    this.detailed.clear()
    this.recent = []
    this.recentFloor = snapshot.lastSyncId
    this.pool.load(records)
    this.lastSyncId = this.cursor.value = snapshot.lastSyncId
    this.settleConfirmed()
    await this.db.replaceAll(snapshot.lastSyncId, records).catch((err) => {
      console.warn('sync: could not cache bootstrap', err)
    })
  }

  private async connect() {
    clearTimeout(this.connectTimer)
    if (this.stopped || this.socket) return

    if (this.needsBootstrap) {
      try {
        await this.bootstrap()
        this.needsBootstrap = false
      } catch (err) {
        if (err instanceof AccessLost) {
          // Removed from the workspace: there is nothing left to sync.
          this.stop()
          this.hooks.onAccessLost?.()
          return
        }
        this.retryConnect()
        return
      }
      if (this.stopped || this.socket) return
    }

    let opened = false
    const socket = this.transport.connect(this.lastSyncId, {
      onOpen: () => {
        if (socket !== this.socket) return
        opened = true
        this.connectAttempt = 0
        this.status.value = 'live'
        void this.flush()
      },
      onMessage: (message) => {
        if (socket !== this.socket) return
        void this.enqueue(() => this.handle(socket, message))
      },
      onClose: () => {
        if (socket !== this.socket) return
        this.socket = null
        if (!opened) this.hooks.onConnectFailed?.()
        this.retryConnect()
      },
    })
    this.socket = socket
  }

  private retryConnect() {
    if (this.stopped) return
    this.status.value = 'offline'
    clearTimeout(this.connectTimer)
    this.connectTimer = setTimeout(() => void this.connect(), retryDelay(this.connectAttempt++))
  }

  private onOnline = () => {
    this.connectAttempt = 0
    this.flushAttempt = 0
    void this.connect()
    void this.flush()
  }

  private handle(socket: SyncSocket, message: SyncMessage) {
    if (socket !== this.socket) return

    if (message.type === 'reset') {
      // Our copy can't be patched; start over from a fresh snapshot.
      this.socket = null
      socket.close()
      this.needsBootstrap = true
      return this.connect()
    }

    const actions = message.actions.filter((a) => a.syncId > this.lastSyncId)
    this.pool.apply(actions)
    this.remember(actions)
    if (actions.length > 0) {
      const latest = actions.slice(-FEED_LENGTH).map(({ syncId, model, action }) => ({ syncId, model, action }))
      this.feed.value = [...this.feed.value, ...latest].slice(-FEED_LENGTH)
    }
    this.lastSyncId = this.cursor.value = Math.max(this.lastSyncId, message.lastSyncId)
    this.settleConfirmed()
    return this.db.applyActions(actions, this.lastSyncId).catch((err) => {
      console.warn('sync: could not cache changes', err)
    })
  }

  // --- writing ---------------------------------------------------------

  /** Sends queued mutations in order, one at a time. */
  private async flush() {
    if (this.flushing || this.stopped) return
    this.flushing = true
    clearTimeout(this.flushTimer)
    try {
      for (;;) {
        const entry = this.outbox.find((e) => e.ackSyncId === undefined)
        if (!entry || this.stopped) break
        await entry.saved
        try {
          const batch = this.issueUpdatesFrom(entry)
          if (batch.length > 1 && (await this.deliverBatch(batch))) {
            this.flushAttempt = 0
            this.settleConfirmed()
            continue
          }
          const ack = await this.withOutboxLock(() => this.deliver(entry))
          if (ack === null) this.drop(entry)
          else entry.ackSyncId = ack
          this.flushAttempt = 0
        } catch (err) {
          if (err instanceof MutationRejected) {
            this.drop(entry)
            this.hooks.onRejected(entry.mutation, err.message)
            continue
          }
          // Offline or a server hiccup: keep the order, try again later.
          this.flushTimer = setTimeout(() => void this.flush(), retryDelay(this.flushAttempt++))
          break
        }
        this.settleConfirmed()
      }
    } finally {
      this.flushing = false
    }
  }

  /**
   * The run of issue updates at the head of the outbox, starting with
   * `first`: what a bulk edit leaves there. Sending them together turns a
   * few hundred requests into a couple.
   */
  private issueUpdatesFrom(first: Pending): Pending[] {
    if (!this.transport.sendIssueUpdates || first.mutation.type !== 'issue.update') return []
    const run: Pending[] = []
    for (const entry of this.outbox.slice(this.outbox.indexOf(first))) {
      if (entry.mutation.type !== 'issue.update' || entry.ackSyncId !== undefined || run.length === MAX_BATCH) break
      run.push(entry)
    }
    return run
  }

  /**
   * Sends a run of issue updates as one request. Resolves to false if the
   * server refused the batch, in which case the caller sends its first
   * entry alone: one by one, the update that is at fault gets found.
   */
  private async deliverBatch(batch: Pending[]): Promise<boolean> {
    await Promise.all(batch.map((entry) => entry.saved))
    try {
      await this.withOutboxLock(async () => {
        // Leave out what another tab has already sent or seen rejected.
        const mine: Pending[] = []
        for (const entry of batch) {
          const stored = entry.seq === undefined ? undefined : await this.db.getMutation(entry.seq)
          if (entry.seq !== undefined && !stored) this.drop(entry)
          else if (stored?.ackSyncId !== undefined) entry.ackSyncId = stored.ackSyncId
          else mine.push(entry)
        }
        if (mine.length === 0) return
        const syncId = await this.transport.sendIssueUpdates!(mine.map((entry) => entry.mutation as IssueUpdate))
        for (const entry of mine) {
          entry.ackSyncId = syncId
          if (entry.seq !== undefined) await this.db.ackMutation(entry.seq, syncId).catch(() => {})
        }
      })
      return true
    } catch (err) {
      if (err instanceof MutationRejected) return false
      throw err
    }
  }

  /**
   * Sends one mutation unless another tab sharing this database already has.
   * Resolves to the confirming sync ID, or null if the entry is gone (the
   * other tab saw it confirmed or rejected).
   */
  private async deliver(entry: Pending): Promise<number | null> {
    if (entry.seq !== undefined) {
      const stored = await this.db.getMutation(entry.seq)
      if (!stored) return null
      if (stored.ackSyncId !== undefined) return stored.ackSyncId
    }
    const syncId = await this.transport.send(entry.mutation)
    if (entry.seq !== undefined) {
      await this.db.ackMutation(entry.seq, syncId).catch(() => {})
    }
    return syncId
  }

  /** Runs `fn` while no other tab is sending from the same outbox. */
  private withOutboxLock<T>(fn: () => Promise<T>): Promise<T> {
    const locks = globalThis.navigator?.locks
    if (!locks) return fn()
    return locks.request(`cruuze-outbox:${this.db.name}`, fn) as Promise<T>
  }

  /** Removes a mutation that will never be confirmed, undoing it locally. */
  private drop(entry: Pending) {
    this.outbox = this.outbox.filter((e) => e !== entry)
    if (entry.seq !== undefined) void this.db.deleteMutations([entry.seq]).catch(() => {})
    this.refreshPending()
  }

  /** Forgets mutations whose confirming change has now been applied. */
  private settleConfirmed() {
    const confirmed = this.outbox.filter((e) => e.ackSyncId !== undefined && e.ackSyncId <= this.lastSyncId)
    if (confirmed.length === 0) return
    this.outbox = this.outbox.filter((e) => !confirmed.includes(e))
    const seqs = confirmed.flatMap((e) => (e.seq === undefined ? [] : [e.seq]))
    void this.db.deleteMutations(seqs).catch(() => {})
    this.refreshPending()
  }

  private refreshPending() {
    this.pool.setPending(this.outbox.map((e) => e.mutation))
    this.pendingCount.value = this.outbox.length
  }
}
