import { api, ApiError, unwrap } from '@/api/client'

import { type Comment, type Issue, type Label, type ModelName, type Models, type Notification, type Reaction, reactionKey } from './types'

/**
 * Fields of an issue that can be edited. `null` clears a field that can be
 * empty. Labels are added and removed rather than replaced, so two people
 * labelling the same issue don't undo each other.
 */
export interface IssuePatch {
  title?: string
  description?: string
  stateId?: string
  priority?: number
  assigneeId?: string | null
  sortOrder?: number
  addLabelIds?: string[]
  removeLabelIds?: string[]
  dueDate?: string | null
  projectId?: string | null
  milestoneId?: string | null
  cycleId?: string | null
  parentId?: string | null
  /** Marks the issue as looked at and still wanted; it changes nothing else. */
  touch?: boolean
}

/** Models whose records are created, edited and deleted as plain records. */
export type CrudModel = 'Project' | 'Milestone' | 'Relation' | 'Template' | 'View' | 'Favorite' | 'WorkflowState' | 'TimeEntry' | 'Initiative' | 'ProjectDependency' | 'ProjectUpdate' | 'CustomerRequest'


export interface LabelPatch {
  name?: string
  color?: string
}

/**
 * A change made locally that the server has yet to confirm. Mutations are
 * plain data so they can wait in IndexedDB across reloads.
 */
export type Mutation =
  | { type: 'issue.create'; issue: Issue }
  | { type: 'issue.update'; issueId: string; patch: IssuePatch }
  | { type: 'issue.archive'; issueId: string }
  | { type: 'label.create'; workspaceId: string; label: Label }
  | { type: 'label.update'; labelId: string; patch: LabelPatch }
  | { type: 'label.delete'; labelId: string }
  | { type: 'issue.subscribe'; issueId: string; userId: string; subscribed: boolean }
  | { type: 'comment.create'; comment: Comment }
  | { type: 'comment.update'; commentId: string; body: string }
  | { type: 'comment.delete'; commentId: string }
  | { type: 'reaction.set'; reaction: Reaction; on: boolean }
  | { type: 'notification.read'; notificationId: string; read: boolean }
  | { type: 'notification.delete'; notificationId: string }
  | { type: 'record.create'; model: CrudModel; workspaceId: string; record: Models[CrudModel] }
  /** `null` in the patch clears a field. */
  | { type: 'record.update'; model: CrudModel; id: string; patch: Record<string, unknown> }
  | { type: 'record.delete'; model: CrudModel; id: string }

/** The record a mutation changes. */
export function mutationTarget(m: Mutation): { model: ModelName; key: string } {
  switch (m.type) {
    case 'issue.create':
      return { model: 'Issue', key: m.issue.id }
    case 'issue.update':
    case 'issue.archive':
      return { model: 'Issue', key: m.issueId }
    case 'label.create':
      return { model: 'Label', key: m.label.id }
    case 'label.update':
    case 'label.delete':
      return { model: 'Label', key: m.labelId }
    case 'issue.subscribe':
      return { model: 'Issue', key: m.issueId }
    case 'comment.create':
      return { model: 'Comment', key: m.comment.id }
    case 'comment.update':
    case 'comment.delete':
      return { model: 'Comment', key: m.commentId }
    case 'reaction.set':
      return { model: 'Reaction', key: reactionKey(m.reaction) }
    case 'notification.read':
    case 'notification.delete':
      return { model: 'Notification', key: m.notificationId }
    case 'record.create':
      return { model: m.model, key: (m.record as { id: string }).id }
    case 'record.update':
    case 'record.delete':
      return { model: m.model, key: m.id }
  }
}

/** Merges a patch into a record; `null` values remove the field. */
function patchRecord<T extends object>(record: T, patch: Record<string, unknown>): T {
  const next = { ...record } as Record<string, unknown>
  for (const [key, value] of Object.entries(patch)) {
    if (value === null) delete next[key]
    else if (value !== undefined) next[key] = value
  }
  return next as T
}

