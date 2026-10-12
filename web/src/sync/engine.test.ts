import 'fake-indexeddb/auto'

import { describe, expect, it, vi } from 'vitest'

import { AccessLost, type EngineHooks, type SocketHandlers, SyncEngine, type Transport } from './engine'
import { LocalDb } from './localdb'
import { type Mutation, MutationRejected } from './mutations'
import type { Comment, Issue, IssueDetails, SyncAction, SyncBootstrap, TeamRecord } from './types'

// Only what these tests read; a real team record has more settings.
const TEAM = { id: 'team-1', name: 'Engineering', key: 'ENG', private: false, createdAt: '2026-01-01T00:00:00Z' } as TeamRecord
const BACKLOG = { id: 'state-backlog', teamId: TEAM.id, name: 'Backlog', type: 'backlog', color: '#999', position: 0 } as const
const DONE = { id: 'state-done', teamId: TEAM.id, name: 'Done', type: 'completed', color: '#55f', position: 3 } as const

const BUG = { id: 'label-bug', name: 'Bug', color: '#eb5757' }

function issue(id: string, overrides: Partial<Issue> = {}): Issue {
  return {
    id,
    teamId: TEAM.id,
    number: 1,
    title: 'Fix login',
    description: '',
    stateId: BACKLOG.id,
    priority: 0,
    sortOrder: 0,
    labelIds: [],
    subscriberIds: [],
    createdAt: '2026-01-01T00:00:00Z',
    updatedAt: '2026-01-01T00:00:00Z',
    ...overrides,
  }
}

function comment(id: string, body: string, issueId = 'issue-1'): Comment {
  return { id, issueId, userId: 'user-1', body, createdAt: `2026-01-01T00:00:0${id.length}Z` }
}

function commentAction(syncId: number, data: Comment, action: SyncAction['action'] = 'I'): SyncAction {
  return { syncId, model: 'Comment', modelId: data.id, action, data }
}

function issueAction(syncId: number, data: Issue, action: SyncAction['action'] = 'U'): SyncAction {
  return { syncId, model: 'Issue', modelId: data.id, action, data }
}

/** A scriptable stand-in for the server. */
class FakeServer implements Transport {
  snapshot: SyncBootstrap = {
    lastSyncId: 10,
    users: [],
    teams: [TEAM],
    teamMembers: [],
    workflowStates: [BACKLOG, DONE],
    labels: [BUG],
    issues: [issue('issue-1')],
    comments: [],
    attachments: [],
    notifications: [],
    projects: [],
    milestones: [],
    cycles: [],
    relations: [],
    templates: [],
    views: [],
    favorites: [],
    pullRequests: [],
    timeEntries: [],
    initiatives: [],
    projectDependencies: [],
    projectUpdates: [],
    customerRequests: [],
  }
  bootstraps = 0
  /** What `issueDetails` answers; the default is an empty discussion as of now. */
  details: (issueId: string) => Promise<IssueDetails> = async () => ({
    syncId: this.snapshot.lastSyncId,
    comments: [],
    reactions: [],
    activities: [],
    inProgressSeconds: 0,
  })
  detailRequests: string[] = []
  /** Issues the server has but the bootstrap leaves out. */
  archived: Issue[] = []
  sockets: { after: number; handlers: SocketHandlers; closed: boolean }[] = []
  sent: Mutation[] = []
  /** Set to let the engine batch issue updates. */
  sendIssueUpdates?: Transport['sendIssueUpdates']
  /** What `send` does; defaults to acknowledging with the next sync ID. */
  onSend: (mutation: Mutation) => Promise<number> = async () => ++this.snapshot.lastSyncId

  async bootstrap() {
    this.bootstraps++
    return structuredClone(this.snapshot)
  }

  async lookupIssue(ref: string) {
    return this.archived.find((i) => i.id === ref) ?? null
  }

  issueDetails(issueId: string) {
    this.detailRequests.push(issueId)
    return this.details(issueId)
  }

  connect(after: number, handlers: SocketHandlers) {
    const socket = { after, handlers, closed: false }
    this.sockets.push(socket)
    queueMicrotask(() => handlers.onOpen())
    return {
      close: () => {
        socket.closed = true
      },
    }
  }

  send(mutation: Mutation) {
    this.sent.push(mutation)
    return this.onSend(mutation)
  }

  get socket() {
    return this.sockets[this.sockets.length - 1]!
  }

