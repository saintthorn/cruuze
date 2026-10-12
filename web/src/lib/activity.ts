import type { Activity, Label, Notification, UserRecord, WorkflowState } from '@/sync/types'

import { formatDueDate, priorityLabel } from './issues'

export interface ActivityContext {
  user(id: string | undefined): UserRecord | undefined
  state(id: string): WorkflowState | undefined
  label(id: string): Label | undefined
  /** The name of a project, cycle or issue, if it still exists. */
  projectName(id: string): string | undefined
  cycleName(id: string): string | undefined
  milestoneName(id: string): string | undefined
  issueName(id: string): string | undefined
}

function personName(user: UserRecord | undefined): string {
  return user ? user.name || user.email : 'Someone'
}

/**
 * Who did something: a person, "Someone" for a person who has since left,
 * or the app itself when there never was an actor (cycle rollover, a pull
 * request moving its issue).
 */
export function actorName(actorId: string | undefined, user: UserRecord | undefined): string {
  return actorId ? personName(user) : 'Cruuze'
}

function labelNames(ids: readonly string[] | undefined, ctx: ActivityContext): string {
  return (ids ?? []).map((id) => ctx.label(id)?.name ?? 'a deleted label').join(', ')
}

/** What an activity entry says after the actor's name, e.g. "changed status from Todo to Done". */
export function describeActivity(activity: Activity, ctx: ActivityContext): string {
  const { from, to } = activity
  switch (activity.type) {
    case 'created':
      return 'created the issue'
    case 'archived':
      return 'archived the issue'
    case 'reviewed':
      return 'looked this over and kept it'
    case 'unarchived':
      return 'restored the issue from the archive'
    case 'title':
      return `changed the title from “${from}” to “${to}”`
    case 'description':
      return 'updated the description'
    case 'state': {
      const before = from ? ctx.state(from)?.name : undefined
      const after = to ? ctx.state(to)?.name : undefined
      return before && after ? `changed status from ${before} to ${after}` : 'changed the status'
    }
    case 'priority':
      return `set priority to ${priorityLabel(Number(to))}`
    case 'assignee':
      if (!to) return 'removed the assignee'
      return to === activity.actorId ? 'self-assigned the issue' : `assigned the issue to ${personName(ctx.user(to))}`
    case 'labels': {
      const added = labelNames(activity.addedLabelIds, ctx)
      const removed = labelNames(activity.removedLabelIds, ctx)
      const parts = []
      if (added) parts.push(`added ${activity.addedLabelIds!.length === 1 ? 'label' : 'labels'} ${added}`)
      if (removed) parts.push(`removed ${activity.removedLabelIds!.length === 1 ? 'label' : 'labels'} ${removed}`)
      return parts.join(' and ')
    }
    case 'project':
      if (!to) return 'removed the issue from its project'
      return `added the issue to ${ctx.projectName(to) ?? 'a project'}`
    case 'milestone':
      if (!to) return 'removed the issue from its milestone'
      return `moved the issue to milestone ${ctx.milestoneName(to) ?? '(deleted)'}`
    case 'cycle':
      if (!to) return 'removed the issue from its cycle'
      return `moved the issue to ${ctx.cycleName(to) ?? 'another cycle'}`
    case 'parent':
      if (!to) return 'removed the parent issue'
      return `made this a sub-issue of ${ctx.issueName(to) ?? 'another issue'}`
    case 'dueDate':
      return to ? `set the due date to ${formatDueDate(to)}` : 'removed the due date'
  }
}

/** One line describing a notification, e.g. "Ada mentioned you". */
export function describeNotification(notification: Notification, actor: UserRecord | undefined): string {
  const who = personName(actor)
  switch (notification.type) {
    case 'assigned':
      return `${who} assigned this to you`
    case 'mentioned':
      return `${who} mentioned you`
    case 'commented':
      return `${who} commented`
    case 'status':
      // Without an actor it was automatic, e.g. a pull request was merged.
      return notification.actorId ? `${who} changed the status` : 'The status changed'
  }
}
