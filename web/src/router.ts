import { createRouter, createWebHistory } from 'vue-router'

import { useSessionStore } from './stores/session'
import { useSyncStore } from './stores/sync'
import { useWorkspaceStore } from './stores/workspace'

declare module 'vue-router' {
  interface RouteMeta {
    /** Reachable without signing in. */
    public?: boolean
  }
}

export const router = createRouter({
  history: createWebHistory(),
  routes: [
    { path: '/login', name: 'login', component: () => import('./views/LoginView.vue'), meta: { public: true } },
    { path: '/auth/verify', name: 'verify', component: () => import('./views/VerifyView.vue'), meta: { public: true } },
    { path: '/unavailable', name: 'unavailable', component: () => import('./views/UnavailableView.vue'), meta: { public: true } },
    { path: '/portal/:urlKey', name: 'portal', component: () => import('./views/PortalView.vue'), meta: { public: true } },
    { path: '/welcome', name: 'welcome', component: () => import('./views/WelcomeView.vue') },
    { path: '/join', name: 'join', component: () => import('./views/JoinView.vue') },
    // Resolved by the guard below to the user's default workspace.
    { path: '/', name: 'home', component: { render: () => null } },
    {
      path: '/:workspaceKey',
      component: () => import('./views/WorkspaceLayout.vue'),
      children: [
        // Resolved by the guard below to the user's first team.
        { path: '', name: 'workspace', component: { render: () => null } },
        { path: 'inbox', name: 'inbox', component: () => import('./views/InboxView.vue') },
        { path: 'focus', name: 'focus', component: () => import('./views/FocusView.vue') },
        { path: 'my-issues', name: 'my-issues', component: () => import('./views/MyIssuesView.vue') },
        { path: 'search', name: 'search', component: () => import('./views/SearchView.vue') },
        { path: 'team/:teamKey/active', name: 'team-issues', component: () => import('./views/TeamIssuesView.vue') },
        { path: 'team/:teamKey/cycles', name: 'cycles', component: () => import('./views/CyclesView.vue') },
        { path: 'team/:teamKey/cycle/:number', name: 'cycle', component: () => import('./views/CycleDetailView.vue') },
        { path: 'team/:teamKey/calendar', name: 'calendar', component: () => import('./views/CalendarView.vue') },
        { path: 'team/:teamKey/triage', name: 'triage', component: () => import('./views/TriageView.vue') },
        { path: 'team/:teamKey/insights', name: 'insights', component: () => import('./views/InsightsView.vue') },
        { path: 'team/:teamKey/cleanup', name: 'cleanup', component: () => import('./views/CleanupView.vue') },
        { path: 'team/:teamKey/archived', name: 'archived', component: () => import('./views/ArchivedView.vue') },
        { path: 'projects', name: 'projects', component: () => import('./views/ProjectsView.vue') },
        { path: 'project/:projectId', name: 'project', component: () => import('./views/ProjectDetailView.vue') },
        { path: 'roadmap', name: 'roadmap', component: () => import('./views/RoadmapView.vue') },
        { path: 'requests', name: 'requests', component: () => import('./views/RequestsView.vue') },
        { path: 'view/:viewId', name: 'view', component: () => import('./views/SavedViewView.vue') },
        { path: 'issue/:issueRef', name: 'issue', component: () => import('./views/IssueDetailView.vue') },
        { path: 'settings/general', name: 'settings-workspace', component: () => import('./views/settings/WorkspaceView.vue') },
        { path: 'settings/members', name: 'settings-members', component: () => import('./views/settings/MembersView.vue') },
        { path: 'settings/teams', name: 'settings-teams', component: () => import('./views/settings/TeamsView.vue') },
        { path: 'settings/teams/:teamKey', name: 'settings-team', component: () => import('./views/settings/TeamSettingsView.vue') },
        { path: 'settings/labels', name: 'settings-labels', component: () => import('./views/settings/LabelsView.vue') },
        { path: 'settings/api', name: 'settings-api', component: () => import('./views/settings/ApiView.vue') },
        { path: 'settings/integrations', name: 'settings-integrations', component: () => import('./views/settings/IntegrationsView.vue') },
        { path: 'settings/import', name: 'settings-import', component: () => import('./views/settings/ImportView.vue') },
        { path: 'settings/instance', name: 'settings-instance', component: () => import('./views/settings/InstanceView.vue') },
        { path: 'settings/account', name: 'settings-account', component: () => import('./views/settings/AccountView.vue') },
        { path: 'settings/themes', name: 'settings-themes', component: () => import('./views/settings/ThemesView.vue') },
      ],
    },
  ],
})

router.beforeEach(async (to) => {
  if (to.name === 'unavailable') return true

  const session = useSessionStore()
  try {
    await session.load()
  } catch {
    return { name: 'unavailable', query: { next: to.fullPath } }
  }

  if (to.meta.public) {
    return to.name === 'login' && session.user ? { name: 'home' } : true
  }
  if (!session.user) return { name: 'login' }
  if (!session.user.name && to.name !== 'welcome') return { name: 'welcome' }

  if (to.name === 'home') {
    const workspace = session.defaultWorkspace()
    return workspace
      ? { name: 'workspace', params: { workspaceKey: workspace.urlKey } }
      : { name: 'join' }
  }

  const workspaceKey = to.params.workspaceKey
  if (typeof workspaceKey === 'string') {
    const workspace = session.workspaceByKey(workspaceKey)
    if (!workspace) return { name: 'home' }

    const store = useWorkspaceStore()
    try {
      await store.open(workspace)
    } catch {
      return { name: 'unavailable', query: { next: to.fullPath } }
    }
    if (to.name === 'workspace') {
      // With work assigned, the day starts at Focus; otherwise at the team's issues.
      const sync = useSyncStore()
      const hasWork = sync.myIssues.some((issue) => {
        const type = sync.state(issue.stateId)?.type
        return type !== 'completed' && type !== 'canceled'
      })
      if (hasWork) return { name: 'focus', params: { workspaceKey } }
      const team = store.myTeams[0]
      return team
        ? { name: 'team-issues', params: { workspaceKey, teamKey: team.key } }
        : { name: 'settings-teams', params: { workspaceKey } }
    }
  }
  return true
})
