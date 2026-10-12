import { stateKey } from '@/lib/filter'
import { branchName, type Grouping, type IssueGroup, issueIdentifier } from '@/lib/issues'
import { useSyncStore } from '@/stores/sync'
import { useToastStore } from '@/stores/toasts'
import type { IssuePatch } from '@/sync/mutations'
import type { Issue, WorkflowState } from '@/sync/types'

import { useCopy } from './useCopy'

/**
 * Changes that apply to one issue or to many at once. Lists that mix teams
 * are why statuses are addressed by key (see `stateKey`) rather than by ID:
 * each issue moves to its own team's state of that name.
 */
export function useIssueActions() {
  const sync = useSyncStore()
  const toasts = useToastStore()
  const copy = useCopy()

  function issuesOf(ids: readonly string[]): Issue[] {
    return ids.flatMap((id) => sync.issues.find((i) => i.id === id) ?? [])
  }

  /** The statuses any of the issues could move to, in workflow order. */
  function statusOptions(ids: readonly string[]): WorkflowState[] {
    const byKey = new Map<string, WorkflowState>()
    for (const teamId of new Set(issuesOf(ids).map((i) => i.teamId))) {
      for (const state of sync.statesForTeam(teamId)) {
        if (!byKey.has(stateKey(state))) byKey.set(stateKey(state), state)
      }
    }
    return [...byKey.values()].sort((a, b) => a.position - b.position)
  }

  /** The state of the issue's own team that matches a status key. */
  function stateForKey(issue: Issue, key: string): WorkflowState | undefined {
    return sync.statesForTeam(issue.teamId).find((s) => stateKey(s) === key)
  }

  function setStatus(ids: readonly string[], key: string) {
    for (const issue of issuesOf(ids)) {
      const state = stateForKey(issue, key)
      if (state && state.id !== issue.stateId) sync.updateIssue(issue.id, { stateId: state.id })
    }
  }

  function setPriority(ids: readonly string[], priority: number) {
    for (const issue of issuesOf(ids)) {
      if (issue.priority !== priority) sync.updateIssue(issue.id, { priority })
    }
  }

  function setAssignee(ids: readonly string[], assigneeId: string | null) {
    for (const issue of issuesOf(ids)) {
      if ((issue.assigneeId ?? null) !== assigneeId) sync.updateIssue(issue.id, { assigneeId })
    }
  }

  /** Whether all, some or none of the issues have the label. */
  function labelState(ids: readonly string[], labelId: string): 'all' | 'some' | 'none' {
    const issues = issuesOf(ids)
    const count = issues.filter((i) => i.labelIds.includes(labelId)).length
    return count === 0 ? 'none' : count === issues.length ? 'all' : 'some'
  }

  /** Adds the label to every issue, unless they all have it: then removes it. */
  function toggleLabel(ids: readonly string[], labelId: string) {
    const remove = labelState(ids, labelId) === 'all'
    for (const issue of issuesOf(ids)) {
      if (remove) sync.updateIssue(issue.id, { removeLabelIds: [labelId] })
      else if (!issue.labelIds.includes(labelId)) sync.updateIssue(issue.id, { addLabelIds: [labelId] })
    }
  }

  function archive(ids: readonly string[]) {
    const issues = issuesOf(ids)
    for (const issue of issues) sync.archiveIssue(issue.id)
    if (issues.length > 0) toasts.show(issues.length === 1 ? 'Issue archived' : `${issues.length} issues archived`)
  }

  function identifier(issue: Issue): string {
    return issueIdentifier(issue, sync.team(issue.teamId))
  }

  function copyIds(ids: readonly string[]) {
    const issues = issuesOf(ids)
    return copy(issues.map(identifier).join(', '), issues.length === 1 ? 'Issue ID' : 'Issue IDs')
  }

  /** The app URL of an issue; `path` is its route path. */
  function copyLink(path: string) {
    return copy(new URL(path, window.location.origin).href, 'Link')
  }

  /** A branch name for the issue; its pull request links back by the ID in it. */
  function copyBranch(issue: Issue) {
    const me = sync.user(sync.currentUserId())
    return copy(branchName(identifier(issue), issue.title, me?.name || me?.email || ''), 'Branch name')
  }

  /**
   * What dropping an issue into a group changes, beyond its position: the
   * property the list is grouped by.
   */
  function groupPatch(issue: Issue, group: IssueGroup, grouping: Grouping): IssuePatch {
    if (grouping === 'status' && group.state) {
      const state = stateForKey(issue, stateKey(group.state))
      return state && state.id !== issue.stateId ? { stateId: state.id } : {}
    }
    if (grouping === 'priority' && group.priority !== undefined && group.priority !== issue.priority) {
      return { priority: group.priority }
    }
    if (grouping === 'project' && (group.project?.id ?? null) !== (issue.projectId ?? null)) {
      return { projectId: group.project?.id ?? null }
    }
    if (grouping === 'assignee' && (group.user?.id ?? null) !== (issue.assigneeId ?? null)) {
      return { assigneeId: group.user?.id ?? null }
    }
    return {}
  }

  return {
    issuesOf,
    statusOptions,
    setStatus,
    setPriority,
    setAssignee,
    labelState,
    toggleLabel,
    archive,
    identifier,
    copyIds,
    copyLink,
    copyBranch,
    groupPatch,
  }
}