function patchIssue(issue: Issue, patch: IssuePatch): Issue {
  const { addLabelIds, removeLabelIds, touch, ...fields } = patch
  const next = patchRecord(issue, fields)
  // Anything done to an issue freshens it, as on the server.
  if (touch || Object.keys(fields).length > 0) next.updatedAt = new Date().toISOString()

  // A milestone belongs to a project, so moving project drops it (as the server does).
  if (fields.projectId !== undefined && (fields.projectId ?? undefined) !== issue.projectId && fields.milestoneId === undefined) {
    delete next.milestoneId
  }
  if (addLabelIds?.length || removeLabelIds?.length) {
    // Same rule as the server: removal wins, order is otherwise kept.
    const kept = issue.labelIds.filter((id) => !removeLabelIds?.includes(id))
    const added = (addLabelIds ?? []).filter((id) => !kept.includes(id) && !removeLabelIds?.includes(id))
    next.labelIds = [...kept, ...added]
  }
  return next
}

/**
 * Applies a mutation to the current copy of its record, returning what the
 * user should see (`undefined` for "no such record").
 */
export function applyMutation(m: Mutation, current: Models[ModelName] | undefined): Models[ModelName] | undefined {
  switch (m.type) {
    case 'issue.create':
      // Once the server's copy arrives it wins: it has the real number.
      return current ?? m.issue
    case 'issue.update':
      return current && patchIssue(current as Issue, m.patch)
    case 'label.create':
      return current ?? m.label
    case 'label.update':
      return current && { ...(current as Label), ...m.patch }
    case 'issue.subscribe': {
      if (!current) return undefined
      const issue = current as Issue
      const others = issue.subscriberIds.filter((id) => id !== m.userId)
      return { ...issue, subscriberIds: m.subscribed ? [...others, m.userId] : others }
    }
    case 'comment.create':
      return current ?? m.comment
    case 'comment.update':
      return current && { ...(current as Comment), body: m.body, editedAt: (current as Comment).editedAt ?? new Date().toISOString() }
    case 'reaction.set':
      return m.on ? (current ?? m.reaction) : undefined
    case 'notification.read': {
      if (!current) return undefined
      const { readAt, ...rest } = current as Notification
      return m.read ? { ...rest, readAt: readAt ?? new Date().toISOString() } : rest
    }
    case 'record.create':
      return current ?? m.record
    case 'record.update':
      return current && patchRecord(current, m.patch)
    case 'issue.archive':
    case 'label.delete':
    case 'comment.delete':
    case 'notification.delete':
    case 'record.delete':
      return undefined
  }
}

/** The server refused a mutation; retrying it would not help. */
export class MutationRejected extends Error {}

// Everything else (network failures, 401, 429, 5xx) is worth retrying.
const REJECTED_STATUSES = new Set([400, 403, 404, 409, 413, 422])

type Ack = Promise<{ syncId: number }>

