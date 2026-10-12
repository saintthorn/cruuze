import './style.css'
import './themes.css'

import { createPinia } from 'pinia'
import { createApp } from 'vue'

import { setSessionExpiredHandler } from './api/client'
import App from './App.vue'
import { applyStoredTheme } from './lib/themes'
import { registerServiceWorker } from './offline'
import { router } from './router'
import { useSessionStore } from './stores/session'
import { useSyncStore } from './stores/sync'
import { useToastStore } from './stores/toasts'
import { useWorkspaceStore } from './stores/workspace'
import { exitWorkspace } from './workspaceExit'

applyStoredTheme()

const app = createApp(App).use(createPinia()).use(router)

function sessionExpired() {
  const session = useSessionStore()
  if (!session.user && !useWorkspaceStore().current) return
  session.clear()
  // Keep the local copy: queued changes are sent after signing back in.
  void useWorkspaceStore().close()
  if (!router.currentRoute.value.meta.public) void router.push({ name: 'login' })
}

setSessionExpiredHandler(sessionExpired)

// The sync socket can't tell us why it failed to connect, so ask the server
// whether we are still signed in.
useSyncStore().setConnectFailedHandler(() => {
  if (!navigator.onLine) return
  const session = useSessionStore()
  session
    .load(true)
    .then(() => {
      if (!session.user) sessionExpired()
    })
    .catch(() => {})
})

// Removed from the workspace that is open (or it was deleted): drop its
// local copy and go wherever the user still belongs.
useSyncStore().setAccessLostHandler(() => {
  const workspace = useWorkspaceStore()
  const name = workspace.current?.name
  useToastStore().show(name ? `You no longer have access to ${name}.` : 'You no longer have access to that workspace.')
  void exitWorkspace()
})

app.mount('#app')
registerServiceWorker()
