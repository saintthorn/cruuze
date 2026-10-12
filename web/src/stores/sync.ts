import { defineStore } from 'pinia'
import { computed, shallowRef, watch } from 'vue'

import { api, unwrap } from '@/api/client'

import { SyncEngine, type SyncStatus } from '@/sync/engine'
import { LocalDb } from '@/sync/localdb'
import { findHygieneIssues } from '@/lib/hygiene'
import { LABEL_COLORS, type Progress, progressOf, todayUTC } from '@/lib/issues'
import type { CrudModel, IssuePatch, LabelPatch, Mutation } from '@/sync/mutations'
import { ObjectPool } from '@/sync/pool'
import { httpTransport } from '@/sync/transport'
import type {
  Activity,
  Attachment,
  Comment,
  Cycle,
  Favorite,
  PullRequest,
  TimeEntry,
  Initiative,
  ProjectDependency,
  ProjectUpdate,
  CustomerRequest,
  Issue,
  Label,
  Milestone,
  Models,
  Notification,
  Project,
  Reaction,
  Relation,
  TeamRecord,
  Template,
  UserRecord,
  View,
  WorkflowState,
} from '@/sync/types'

import { useToastStore } from './toasts'

const EMPTY_POOL = new ObjectPool()

/** What people call a model: "time entry" for TimeEntry, and so on. */
function noun(model: CrudModel): string {
  if (model === 'WorkflowState') return 'status'
  return model.replace(/([a-z])([A-Z])/g, '$1 $2').toLowerCase()
}

function describe(mutation: Mutation): string {
  switch (mutation.type) {
    case 'issue.create':
      return `Couldn't create "${mutation.issue.title}"`
    case 'issue.update':
      return "Couldn't save your change"
    case 'issue.archive':
      return "Couldn't archive the issue"
    case 'label.create':
      return `Couldn't create the label "${mutation.label.name}"`
    case 'label.update':
      return "Couldn't update the label"
    case 'label.delete':
      return "Couldn't delete the label"
    case 'issue.subscribe':
      return "Couldn't change your subscription"
    case 'comment.create':
      return "Couldn't post your comment"
    case 'comment.update':
      return "Couldn't save your comment"
    case 'comment.delete':
      return "Couldn't delete the comment"
    case 'reaction.set':
      return "Couldn't save your reaction"
    case 'notification.read':
    case 'notification.delete':
      return "Couldn't update your inbox"
    case 'record.create':
      return `Couldn't create the ${noun(mutation.model)}`
    case 'record.update':
      return `Couldn't save the ${noun(mutation.model)}`
    case 'record.delete':
      return `Couldn't delete the ${noun(mutation.model)}`
  }
}

/** What a new issue can be created with; everything but the title is optional. */
export interface NewIssue {
  teamId: string
  title: string
  description?: string
  stateId?: string
  priority?: number
  assigneeId?: string
  labelIds?: string[]
  dueDate?: string
  projectId?: string
  cycleId?: string
  parentId?: string
}

/**
 * The synced copy of the open workspace. Everything here reads from memory
 * and updates live; writes show immediately and reach the server in the
 * background.
 */