  /** Delivers confirmed changes over the open socket. */
  push(...actions: SyncAction[]) {
    const lastSyncId = actions[actions.length - 1]!.syncId
    this.socket.handlers.onMessage({ type: 'actions', lastSyncId, actions })
  }
}

let dbCount = 0

async function setup(server = new FakeServer(), name = `test-${++dbCount}`) {
  const hooks = { onRejected: vi.fn<EngineHooks['onRejected']>() }
  const engine = new SyncEngine(await LocalDb.open(name), server, hooks)
  await engine.start()
  await vi.waitFor(() => expect(engine.status.value).toBe('live'))
  return { engine, server, hooks, name, issues: engine.pool.view.Issue }
}

describe('SyncEngine', () => {
  it('bootstraps once, then starts from the local cache', async () => {
    const first = await setup()
    expect(first.server.bootstraps).toBe(1)
    expect(first.server.socket.after).toBe(10)
    expect(first.issues.get('issue-1')?.title).toBe('Fix login')
    expect(first.engine.pool.view.WorkflowState.size).toBe(2)

    first.server.push(issueAction(11, issue('issue-2', { number: 2, title: 'Second' }), 'I'))
    await vi.waitFor(() => expect(first.issues.size).toBe(2))
    // Let the cache write finish before "reloading".
    await vi.waitFor(async () => expect((await (await LocalDb.open(first.name)).load()).lastSyncId).toBe(11))
    first.engine.stop()

    const second = await setup(first.server, first.name)
    expect(second.server.bootstraps).toBe(1)
    expect(second.server.socket.after).toBe(11)
    expect(second.issues.get('issue-2')?.title).toBe('Second')
  })

  it('applies changes in order and ignores ones it already has', async () => {
    const { server, issues } = await setup()
    server.push(issueAction(11, issue('issue-1', { title: 'Newer' })))
    await vi.waitFor(() => expect(issues.get('issue-1')?.title).toBe('Newer'))

    // A replayed, older change must not overwrite the newer state.
    server.push(issueAction(9, issue('issue-1', { title: 'Stale' })))
    server.push(issueAction(12, issue('issue-1', { title: 'Newest' })))
    await vi.waitFor(() => expect(issues.get('issue-1')?.title).toBe('Newest'))

    // Archived and deleted issues leave the list.
    server.push(issueAction(13, issue('issue-1', { archivedAt: '2026-02-01T00:00:00Z' })))
    await vi.waitFor(() => expect(issues.has('issue-1')).toBe(false))
  })

  it('shows a created issue at once and keeps it pending until confirmed', async () => {
    const { engine, server, issues } = await setup()
    const draft = issue('issue-new', { number: 0, title: 'Optimistic' })

    engine.mutate({ type: 'issue.create', issue: draft })
    expect(issues.get('issue-new')?.number).toBe(0)
    expect(engine.pendingCount.value).toBe(1)

    await vi.waitFor(() => expect(server.sent).toHaveLength(1))
    // Acknowledged by the server (sync ID 11), but the change hasn't arrived yet.
    await new Promise((resolve) => setTimeout(resolve, 10))
    expect(engine.pendingCount.value).toBe(1)

    server.push(issueAction(11, { ...draft, number: 2 }, 'I'))
    await vi.waitFor(() => expect(engine.pendingCount.value).toBe(0))
    expect(issues.get('issue-new')?.number).toBe(2)
  })

  it('keeps pending edits on top of changes that arrive underneath them', async () => {
    const { engine, server, issues } = await setup()
    // Hold the acknowledgement so the edit stays pending.
    let acknowledge!: (syncId: number) => void
    server.onSend = () => new Promise((resolve) => (acknowledge = resolve))

    engine.mutate({ type: 'issue.update', issueId: 'issue-1', patch: { stateId: DONE.id } })
    expect(issues.get('issue-1')?.stateId).toBe(DONE.id)

    // Someone else renames the issue meanwhile.
    server.push(issueAction(11, issue('issue-1', { title: 'Renamed by Bob' })))
    await vi.waitFor(() => expect(issues.get('issue-1')?.title).toBe('Renamed by Bob'))
    expect(issues.get('issue-1')?.stateId).toBe(DONE.id)

    await vi.waitFor(() => expect(server.sent).toHaveLength(1))
    acknowledge(12)
    server.push(issueAction(12, issue('issue-1', { title: 'Renamed by Bob', stateId: DONE.id })))
    await vi.waitFor(() => expect(engine.pendingCount.value).toBe(0))
    expect(issues.get('issue-1')).toMatchObject({ title: 'Renamed by Bob', stateId: DONE.id })
  })

  it('combines label edits and clears optional fields', async () => {
    const { engine, server, issues } = await setup()
    let acknowledge!: (syncId: number) => void
    server.onSend = () => new Promise((resolve) => (acknowledge = resolve))

    engine.mutate({ type: 'label.create', workspaceId: 'ws-1', label: { id: 'label-new', name: 'Urgent', color: '#f2994a' } })
    engine.mutate({ type: 'issue.update', issueId: 'issue-1', patch: { addLabelIds: ['label-new'], dueDate: '2026-12-24' } })
    expect(engine.pool.view.Label.get('label-new')?.name).toBe('Urgent')
    expect(issues.get('issue-1')).toMatchObject({ labelIds: ['label-new'], dueDate: '2026-12-24' })

    // Someone else adds a different label underneath: both survive.
    server.push(issueAction(11, issue('issue-1', { labelIds: [BUG.id] })))
    await vi.waitFor(() => expect(issues.get('issue-1')?.labelIds).toEqual([BUG.id, 'label-new']))

    engine.mutate({ type: 'issue.update', issueId: 'issue-1', patch: { removeLabelIds: [BUG.id], dueDate: null } })
    const shown = issues.get('issue-1')!
    expect(shown.labelIds).toEqual(['label-new'])
    expect('dueDate' in shown).toBe(false)

    // Deleting the pending label hides it again.
    engine.mutate({ type: 'label.delete', labelId: 'label-new' })
    expect(engine.pool.view.Label.has('label-new')).toBe(false)
    acknowledge(12)
  })

  it('applies comments, reactions and inbox changes optimistically', async () => {
    const { engine, server } = await setup()
    const { Comment, Reaction, Notification, Issue } = engine.pool.view
    server.push({
      syncId: 11,
      model: 'Notification',
      modelId: 'n-1',
      action: 'I',
      data: { id: 'n-1', type: 'mentioned', issueId: 'issue-1', createdAt: '2026-01-02T00:00:00Z' },
    })
    await vi.waitFor(() => expect(Notification.size).toBe(1))
    // Never acknowledge, so everything below stays pending.
    server.onSend = () => new Promise(() => {})

    const comment = { id: 'c-1', issueId: 'issue-1', userId: 'me', body: 'Looks good', createdAt: '2026-01-02T00:00:00Z' }
    engine.mutate({ type: 'comment.create', comment })
    engine.mutate({ type: 'comment.update', commentId: 'c-1', body: 'Looks great' })
    engine.mutate({ type: 'reaction.set', reaction: { commentId: 'c-1', userId: 'me', emoji: '🎉' }, on: true })
    engine.mutate({ type: 'issue.subscribe', issueId: 'issue-1', userId: 'me', subscribed: true })
    engine.mutate({ type: 'notification.read', notificationId: 'n-1', read: true })

    expect(Comment.get('c-1')).toMatchObject({ body: 'Looks great' })
    expect(Comment.get('c-1')?.editedAt).toBeDefined()
    expect(Reaction.has('c-1:me:🎉')).toBe(true)
    expect(Issue.get('issue-1')?.subscriberIds).toEqual(['me'])
    expect(Notification.get('n-1')?.readAt).toBeDefined()

    // Undoing each one locally works the same way.
    engine.mutate({ type: 'reaction.set', reaction: { commentId: 'c-1', userId: 'me', emoji: '🎉' }, on: false })
    engine.mutate({ type: 'issue.subscribe', issueId: 'issue-1', userId: 'me', subscribed: false })
    engine.mutate({ type: 'notification.read', notificationId: 'n-1', read: false })
    engine.mutate({ type: 'comment.delete', commentId: 'c-1' })
    expect(Reaction.size).toBe(0)
    expect(Issue.get('issue-1')?.subscriberIds).toEqual([])
    expect(Notification.get('n-1')?.readAt).toBeUndefined()
    expect(Comment.has('c-1')).toBe(false)
  })

  it('creates, patches and deletes plain records optimistically', async () => {
    const { engine, server, issues } = await setup()
    server.onSend = () => new Promise(() => {})
    const { Project, Milestone } = engine.pool.view

    const project = { id: 'p-1', name: 'Launch', description: '', status: 'backlog' as const, color: '#5e6ad2', createdAt: '2026-01-02T00:00:00Z' }
    engine.mutate({ type: 'record.create', model: 'Project', workspaceId: 'ws-1', record: project })
    engine.mutate({ type: 'record.create', model: 'Milestone', workspaceId: 'ws-1', record: { id: 'm-1', projectId: 'p-1', name: 'Beta', sortOrder: 1 } })
    engine.mutate({ type: 'record.update', model: 'Project', id: 'p-1', patch: { status: 'started', targetDate: '2026-12-01' } })
    expect(Project.get('p-1')).toMatchObject({ status: 'started', targetDate: '2026-12-01' })

    // null clears a field.
    engine.mutate({ type: 'record.update', model: 'Project', id: 'p-1', patch: { targetDate: null } })
    expect('targetDate' in Project.get('p-1')!).toBe(false)

    // An issue's milestone goes when it moves to another project, as on the server.
    engine.mutate({ type: 'issue.update', issueId: 'issue-1', patch: { projectId: 'p-1', milestoneId: 'm-1' } })
    expect(issues.get('issue-1')).toMatchObject({ projectId: 'p-1', milestoneId: 'm-1' })
    engine.mutate({ type: 'issue.update', issueId: 'issue-1', patch: { projectId: 'p-2' } })
    expect(issues.get('issue-1')?.projectId).toBe('p-2')
    expect(issues.get('issue-1')?.milestoneId).toBeUndefined()

    engine.mutate({ type: 'record.delete', model: 'Milestone', id: 'm-1' })
    expect(Milestone.size).toBe(0)
  })

  it('rolls back a change the server refuses', async () => {
    const { engine, server, hooks, issues } = await setup()
    server.onSend = async () => {
      throw new MutationRejected('Issue title must be 1-500 characters.')
    }

    engine.mutate({ type: 'issue.update', issueId: 'issue-1', patch: { title: 'x'.repeat(501) } })
    expect(issues.get('issue-1')?.title).toHaveLength(501)

    await vi.waitFor(() => expect(hooks.onRejected).toHaveBeenCalledOnce())
    expect(issues.get('issue-1')?.title).toBe('Fix login')
    expect(engine.pendingCount.value).toBe(0)
  })

  it('keeps unsent changes across a reload and sends them in order', async () => {
    const { engine, server, name, issues } = await setup()
    server.onSend = async () => {
      throw new TypeError('Failed to fetch')
    }

    engine.mutate({ type: 'issue.update', issueId: 'issue-1', patch: { title: 'Edited offline' } })
    engine.mutate({ type: 'issue.archive', issueId: 'issue-1' })
    expect(issues.has('issue-1')).toBe(false)
    await vi.waitFor(async () => expect((await (await LocalDb.open(name)).load()).outbox).toHaveLength(2))
    await vi.waitFor(() => expect(server.sent.length).toBeGreaterThan(0))
    engine.stop()

    // Back online after a reload.
    const online = new FakeServer()
    const reloaded = await setup(online, name)
    expect(online.bootstraps).toBe(0)
    await vi.waitFor(() => expect(online.sent.map((m) => m.type)).toEqual(['issue.update', 'issue.archive']))
    // Still hidden while pending, then confirmed by the server's changes.
    expect(reloaded.issues.has('issue-1')).toBe(false)
    online.push(
      issueAction(11, issue('issue-1', { title: 'Edited offline' })),
      issueAction(12, issue('issue-1', { title: 'Edited offline', archivedAt: '2026-02-01T00:00:00Z' })),
    )
    await vi.waitFor(() => expect(reloaded.engine.pendingCount.value).toBe(0))
    expect(reloaded.issues.has('issue-1')).toBe(false)
    await vi.waitFor(async () => expect((await (await LocalDb.open(name)).load()).outbox).toHaveLength(0))
  })

  it('bootstraps again when the server says its copy is beyond repair', async () => {
    const { server, issues } = await setup()
    server.snapshot = { ...server.snapshot, lastSyncId: 50, issues: [issue('issue-9', { number: 9, title: 'Fresh' })] }

    const before = server.socket
    before.handlers.onMessage({ type: 'reset' })

    await vi.waitFor(() => expect(server.bootstraps).toBe(2))
    await vi.waitFor(() => expect(server.sockets).toHaveLength(2))
    expect(before.closed).toBe(true)
    expect(server.socket.after).toBe(50)
    expect([...issues.keys()]).toEqual(['issue-9'])
  })

  it("loads an issue's discussion on demand, once, and keeps it across a reload", async () => {
    const server = new FakeServer()
    server.details = async () => ({ syncId: 10, comments: [comment('c1', 'First'), comment('c22', 'Second')], reactions: [], activities: [], inProgressSeconds: 0 })
    const first = await setup(server)
    const comments = first.engine.pool.view.Comment
    expect(comments.size).toBe(0)
    expect(first.engine.detailed.has('issue-1')).toBe(false)

    // Two views asking at once share one request.
    await Promise.all([first.engine.loadDetails('issue-1'), first.engine.loadDetails('issue-1')])
    expect(server.detailRequests).toEqual(['issue-1'])
    expect([...comments.values()].map((c) => c.body)).toEqual(['First', 'Second'])
    expect(first.engine.detailed.has('issue-1')).toBe(true)
    await first.engine.loadDetails('issue-1')
    expect(server.detailRequests).toHaveLength(1)

    // From then on the socket keeps it current.
    server.push(commentAction(11, comment('c333', 'Third')))
    await vi.waitFor(() => expect(comments.size).toBe(3))
    await vi.waitFor(async () => expect((await (await LocalDb.open(first.name)).load()).lastSyncId).toBe(11))
    first.engine.stop()

    const second = await setup(server, first.name)
    expect(second.engine.detailed.has('issue-1')).toBe(true)
    expect(second.engine.pool.view.Comment.size).toBe(3)
    await second.engine.loadDetails('issue-1')
    expect(server.detailRequests).toHaveLength(1)
  })

  it('does not let details that are older than the pool undo newer changes', async () => {
    const server = new FakeServer()
    const { engine } = await setup(server)
    const comments = engine.pool.view.Comment

    // The details were read at sync 10, but arrive after the socket has
    // already delivered an edit (11), a deletion (12) and a new comment (13).
    let respond!: (details: IssueDetails) => void
    server.details = () => new Promise((resolve) => (respond = resolve))
    const loading = engine.loadDetails('issue-1')
    server.push(
      commentAction(11, comment('c1', 'Edited'), 'U'),
      commentAction(12, comment('c22', 'Doomed'), 'D'),
      commentAction(13, comment('c333', 'New')),
    )
    await vi.waitFor(() => expect(comments.size).toBe(2))
    respond({ syncId: 10, comments: [comment('c1', 'Original'), comment('c22', 'Doomed'), comment('c4444', 'Old')], reactions: [], activities: [], inProgressSeconds: 0 })
    await loading

    expect([...comments.values()].map((c) => c.body).sort()).toEqual(['Edited', 'New', 'Old'])
    // The cache agrees, so a reload shows the same.
    await vi.waitFor(async () => {
      const cached = (await (await LocalDb.open(engine['db'].name)).load()).records.filter((r) => r.model === 'Comment')
      expect(cached.map((r) => (r.value as Comment).body).sort()).toEqual(['Edited', 'New', 'Old'])
    })
  })

  it('fetches details again when a bootstrap replaced the pool meanwhile', async () => {
    const server = new FakeServer()
    const { engine } = await setup(server)

    let respond!: (details: IssueDetails) => void
    server.details = () => new Promise((resolve) => (respond = resolve))
    const loading = engine.loadDetails('issue-1')
    await vi.waitFor(() => expect(server.detailRequests).toHaveLength(1))

    server.snapshot = { ...server.snapshot, lastSyncId: 50 }
    server.socket.handlers.onMessage({ type: 'reset' })
    await vi.waitFor(() => expect(server.bootstraps).toBe(2))
    const stale = respond
    server.details = async () => ({ syncId: 50, comments: [comment('c1', 'Fresh')], reactions: [], activities: [], inProgressSeconds: 0 })
    stale({ syncId: 10, comments: [comment('c1', 'Stale')], reactions: [], activities: [], inProgressSeconds: 0 })
    await loading

    expect(server.detailRequests).toHaveLength(2)
    expect(engine.pool.view.Comment.get('c1')?.body).toBe('Fresh')
  })

  it('forgets which discussions it had when it bootstraps again', async () => {
    const { engine, server } = await setup()
    await engine.loadDetails('issue-1')
    expect(engine.detailed.size).toBe(1)

    server.socket.handlers.onMessage({ type: 'reset' })
    await vi.waitFor(() => expect(server.bootstraps).toBe(2))
    expect(engine.detailed.size).toBe(0)
    expect((await (await LocalDb.open(engine['db'].name)).load()).detailed).toEqual([])
  })

  it('fetches an archived issue on demand without listing it as active', async () => {
    const server = new FakeServer()
    server.archived = [issue('issue-old', { title: 'Long gone', archivedAt: '2026-01-02T00:00:00Z' })]
    const { engine, issues } = await setup(server)

    expect(await engine.fetchIssue('nope')).toBeUndefined()
    expect((await engine.fetchIssue('issue-old'))?.title).toBe('Long gone')
    expect(issues.get('issue-old')?.archivedAt).toBeDefined()

    // Unarchived by someone: the update makes it an ordinary issue again.
    server.push(issueAction(11, issue('issue-old', { title: 'Back' })))
    await vi.waitFor(() => expect(issues.get('issue-old')?.archivedAt).toBeUndefined())
  })

  it('stops and says so when the user was removed from the workspace', async () => {
    const server = new FakeServer()
    const onAccessLost = vi.fn()
    const engine = new SyncEngine(await LocalDb.open(`test-${++dbCount}`), server, { onRejected: vi.fn(), onAccessLost })
    await engine.start()
    await vi.waitFor(() => expect(engine.status.value).toBe('live'))

    server.bootstrap = async () => {
      throw new AccessLost()
    }
    server.socket.handlers.onMessage({ type: 'reset' })
    await vi.waitFor(() => expect(onAccessLost).toHaveBeenCalledOnce())
    expect(server.sockets).toHaveLength(1)
  })

  it('sends a run of issue updates as one request, and finds the one at fault if refused', async () => {
    const server = new FakeServer()
    server.snapshot.issues = [issue('issue-1'), issue('issue-2'), issue('issue-3')]
    const batches: string[][] = []
    server.sendIssueUpdates = async (mutations) => {
      batches.push(mutations.map((m) => m.issueId))
      if (mutations.some((m) => m.patch.title === 'bad')) throw new MutationRejected('no')
      return ++server.snapshot.lastSyncId
    }
    server.onSend = async (mutation) => {
      if (mutation.type === 'issue.update' && mutation.patch.title === 'bad') throw new MutationRejected('That title is not allowed')
      return ++server.snapshot.lastSyncId
    }
    const { engine, hooks, issues } = await setup(server)

    // A bulk edit: three updates queued in the same tick go out together.
    for (const id of ['issue-1', 'issue-2', 'issue-3']) engine.mutate({ type: 'issue.update', issueId: id, patch: { priority: 2 } })
    await vi.waitFor(() => expect(batches).toEqual([['issue-1', 'issue-2', 'issue-3']]))
    expect(server.sent).toHaveLength(0)
    server.push(...['issue-1', 'issue-2', 'issue-3'].map((id, i) => issueAction(11 + i, issue(id, { priority: 2 }))))
    await vi.waitFor(() => expect(engine.pendingCount.value).toBe(0))

    // A refused batch falls back to one at a time: the bad update is rolled
    // back and reported, the good ones still go through.
    server.snapshot.lastSyncId = 20 // past what the socket just delivered
    engine.mutate({ type: 'issue.update', issueId: 'issue-1', patch: { title: 'fine' } })
    engine.mutate({ type: 'issue.update', issueId: 'issue-2', patch: { title: 'bad' } })
    engine.mutate({ type: 'issue.update', issueId: 'issue-3', patch: { title: 'also fine' } })
    await vi.waitFor(() => expect(hooks.onRejected).toHaveBeenCalledOnce())
    await vi.waitFor(() => expect(issues.get('issue-3')?.title).toBe('also fine'))
    expect(issues.get('issue-1')?.title).toBe('fine')
    expect(issues.get('issue-2')?.title).toBe('Fix login')
    expect(server.sent.map((m) => (m.type === 'issue.update' ? m.issueId : ''))).toContain('issue-2')
  })

  it('reports offline and reconnects from where it left off', async () => {
    vi.useFakeTimers({ toFake: ['setTimeout', 'clearTimeout'] })
    try {
      const server = new FakeServer()
      const engine = new SyncEngine(await LocalDb.open(`test-${++dbCount}`), server, { onRejected: vi.fn() })
      await engine.start()
      await vi.waitFor(() => expect(engine.status.value).toBe('live'))

      server.push(issueAction(11, issue('issue-1', { title: 'Seen' })))
      await vi.waitFor(() => expect(engine.pool.view.Issue.get('issue-1')?.title).toBe('Seen'))

      server.socket.handlers.onClose()
      expect(engine.status.value).toBe('offline')

      await vi.advanceTimersByTimeAsync(1000)
      expect(server.sockets).toHaveLength(2)
      expect(server.socket.after).toBe(11)
      await vi.waitFor(() => expect(engine.status.value).toBe('live'))
      engine.stop()
    } finally {
      vi.useRealTimers()
    }
  })
})