/** How each plain-record model is created, edited and deleted on the server. */
const CRUD: {
  [M in CrudModel]: {
    create(workspaceId: string, record: Models[M]): Ack
    update?(id: string, patch: Record<string, unknown>): Ack
    remove(id: string): Ack
  }
} = {
  Project: {
    create: (workspaceId, body) => unwrap(api.POST('/workspaces/{workspaceId}/projects', { params: { path: { workspaceId } }, body })),
    update: (projectId, body) => unwrap(api.PATCH('/projects/{projectId}', { params: { path: { projectId } }, body })),
    remove: (projectId) => unwrap(api.DELETE('/projects/{projectId}', { params: { path: { projectId } } })),
  },
  Milestone: {
    create: (_, body) => unwrap(api.POST('/projects/{projectId}/milestones', { params: { path: { projectId: body.projectId } }, body })),
    update: (milestoneId, body) => unwrap(api.PATCH('/milestones/{milestoneId}', { params: { path: { milestoneId } }, body })),
    remove: (milestoneId) => unwrap(api.DELETE('/milestones/{milestoneId}', { params: { path: { milestoneId } } })),
  },
  Relation: {
    create: (_, body) => unwrap(api.POST('/issues/{issueId}/relations', { params: { path: { issueId: body.issueId } }, body })),
    remove: (relationId) => unwrap(api.DELETE('/relations/{relationId}', { params: { path: { relationId } } })),
  },
  Template: {
    create: (_, body) => unwrap(api.POST('/teams/{teamId}/templates', { params: { path: { teamId: body.teamId } }, body })),
    update: (templateId, body) => unwrap(api.PATCH('/templates/{templateId}', { params: { path: { templateId } }, body })),
    remove: (templateId) => unwrap(api.DELETE('/templates/{templateId}', { params: { path: { templateId } } })),
  },
  View: {
    create: (workspaceId, body) => unwrap(api.POST('/workspaces/{workspaceId}/views', { params: { path: { workspaceId } }, body })),
    update: (viewId, body) => unwrap(api.PATCH('/views/{viewId}', { params: { path: { viewId } }, body })),
    remove: (viewId) => unwrap(api.DELETE('/views/{viewId}', { params: { path: { viewId } } })),
  },
  Favorite: {
    create: (workspaceId, body) => unwrap(api.POST('/workspaces/{workspaceId}/favorites', { params: { path: { workspaceId } }, body })),
    remove: (favoriteId) => unwrap(api.DELETE('/favorites/{favoriteId}', { params: { path: { favoriteId } } })),
  },
  WorkflowState: {
    create: (_, body) => unwrap(api.POST('/teams/{teamId}/states', { params: { path: { teamId: body.teamId } }, body })),
    update: (stateId, body) => unwrap(api.PATCH('/states/{stateId}', { params: { path: { stateId } }, body })),
    remove: (stateId) => unwrap(api.DELETE('/states/{stateId}', { params: { path: { stateId } } })),
  },
  Initiative: {
    create: (workspaceId, body) => unwrap(api.POST('/workspaces/{workspaceId}/initiatives', { params: { path: { workspaceId } }, body })),
    update: (initiativeId, body) => unwrap(api.PATCH('/initiatives/{initiativeId}', { params: { path: { initiativeId } }, body })),
    remove: (initiativeId) => unwrap(api.DELETE('/initiatives/{initiativeId}', { params: { path: { initiativeId } } })),
  },
  ProjectDependency: {
    create: (_, body) => unwrap(api.POST('/projects/{projectId}/dependencies', { params: { path: { projectId: body.projectId } }, body })),
    remove: (dependencyId) => unwrap(api.DELETE('/project-dependencies/{dependencyId}', { params: { path: { dependencyId } } })),
  },
  ProjectUpdate: {
    create: (_, body) => unwrap(api.POST('/projects/{projectId}/updates', { params: { path: { projectId: body.projectId } }, body })),
    remove: (updateId) => unwrap(api.DELETE('/project-updates/{updateId}', { params: { path: { updateId } } })),
  },
  CustomerRequest: {
    create: (workspaceId, { id, title, body, requesterEmail, issueId }) =>
      unwrap(api.POST('/workspaces/{workspaceId}/requests', { params: { path: { workspaceId } }, body: { id, title, body, requesterEmail, issueId } })),
    update: (requestId, body) => unwrap(api.PATCH('/requests/{requestId}', { params: { path: { requestId } }, body })),
    remove: (requestId) => unwrap(api.DELETE('/requests/{requestId}', { params: { path: { requestId } } })),
  },
  TimeEntry: {
    // A running timer has no duration to send.
    create: (_, { id, issueId, startedAt, endedAt, seconds, note }) =>
      unwrap(
        api.POST('/issues/{issueId}/time-entries', {
          params: { path: { issueId } },
          body: { id, startedAt, endedAt, seconds: endedAt ? seconds : undefined, note },
        }),
      ),
    update: (timeEntryId, body) => unwrap(api.PATCH('/time-entries/{timeEntryId}', { params: { path: { timeEntryId } }, body })),
    remove: (timeEntryId) => unwrap(api.DELETE('/time-entries/{timeEntryId}', { params: { path: { timeEntryId } } })),
  },
}