export const useSyncStore = defineStore('sync', () => {
  const toasts = useToastStore()

  const engine = shallowRef<SyncEngine | null>(null)
  let db: LocalDb | null = null
  let userId = ''
  let workspaceId = ''
  /** Called when the sync socket can't connect; set by the app shell. */
  let onConnectFailed: (() => void) | undefined
  /** Called when the user turns out to have been removed from the workspace. */
  let onAccessLost: (() => void) | undefined

  const pool = computed(() => engine.value?.pool ?? EMPTY_POOL)
  const status = computed<SyncStatus>(() => engine.value?.status.value ?? 'loading')
  const pendingCount = computed(() => engine.value?.pendingCount.value ?? 0)
  /** The sync ID the local copy is current as of. */
  const syncId = computed(() => engine.value?.cursor.value ?? 0)
  /** The last few changes that arrived from the server, newest last. */
  const feed = computed(() => engine.value?.feed.value ?? [])
  /** How many records the local copy holds, across every model. */
  const recordCount = computed(() => Object.values(pool.value.view).reduce((total, table) => total + table.size, 0))

  /** Opens the workspace's local copy, resolving once there is data to show. */
  async function open(openWorkspaceId: string, currentUserId: string) {
    await close()
    userId = currentUserId
    workspaceId = openWorkspaceId
    // One database per user and workspace, so accounts never share a cache.
    db = await LocalDb.open(`cruuze:${currentUserId}:${workspaceId}`)
    const next = new SyncEngine(db, httpTransport(workspaceId), {
      onRejected: (mutation, message) => toasts.show(`${describe(mutation)}: ${message}`),
      onConnectFailed: () => onConnectFailed?.(),
      onAccessLost: () => onAccessLost?.(),
    })
    try {
      await next.start()
    } catch (err) {
      next.stop()
      db.close()
      db = null
      throw err
    }
    engine.value = next
  }

  /** Stops syncing. With `forget`, also deletes the local copy from this browser. */
  async function close(forget = false) {
    engine.value?.stop()
    engine.value = null
    const closing = db
    db = null
    if (forget) await closing?.destroy().catch(() => {})
    else closing?.close()
  }

  function setConnectFailedHandler(handler: () => void) {
    onConnectFailed = handler
  }

  function setAccessLostHandler(handler: () => void) {
    onAccessLost = handler
  }

  /**
   * Fetches an issue's comments and activity if they aren't here yet. They
   * are left out of the initial load and arrive when an issue is opened.
   */
  function loadIssueDetails(issueId: string): Promise<void> {
    return engine.value?.loadDetails(issueId) ?? Promise.resolve()
  }

  /** Whether an issue's whole discussion is loaded, rather than just parts of it. */
  function hasIssueDetails(issueId: string): boolean {
    return engine.value?.detailed.has(issueId) ?? false
  }

  // --- selectors -------------------------------------------------------

  const users = computed<UserRecord[]>(() =>
    [...pool.value.view.User.values()].sort((a, b) => (a.name || a.email).localeCompare(b.name || b.email)),
  )

  const teams = computed<TeamRecord[]>(() =>
    [...pool.value.view.Team.values()].sort((a, b) => a.name.localeCompare(b.name)),
  )

  const myTeamIds = computed(() => {
    const ids = new Set<string>()
    for (const member of pool.value.view.TeamMember.values()) {
      if (member.userId === userId) ids.add(member.teamId)
    }
    return ids
  })

  /** Open issues that need a look (stale, stuck, blocked, unowned, overdue), by team. */
  const hygiene = computed(() =>
    findHygieneIssues({
      issues: issues.value,
      relations: pool.value.view.Relation.values(),
      stateOf: (id) => stateIndex.value.get(id),
      staleDays: (teamId) => pool.value.view.Team.get(teamId)?.staleDays ?? 0,
    }),
  )

  /** The members of a team, in the same order as `users`. */
  function teamMembers(teamId: string): UserRecord[] {
    const ids = new Set<string>()
    for (const member of pool.value.view.TeamMember.values()) {
      if (member.teamId === teamId) ids.add(member.userId)
    }
    return users.value.filter((u) => ids.has(u.id))
  }

  const labels = computed<Label[]>(() =>
    [...pool.value.view.Label.values()].sort((a, b) => a.name.localeCompare(b.name)),
  )

  /**
   * Every active issue the user can see, in manual order, including ones in
   * triage. Archived issues fetched for viewing are in the pool but not here.
   */
  const allIssues = computed<Issue[]>(() =>
    [...pool.value.view.Issue.values()].filter((i) => !i.archivedAt).sort((a, b) => a.sortOrder - b.sortOrder),
  )

  // Plain copies of the small tables that are looked up once per issue in
  // every pass over the issues. Reading the reactive tables directly costs a
  // dependency check per lookup, which adds up over tens of thousands.
  const stateIndex = computed(() => new Map(pool.value.view.WorkflowState))
  const triageStateIds = computed(() => {
    const ids = new Set<string>()
    for (const s of stateIndex.value.values()) if (s.type === 'triage') ids.add(s.id)
    return ids
  })

  const closedStateIds = computed(() => {
    const ids = new Set<string>()
    for (const s of stateIndex.value.values()) if (s.type === 'completed' || s.type === 'canceled') ids.add(s.id)
    return ids
  })
  /** How many of `issues` are completed or canceled. */
  const closedCount = computed(() => {
    const closed = closedStateIds.value
    let count = 0
    for (const issue of issues.value) if (closed.has(issue.stateId)) count++
    return count
  })

  function isTriage(issue: Issue): boolean {
    return triageStateIds.value.has(issue.stateId)
  }

  /** Issues the team has accepted: everything but those waiting in triage. */
  const issues = computed<Issue[]>(() => {
    const triage = triageStateIds.value
    return triage.size === 0 ? allIssues.value : allIssues.value.filter((i) => !triage.has(i.stateId))
  })

  /** A team's issues waiting in triage, oldest first. */
  const triageByTeam = computed(() => {
    const byTeam = new Map<string, Issue[]>()
    const triage = triageStateIds.value
    if (triage.size === 0) return byTeam
    for (const issue of allIssues.value) {
      if (!triage.has(issue.stateId)) continue
      const list = byTeam.get(issue.teamId)
      if (list) list.push(issue)
      else byTeam.set(issue.teamId, [issue])
    }
    for (const list of byTeam.values()) list.sort((a, b) => a.createdAt.localeCompare(b.createdAt))
    return byTeam
  })

  function triageIssues(teamId: string): Issue[] {
    return triageByTeam.value.get(teamId) ?? []
  }

  function user(id: string | undefined): UserRecord | undefined {
    return id ? pool.value.view.User.get(id) : undefined
  }

  function team(id: string): TeamRecord | undefined {
    return pool.value.view.Team.get(id)
  }

  function teamByKey(key: string): TeamRecord | undefined {
    return teams.value.find((t) => t.key === key)
  }

  function state(id: string): WorkflowState | undefined {
    return stateIndex.value.get(id)
  }

  /** The labels with the given IDs, skipping any that have been deleted. */
  function labelsById(ids: readonly string[]): Label[] {
    return ids.flatMap((id) => pool.value.view.Label.get(id) ?? [])
  }

  /**
   * A team's workflow states in display order. Triage is left out unless
   * asked for: it is where issues wait, not somewhere to move them.
   */
  function statesForTeam(teamId: string, includeTriage = false): WorkflowState[] {
    return [...pool.value.view.WorkflowState.values()]
      .filter((s) => s.teamId === teamId && (includeTriage || s.type !== 'triage'))
      .sort((a, b) => a.position - b.position)
  }

  // --- planning --------------------------------------------------------

  const projects = computed<Project[]>(() =>
    [...pool.value.view.Project.values()].sort((a, b) => a.name.localeCompare(b.name)),
  )
  const project = (id: string | undefined): Project | undefined => (id ? pool.value.view.Project.get(id) : undefined)

  const initiatives = computed<Initiative[]>(() =>
    [...pool.value.view.Initiative.values()].sort((a, b) => a.name.localeCompare(b.name)),
  )
  const initiative = (id: string | undefined): Initiative | undefined => (id ? pool.value.view.Initiative.get(id) : undefined)

  // Dependencies and updates of deleted projects linger locally; leave them out.
  const projectDependencies = computed<ProjectDependency[]>(() =>
    [...pool.value.view.ProjectDependency.values()].filter(
      (d) => pool.value.view.Project.has(d.projectId) && pool.value.view.Project.has(d.dependsOnId),
    ),
  )
  const projectUpdatesByProject = computed(() => {
    const groups = new Map<string, ProjectUpdate[]>()
    for (const update of pool.value.view.ProjectUpdate.values()) {
      const group = groups.get(update.projectId)
      if (group) group.push(update)
      else groups.set(update.projectId, [update])
    }
    // Newest first: the first one is the project's health.
    for (const group of groups.values()) group.sort((a, b) => b.createdAt.localeCompare(a.createdAt))
    return groups
  })
  const projectUpdates = (projectId: string): ProjectUpdate[] => projectUpdatesByProject.value.get(projectId) ?? []
  const projectHealth = (projectId: string): ProjectUpdate['health'] | undefined => projectUpdates(projectId)[0]?.health

  /** Progress of every project, counted in one pass over the issues. */
  const projectProgress = computed(() => {
    const groups = new Map<string, Issue[]>()
    for (const issue of issues.value) {
      if (!issue.projectId) continue
      const group = groups.get(issue.projectId)
      if (group) group.push(issue)
      else groups.set(issue.projectId, [issue])
    }
    const out = new Map<string, Progress>()
    for (const [id, group] of groups) out.set(id, progressOf(group, state))
    return out
  })
  const NO_PROGRESS: Progress = progressOf([], state)
  const progressOfProject = (projectId: string): Progress => projectProgress.value.get(projectId) ?? NO_PROGRESS

  function createInitiative(name: string): Initiative {
    const color = LABEL_COLORS[initiatives.value.length % LABEL_COLORS.length]!
    return createRecord('Initiative', { id: crypto.randomUUID(), name: name.trim(), description: '', color, createdAt: new Date().toISOString() })
  }

  function addProjectDependency(projectId: string, dependsOnId: string) {
    createRecord('ProjectDependency', { id: crypto.randomUUID(), projectId, dependsOnId, createdAt: new Date().toISOString() })
  }

  function postProjectUpdate(projectId: string, health: ProjectUpdate['health'], body: string) {
    createRecord('ProjectUpdate', { id: crypto.randomUUID(), projectId, userId, health, body: body.trim(), createdAt: new Date().toISOString() })
  }

  // --- customers' requests -----------------------------------------------

  const customerRequests = computed<CustomerRequest[]>(() =>
    [...pool.value.view.CustomerRequest.values()].sort((a, b) => b.createdAt.localeCompare(a.createdAt)),
  )
  const customerRequest = (id: string | undefined): CustomerRequest | undefined =>
    id ? pool.value.view.CustomerRequest.get(id) : undefined
  const newRequestCount = computed(() => customerRequests.value.filter((r) => r.status === 'new').length)
  const requestsByIssue = computed(() => {
    const groups = new Map<string, CustomerRequest[]>()
    for (const request of customerRequests.value) {
      if (!request.issueId) continue
      const group = groups.get(request.issueId)
      if (group) group.push(request)
      else groups.set(request.issueId, [request])
    }
    return groups
  })
  const requestsForIssue = (issueId: string): CustomerRequest[] => requestsByIssue.value.get(issueId) ?? []

  /** Notes down a request on a customer's behalf. */
  function createCustomerRequest(title: string, requesterEmail: string, body = '', issueId?: string): CustomerRequest {
    const now = new Date().toISOString()
    return createRecord('CustomerRequest', {
      id: crypto.randomUUID(),
      requesterEmail: requesterEmail.trim().toLowerCase(),
      title: title.trim(),
      body: body.trim(),
      status: 'new',
      public: false,
      response: '',
      issueId,
      voteCount: 0,
      createdAt: now,
      updatedAt: now,
    })
  }

  function milestonesForProject(projectId: string): Milestone[] {
    return [...pool.value.view.Milestone.values()]
      .filter((m) => m.projectId === projectId)
      .sort((a, b) => a.sortOrder - b.sortOrder)
  }
  const milestone = (id: string | undefined): Milestone | undefined => (id ? pool.value.view.Milestone.get(id) : undefined)

  function cyclesForTeam(teamId: string): Cycle[] {
    return [...pool.value.view.Cycle.values()].filter((c) => c.teamId === teamId).sort((a, b) => a.number - b.number)
  }
  const cycle = (id: string | undefined): Cycle | undefined => (id ? pool.value.view.Cycle.get(id) : undefined)

  /** The team's cycle that contains today, if it has one. */
  function currentCycle(teamId: string): Cycle | undefined {
    const today = todayUTC()
    return cyclesForTeam(teamId).find((c) => c.startsAt <= today && today <= c.endsAt)
  }

  function cycleName(cycle: Cycle): string {
    return cycle.name || `Cycle ${cycle.number}`
  }

  const subIssues = (issueId: string): Issue[] => allIssues.value.filter((i) => i.parentId === issueId)

  /** Relations an issue takes part in, from either side. */
  function relationsForIssue(issueId: string): Relation[] {
    return [...pool.value.view.Relation.values()].filter((r) => r.issueId === issueId || r.relatedIssueId === issueId)
  }

  function templatesForTeam(teamId: string): Template[] {
    return [...pool.value.view.Template.values()].filter((t) => t.teamId === teamId).sort((a, b) => a.name.localeCompare(b.name))
  }

  const views = computed<View[]>(() => [...pool.value.view.View.values()].sort((a, b) => a.name.localeCompare(b.name)))
  const view = (id: string): View | undefined => pool.value.view.View.get(id)

  const favorites = computed<Favorite[]>(() =>
    [...pool.value.view.Favorite.values()].sort((a, b) => a.sortOrder - b.sortOrder),
  )
  function favoriteOf(kind: Favorite['kind'], targetId: string): Favorite | undefined {
    return favorites.value.find((f) => f.kind === kind && f.targetId === targetId)
  }

  /** A team's issues in manual order. */
  function issuesForTeam(teamId: string): Issue[] {
    return issues.value.filter((i) => i.teamId === teamId)
  }
  const issue = (id: string | undefined): Issue | undefined => (id ? pool.value.view.Issue.get(id) : undefined)

  // Per-issue lookups for the detail page, built once per change rather
  // than by scanning every record for every issue.
  function groupByIssue<T extends { issueId: string; createdAt: string }>(records: Iterable<T>): Map<string, T[]> {
    const groups = new Map<string, T[]>()
    for (const record of records) {
      const group = groups.get(record.issueId)
      if (group) group.push(record)
      else groups.set(record.issueId, [record])
    }
    for (const group of groups.values()) group.sort((a, b) => a.createdAt.localeCompare(b.createdAt))
    return groups
  }
  const commentsByIssue = computed(() => groupByIssue(pool.value.view.Comment.values()))
  const activitiesByIssue = computed(() => groupByIssue(pool.value.view.Activity.values()))
  const attachmentsByIssue = computed(() => groupByIssue(pool.value.view.Attachment.values()))
  const pullRequestsByIssue = computed(() => groupByIssue(pool.value.view.PullRequest.values()))

  const reactionsByComment = computed(() => {
    const groups = new Map<string, Reaction[]>()
    for (const reaction of pool.value.view.Reaction.values()) {
      const group = groups.get(reaction.commentId)
      if (group) group.push(reaction)
      else groups.set(reaction.commentId, [reaction])
    }
    return groups
  })

  const commentsForIssue = (issueId: string): Comment[] => commentsByIssue.value.get(issueId) ?? []
  const activitiesForIssue = (issueId: string): Activity[] => activitiesByIssue.value.get(issueId) ?? []
  const attachmentsForIssue = (issueId: string): Attachment[] => attachmentsByIssue.value.get(issueId) ?? []
  const pullRequestsForIssue = (issueId: string): PullRequest[] => pullRequestsByIssue.value.get(issueId) ?? []
  const reactionsForComment = (commentId: string): Reaction[] => reactionsByComment.value.get(commentId) ?? []

  // --- time ------------------------------------------------------------
  // Only your own entries are here; what everyone sees is the issue's total.

  const timeEntries = computed<TimeEntry[]>(() =>
    [...pool.value.view.TimeEntry.values()].sort((a, b) => b.startedAt.localeCompare(a.startedAt)),
  )
  const runningTimer = computed(() => timeEntries.value.find((entry) => !entry.endedAt))
  const timeEntriesForIssue = (issueId: string): TimeEntry[] => timeEntries.value.filter((entry) => entry.issueId === issueId)

  /** Stops the running timer, if there is one. */
  function stopTimer() {
    const running = runningTimer.value
    if (!running) return
    const ended = new Date()
    const seconds = Math.round((ended.getTime() - Date.parse(running.startedAt)) / 1000)
    // Too short to be worth an entry: an accidental start.
    if (seconds < 1) deleteRecord('TimeEntry', running.id)
    else updateRecord('TimeEntry', running.id, { endedAt: ended.toISOString(), seconds })
  }

  /** Starts timing an issue. There is one timer: this stops any other. */
  function startTimer(issueId: string) {
    if (runningTimer.value?.issueId === issueId) return
    stopTimer()
    createRecord('TimeEntry', { id: crypto.randomUUID(), issueId, userId, startedAt: new Date().toISOString(), seconds: 0, note: '' })
  }

  /** Records time already spent, as a stretch ending now (or on the given day). */
  function logTime(issueId: string, seconds: number, note = '', day?: string) {
    // A past day's time is put at the end of that day's working hours.
    const ended = day && day !== new Date().toLocaleDateString('en-CA') ? new Date(`${day}T18:00:00`) : new Date()
    createRecord('TimeEntry', {
      id: crypto.randomUUID(),
      issueId,
      userId,
      startedAt: new Date(ended.getTime() - seconds * 1000).toISOString(),
      endedAt: ended.toISOString(),
      seconds,
      note: note.trim(),
    })
  }

  function label(id: string): Label | undefined {
    return pool.value.view.Label.get(id)
  }

  function comment(id: string | undefined): Comment | undefined {
    return id ? pool.value.view.Comment.get(id) : undefined
  }

  /** The user's notifications about issues they can still see, newest first. */
  const notifications = computed<Notification[]>(() =>
    [...pool.value.view.Notification.values()]
      .filter((n) => pool.value.view.Issue.has(n.issueId))
      .sort((a, b) => b.createdAt.localeCompare(a.createdAt)),
  )
  const unreadCount = computed(() => notifications.value.filter((n) => !n.readAt).length)

  /** Issues assigned to the signed-in user, across teams. */
  const myIssues = computed(() => issues.value.filter((i) => i.assigneeId === userId))

  /**
   * Finds an issue by what appears in its URL: `ENG-12`, or its ID for an
   * issue the server hasn't numbered yet.
   */
  function issueByRef(ref: string): Issue | undefined {
    const byId = pool.value.view.Issue.get(ref)
    if (byId) return byId
    const match = /^([A-Za-z][A-Za-z0-9]*)-(\d+)$/.exec(ref)
    if (!match) return undefined
    const owner = teamByKey(match[1]!.toUpperCase())
    const number = Number(match[2])
    if (!owner) return undefined
    const active = allIssues.value.find((i) => i.teamId === owner.id && i.number === number)
    if (active) return active
    // An archived issue that was fetched for viewing.
    for (const candidate of pool.value.view.Issue.values()) {
      if (candidate.teamId === owner.id && candidate.number === number) return candidate
    }
    return undefined
  }

  /**
   * Asks the server for an issue that isn't in the local copy, by ID or
   * KEY-number: archived issues aren't. Resolves to undefined if there is no
   * such issue, and rejects if the server can't be reached.
   */
  function fetchIssue(ref: string): Promise<Issue | undefined> {
    return engine.value?.fetchIssue(ref) ?? Promise.resolve(undefined)
  }

  /** Brings an archived issue back. It returns to lists when the change syncs. */
  async function unarchiveIssue(issueId: string) {
    await unwrap(api.POST('/issues/{issueId}/unarchive', { params: { path: { issueId } } }))
    await waitUntil(() => pool.value.view.Issue.get(issueId)?.archivedAt === undefined)
  }

  /** Resolves when `condition` holds, or after `timeoutMs` regardless. */
  function waitUntil(condition: () => boolean, timeoutMs = 3000): Promise<void> {
    return new Promise((resolve) => {
      let stop = () => {}
      const timer = setTimeout(() => {
        stop()
        resolve()
      }, timeoutMs)
      stop = watch(
        condition,
        (met) => {
          if (!met) return
          clearTimeout(timer)
          // The watcher may fire synchronously, before `stop` is assigned.
          queueMicrotask(() => stop())
          resolve()
        },
        { immediate: true },
      )
    })
  }

  // --- mutations -------------------------------------------------------

  function createIssue(input: NewIssue): Issue | undefined {
    const states = statesForTeam(input.teamId)
    const stateId = input.stateId ?? (states.find((s) => s.type === 'backlog') ?? states[0])?.id
    if (!engine.value || !stateId) return undefined

    const top = issuesForTeam(input.teamId)[0]
    const now = new Date().toISOString()
    const issue: Issue = {
      id: crypto.randomUUID(),
      teamId: input.teamId,
      // The server assigns the number; 0 marks an issue it hasn't seen yet.
      number: 0,
      title: input.title.trim(),
      description: input.description ?? '',
      stateId,
      priority: input.priority ?? 0,
      creatorId: userId,
      sortOrder: (top?.sortOrder ?? 0) - 1,
      labelIds: input.labelIds ?? [],
      subscriberIds: [userId],
      createdAt: now,
      updatedAt: now,
    }
    if (input.assigneeId) issue.assigneeId = input.assigneeId
    if (input.dueDate) issue.dueDate = input.dueDate
    if (input.projectId) issue.projectId = input.projectId
    if (input.cycleId) issue.cycleId = input.cycleId
    if (input.parentId) issue.parentId = input.parentId
    engine.value.mutate({ type: 'issue.create', issue })
    return issue
  }

  function updateIssue(issueId: string, patch: IssuePatch) {
    engine.value?.mutate({ type: 'issue.update', issueId, patch })
  }

  function archiveIssue(issueId: string) {
    engine.value?.mutate({ type: 'issue.archive', issueId })
  }

  function setSubscribed(issueId: string, subscribed: boolean) {
    engine.value?.mutate({ type: 'issue.subscribe', issueId, userId, subscribed })
  }

  function createComment(issueId: string, body: string) {
    const comment: Comment = { id: crypto.randomUUID(), issueId, userId, body, createdAt: new Date().toISOString() }
    engine.value?.mutate({ type: 'comment.create', comment })
  }

  function updateComment(commentId: string, body: string) {
    engine.value?.mutate({ type: 'comment.update', commentId, body })
  }

  function deleteComment(commentId: string) {
    engine.value?.mutate({ type: 'comment.delete', commentId })
  }

  /** Adds the user's reaction to a comment, or removes it if it is there. */
  function toggleReaction(commentId: string, emoji: string) {
    const reaction: Reaction = { commentId, userId, emoji }
    const on = !reactionsForComment(commentId).some((r) => r.userId === userId && r.emoji === emoji)
    engine.value?.mutate({ type: 'reaction.set', reaction, on })
  }

  function markNotification(notificationId: string, read: boolean) {
    const current = pool.value.view.Notification.get(notificationId)
    if (!current || Boolean(current.readAt) === read) return
    engine.value?.mutate({ type: 'notification.read', notificationId, read })
  }

  function markAllNotificationsRead() {
    for (const n of notifications.value) markNotification(n.id, true)
  }

  function deleteNotification(notificationId: string) {
    engine.value?.mutate({ type: 'notification.delete', notificationId })
  }

  /**
   * Uploads a file to an issue. Unlike other changes this needs the server,
   * so it fails when offline; the attachment appears once the upload is done.
   */
  async function uploadAttachment(issueId: string, file: File): Promise<Attachment> {
    const result = await unwrap(
      api.POST('/issues/{issueId}/attachments', {
        params: { path: { issueId } },
        // The generated type describes the file as a string; send real form data.
        body: { file: file as unknown as string },
        bodySerializer(body) {
          const form = new FormData()
          form.append('file', body.file as unknown as File)
          return form
        },
      }),
    )
    return result.attachment
  }

  async function deleteAttachment(attachmentId: string) {
    await unwrap(api.DELETE('/attachments/{attachmentId}', { params: { path: { attachmentId } } }))
  }

  // Projects, milestones, relations, templates, views and favorites are all
  // plain records: created with an ID chosen here, patched, deleted.
  function createRecord<M extends CrudModel>(model: M, record: Models[M]): Models[M] {
    engine.value?.mutate({ type: 'record.create', model, workspaceId, record })
    return record
  }

  function updateRecord(model: CrudModel, id: string, patch: Record<string, unknown>) {
    engine.value?.mutate({ type: 'record.update', model, id, patch })
  }

  function deleteRecord(model: CrudModel, id: string) {
    engine.value?.mutate({ type: 'record.delete', model, id })
  }

  function createProject(name: string): Project {
    const color = LABEL_COLORS[projects.value.length % LABEL_COLORS.length]!
    return createRecord('Project', {
      id: crypto.randomUUID(),
      name: name.trim(),
      description: '',
      status: 'backlog',
      color,
      createdAt: new Date().toISOString(),
    })
  }

  function createMilestone(projectId: string, name: string): Milestone {
    const last = milestonesForProject(projectId).at(-1)
    return createRecord('Milestone', { id: crypto.randomUUID(), projectId, name: name.trim(), sortOrder: (last?.sortOrder ?? 0) + 1 })
  }

  function createRelation(issueId: string, relatedIssueId: string, type: Relation['type']) {
    createRecord('Relation', { id: crypto.randomUUID(), issueId, relatedIssueId, type })
  }

  function createTemplate(teamId: string, name: string): Template {
    return createRecord('Template', { id: crypto.randomUUID(), teamId, name: name.trim(), title: '', description: '', priority: 0, labelIds: [] })
  }

  function createView(name: string, options: Record<string, unknown>, teamId?: string): View {
    const record: View = { id: crypto.randomUUID(), name: name.trim(), ownerId: userId, shared: false, options }
    if (teamId) record.teamId = teamId
    return createRecord('View', record)
  }

  /** Pins something to the sidebar, or unpins it if it is already there. */
  function toggleFavorite(kind: Favorite['kind'], targetId: string) {
    const existing = favoriteOf(kind, targetId)
    if (existing) return deleteRecord('Favorite', existing.id)
    const last = favorites.value.at(-1)
    createRecord('Favorite', { id: crypto.randomUUID(), kind, targetId, sortOrder: (last?.sortOrder ?? 0) + 1 })
  }

  function createLabel(name: string, color: string): Label | undefined {
    if (!engine.value) return undefined
    const label: Label = { id: crypto.randomUUID(), name: name.trim(), color }
    engine.value.mutate({ type: 'label.create', workspaceId, label })
    return label
  }

  function updateLabel(labelId: string, patch: LabelPatch) {
    engine.value?.mutate({ type: 'label.update', labelId, patch })
  }

  function deleteLabel(labelId: string) {
    engine.value?.mutate({ type: 'label.delete', labelId })
  }

  return {
    status,
    pendingCount,
    syncId,
    feed,
    stateIndex,
    closedCount,
    hygiene,
    recordCount,
    open,
    close,
    setConnectFailedHandler,
    setAccessLostHandler,
    loadIssueDetails,
    hasIssueDetails,
    teamMembers,
    users,
    teams,
    myTeamIds,
    labels,
    issues,
    allIssues,
    isTriage,
    triageIssues,
    issue,
    myIssues,
    projects,
    project,
    milestonesForProject,
    milestone,
    cyclesForTeam,
    cycle,
    currentCycle,
    cycleName,
    subIssues,
    relationsForIssue,
    relations: computed(() => [...pool.value.view.Relation.values()]),
    templatesForTeam,
    views,
    view,
    favorites,
    favoriteOf,
    createRecord,
    updateRecord,
    deleteRecord,
    createProject,
    createMilestone,
    createRelation,
    createTemplate,
    createView,
    toggleFavorite,
    user,
    team,
    teamByKey,
    state,
    labelsById,
    label,
    comment,
    commentsForIssue,
    activitiesForIssue,
    attachmentsForIssue,
    pullRequestsForIssue,
    customerRequests,
    customerRequest,
    newRequestCount,
    requestsForIssue,
    createCustomerRequest,
    initiatives,
    initiative,
    projectDependencies,
    projectUpdates,
    projectHealth,
    progressOfProject,
    createInitiative,
    addProjectDependency,
    postProjectUpdate,
    timeEntries,
    runningTimer,
    timeEntriesForIssue,
    startTimer,
    stopTimer,
    logTime,
    reactionsForComment,
    notifications,
    unreadCount,
    currentUserId: () => userId,
    statesForTeam,
    issuesForTeam,
    issueByRef,
    fetchIssue,
    unarchiveIssue,
    waitUntil,
    createIssue,
    updateIssue,
    archiveIssue,
    setSubscribed,
    createComment,
    updateComment,
    deleteComment,
    toggleReaction,
    markNotification,
    markAllNotificationsRead,
    deleteNotification,
    uploadAttachment,
    deleteAttachment,
    createLabel,
    updateLabel,
    deleteLabel,
  }
})
