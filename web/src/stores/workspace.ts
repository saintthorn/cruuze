import { defineStore } from 'pinia'
import { computed, ref } from 'vue'

import { api, unwrap, type Workspace, type WorkspaceInvite, type WorkspaceRole } from '@/api/client'
import type { TeamRecord } from '@/sync/types'

import { useSessionStore } from './session'
import { useSyncStore } from './sync'

/** A team plus whether the signed-in user belongs to it. */
export interface TeamView extends TeamRecord {
  isMember: boolean
}

/** The workspace currently open in the app shell. */
export const useWorkspaceStore = defineStore('workspace', () => {
  const session = useSessionStore()
  const sync = useSyncStore()

  const current = ref<Workspace | null>(null)
  const invites = ref<WorkspaceInvite[]>([])

  const teams = computed<TeamView[]>(() =>
    sync.teams.map((team) => ({ ...team, isMember: sync.myTeamIds.has(team.id) })),
  )
  const myTeams = computed(() => teams.value.filter((t) => t.isMember))
  // The user's synced record follows role changes as they happen; the
  // session's copy is only as fresh as the last page load.
  const role = computed<WorkspaceRole | undefined>(
    () => sync.user(session.user?.id)?.role ?? current.value?.role,
  )
  const isAdmin = computed(() => current.value != null && role.value === 'admin')
  const canCreateTeams = computed(() => current.value != null && role.value !== 'guest')
  /** Guests can use labels but not create, edit or delete them. */
  const canManageLabels = canCreateTeams

  function path() {
    if (!current.value) throw new Error('No workspace is open')
    return { workspaceId: current.value.id }
  }

  /** Switches to `workspace`, resolving once its data is ready to show. */
  async function open(workspace: Workspace) {
    if (current.value?.id === workspace.id) return
    const userId = session.user?.id
    if (!userId) throw new Error('Not signed in')

    current.value = null
    invites.value = []
    await sync.open(workspace.id, userId)
    current.value = workspace
    session.rememberWorkspace(workspace.urlKey)
  }

  /** Leaves the workspace. With `forget`, also deletes its local copy. */
  async function close(forget = false) {
    current.value = null
    invites.value = []
    await sync.close(forget)
  }

  async function createTeam(name: string, key: string, isPrivate = false, kind: TeamRecord['kind'] = 'engineering'): Promise<TeamView> {
    const team = await unwrap(
      api.POST('/workspaces/{workspaceId}/teams', { params: { path: path() }, body: { name, key, private: isPrivate, kind } }),
    )
    // The team reaches the local copy through sync; wait so callers can use it.
    await sync.waitUntil(() => sync.myTeamIds.has(team.id) && sync.statesForTeam(team.id).length > 0)
    return { ...(sync.team(team.id) ?? { ...team, kind, cycleWeeks: 0, triageEnabled: false, autoArchiveDays: 90, staleDays: 60, autoCloseDays: 0, autoCloseParent: true, autoAssign: true, autoCycle: true }), isMember: true }
  }

  /** Changes a team's name, privacy, cycle length or triage setting. */
  /** Deletes a team with everything it owns. Admins only; the server has every client reload. */
  async function deleteTeam(teamId: string) {
    await unwrap(api.DELETE('/teams/{teamId}', { params: { path: { teamId } } }))
  }

  async function updateTeam(
    teamId: string,
    settings: {
      name?: string
      kind?: TeamRecord['kind']
      private?: boolean
      cycleWeeks?: number
      triageEnabled?: boolean
      autoArchiveDays?: number
      staleDays?: number
      autoCloseDays?: number
      autoCloseParent?: boolean
      autoAssign?: boolean
      autoCycle?: boolean
      /** `null` clears it. */
      reviewStateId?: string | null
    },
  ) {
    const result = await unwrap(api.PATCH('/teams/{teamId}', { params: { path: { teamId } }, body: settings }))
    // The change reaches the local copy through sync; wait so the page shows it.
    const wanted = JSON.stringify(result.team)
    await sync.waitUntil(() => JSON.stringify(sync.team(teamId)) === wanted)
  }

  /** Adds someone to a team or takes them out of it. */
  async function setTeamMember(teamId: string, userId: string, isMember: boolean) {
    const params = { path: { teamId, userId } }
    if (isMember) await unwrap(api.PUT('/teams/{teamId}/members/{userId}', { params }))
    else await unwrap(api.DELETE('/teams/{teamId}/members/{userId}', { params }))
    await sync.waitUntil(() => sync.teamMembers(teamId).some((u) => u.id === userId) === isMember)
  }

  /** Joins or leaves a team as the signed-in user. */
  async function setTeamMembership(team: TeamView, isMember: boolean) {
    if (session.user) await setTeamMember(team.id, session.user.id, isMember)
  }

  async function rename(name: string) {
    const updated = await unwrap(api.PATCH('/workspaces/{workspaceId}', { params: { path: path() }, body: { name } }))
    if (current.value) current.value.name = updated.name
    const listed = session.workspaces.find((w) => w.id === updated.id)
    if (listed) listed.name = updated.name
  }

  /** Opens or closes the customer portal and sets what it shows. */
  async function updatePortal(patch: { enabled?: boolean; publicBoard?: boolean; intro?: string }) {
    const updated = await unwrap(api.PATCH('/workspaces/{workspaceId}/portal', { params: { path: path() }, body: patch }))
    for (const target of [current.value, session.workspaces.find((w) => w.id === updated.id)]) {
      if (target) Object.assign(target, { portalEnabled: updated.portalEnabled, portalPublicBoard: updated.portalPublicBoard, portalIntro: updated.portalIntro })
    }
  }

  async function setMemberRole(userId: string, newRole: WorkspaceRole) {
    await unwrap(
      api.PATCH('/workspaces/{workspaceId}/members/{userId}', { params: { path: { ...path(), userId } }, body: { role: newRole } }),
    )
    await sync.waitUntil(() => sync.user(userId)?.role === newRole)
  }

  /** Removes a member from the workspace. Removing yourself is leaving it. */
  async function removeMember(userId: string) {
    await unwrap(api.DELETE('/workspaces/{workspaceId}/members/{userId}', { params: { path: { ...path(), userId } } }))
    if (userId !== session.user?.id) await sync.waitUntil(() => sync.user(userId) === undefined)
  }

  async function loadInvites() {
    invites.value = await unwrap(
      api.GET('/workspaces/{workspaceId}/invites', { params: { path: path() } }),
    )
  }

  async function invite(email: string, role: WorkspaceRole) {
    const created = await unwrap(
      api.POST('/workspaces/{workspaceId}/invites', {
        params: { path: path() },
        body: { email, role },
      }),
    )
    invites.value.push(created)
  }

  async function revokeInvite(inviteId: string) {
    await unwrap(
      api.DELETE('/workspaces/{workspaceId}/invites/{inviteId}', {
        params: { path: { ...path(), inviteId } },
      }),
    )
    invites.value = invites.value.filter((i) => i.id !== inviteId)
  }

  return {
    current,
    teams,
    myTeams,
    invites,
    role,
    isAdmin,
    canCreateTeams,
    canManageLabels,
    open,
    close,
    createTeam,
    updateTeam,
    deleteTeam,
    setTeamMember,
    setTeamMembership,
    rename,
    updatePortal,
    setMemberRole,
    removeMember,
    loadInvites,
    invite,
    revokeInvite,
  }
})