async function send(m: Mutation): Promise<{ syncId: number }> {
  switch (m.type) {
    case 'record.create':
      return (CRUD[m.model].create as (workspaceId: string, record: Models[CrudModel]) => Ack)(m.workspaceId, m.record)
    case 'record.update': {
      const update = CRUD[m.model].update
      if (!update) throw new MutationRejected(`${m.model} records can't be edited`)
      return update(m.id, m.patch)
    }
    case 'record.delete':
      return CRUD[m.model].remove(m.id)
    case 'issue.create': {
      const { teamId } = m.issue
      const body = (({ id, title, description, stateId, priority, assigneeId, sortOrder, labelIds, dueDate, projectId, milestoneId, cycleId, parentId }) =>
        ({ id, title, description, stateId, priority, assigneeId, sortOrder, labelIds, dueDate, projectId, milestoneId, cycleId, parentId }))(m.issue)
      return unwrap(api.POST('/teams/{teamId}/issues', { params: { path: { teamId } }, body }))
    }
    case 'issue.update':
      return unwrap(api.PATCH('/issues/{issueId}', { params: { path: { issueId: m.issueId } }, body: m.patch }))
    case 'issue.archive':
      return unwrap(api.DELETE('/issues/{issueId}', { params: { path: { issueId: m.issueId } } }))
    case 'label.create':
      return unwrap(
        api.POST('/workspaces/{workspaceId}/labels', {
          params: { path: { workspaceId: m.workspaceId } },
          body: m.label,
        }),
      )
    case 'label.update':
      return unwrap(api.PATCH('/labels/{labelId}', { params: { path: { labelId: m.labelId } }, body: m.patch }))
    case 'label.delete':
      return unwrap(api.DELETE('/labels/{labelId}', { params: { path: { labelId: m.labelId } } }))
    case 'issue.subscribe': {
      const params = { path: { issueId: m.issueId } }
      return m.subscribed
        ? unwrap(api.PUT('/issues/{issueId}/subscription', { params }))
        : unwrap(api.DELETE('/issues/{issueId}/subscription', { params }))
    }
    case 'comment.create':
      return unwrap(
        api.POST('/issues/{issueId}/comments', {
          params: { path: { issueId: m.comment.issueId } },
          body: { id: m.comment.id, body: m.comment.body },
        }),
      )
    case 'comment.update':
      return unwrap(
        api.PATCH('/comments/{commentId}', { params: { path: { commentId: m.commentId } }, body: { body: m.body } }),
      )
    case 'comment.delete':
      return unwrap(api.DELETE('/comments/{commentId}', { params: { path: { commentId: m.commentId } } }))
    case 'reaction.set': {
      const params = { path: { commentId: m.reaction.commentId, emoji: m.reaction.emoji } }
      return m.on
        ? unwrap(api.PUT('/comments/{commentId}/reactions/{emoji}', { params }))
        : unwrap(api.DELETE('/comments/{commentId}/reactions/{emoji}', { params }))
    }
    case 'notification.read':
      return unwrap(
        api.PATCH('/notifications/{notificationId}', {
          params: { path: { notificationId: m.notificationId } },
          body: { read: m.read },
        }),
      )
    case 'notification.delete':
      return unwrap(
        api.DELETE('/notifications/{notificationId}', { params: { path: { notificationId: m.notificationId } } }),
      )
  }
}

/** Rethrows an API error as MutationRejected if retrying could not help. */
function rejectIfFinal(err: unknown): never {
  if (err instanceof ApiError && REJECTED_STATUSES.has(err.status)) {
    throw new MutationRejected(err.message)
  }
  throw err
}

/**
 * Sends several issue updates as one request, which applies all of them or
 * none. Resolves to the sync ID that includes them; throws like
 * {@link sendMutation}.
 */
export async function sendIssueUpdates(mutations: readonly Extract<Mutation, { type: 'issue.update' }>[]): Promise<number> {
  try {
    const updates = mutations.map((m) => ({ issueId: m.issueId, patch: m.patch }))
    return (await unwrap(api.POST('/issues/batch', { body: { updates } }))).syncId
  } catch (err) {
    rejectIfFinal(err)
  }
}

/**
 * Sends a mutation and resolves to the sync ID that includes it. Throws
 * {@link MutationRejected} if the server refused it, and any other error if
 * the attempt should be retried.
 */
export async function sendMutation(m: Mutation): Promise<number> {
  try {
    return (await send(m)).syncId
  } catch (err) {
    rejectIfFinal(err)
  }
}
