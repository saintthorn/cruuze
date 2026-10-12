import { router } from './router'
import { useSessionStore } from './stores/session'
import { useWorkspaceStore } from './stores/workspace'

/**
 * Closes the open workspace for good, deleting its local copy, and moves on
 * to another workspace if the user has one. For when they left it or were
 * removed from it.
 */
export async function exitWorkspace() {
  await useWorkspaceStore().close(true)
  // The list of workspaces has changed.
  await useSessionStore()
    .load(true)
    .catch(() => {})
  await router.push({ name: 'home' })
}
