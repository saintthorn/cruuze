import { defineStore } from 'pinia'
import { computed, ref } from 'vue'

import { api, ApiError, unwrap, type Me, type Workspace } from '@/api/client'

const LAST_WORKSPACE_KEY = 'cruuze:lastWorkspace'
const ME_KEY = 'cruuze:me'

/** The last known session, kept so the app can start with no connection. */
function rememberMe(value: Me | null) {
  try {
    if (value) localStorage.setItem(ME_KEY, JSON.stringify(value))
    else localStorage.removeItem(ME_KEY)
  } catch {
    // Best effort.
  }
}

function rememberedMe(): Me | null {
  try {
    const stored = localStorage.getItem(ME_KEY)
    return stored ? (JSON.parse(stored) as Me) : null
  } catch {
    return null
  }
}

/** The signed-in user and the workspaces they belong to. */
export const useSessionStore = defineStore('session', () => {
  const me = ref<Me | null>(null)
  const loaded = ref(false)

  const user = computed(() => me.value?.user ?? null)
  const workspaces = computed(() => me.value?.workspaces ?? [])

  /** Loads the session once; later calls are no-ops unless `force` is set. */
  async function load(force = false) {
    if (loaded.value && !force) return
    try {
      me.value = await unwrap(api.GET('/me'))
      rememberMe(me.value)
    } catch (err) {
      if (err instanceof ApiError && err.status === 401) {
        me.value = null
        rememberMe(null)
      } else if (err instanceof ApiError && err.status === 0 && rememberedMe()) {
        // No connection. Carry on as whoever was signed in here last: their
        // workspace opens from its local copy, and the server still checks
        // the real session on every request once it is reachable.
        me.value = rememberedMe()
        return // not `loaded`: ask the server again on the next navigation
      } else {
        throw err
      }
    }
    loaded.value = true
  }

  function set(value: Me | null) {
    me.value = value
    loaded.value = true
    rememberMe(value)
  }

  /** Forgets the session locally, e.g. after the server reports it expired. */
  function clear() {
    set(null)
  }

  async function verifyMagicLink(token: string) {
    set(await unwrap(api.POST('/auth/verify', { body: { token } })))
  }

  async function updateName(name: string) {
    const updated = await unwrap(api.PATCH('/me', { body: { name } }))
    if (me.value) me.value.user = updated
  }

  /** Turns emails about unread notifications on or off. */
  async function setEmailNotifications(on: boolean) {
    await unwrap(api.PATCH('/me', { body: { emailNotifications: on } }))
    if (me.value) me.value.emailNotifications = on
  }

  /** Says who the user is on GitHub, so what they do there is credited to them. Empty clears it. */
  async function setGithubLogin(login: string) {
    await unwrap(api.PATCH('/me', { body: { githubLogin: login } }))
    if (me.value) me.value.githubLogin = login
    rememberMe(me.value)
  }

  async function createWorkspace(name: string, urlKey: string): Promise<Workspace> {
    const workspace = await unwrap(api.POST('/workspaces', { body: { name, urlKey } }))
    me.value?.workspaces.push(workspace)
    return workspace
  }

  async function logout() {
    await unwrap(api.POST('/auth/logout'))
    set(null)
  }

  function workspaceByKey(urlKey: string): Workspace | undefined {
    return workspaces.value.find((w) => w.urlKey === urlKey)
  }

  /** The workspace to open when none is named: the last one visited, else the first. */
  function defaultWorkspace(): Workspace | undefined {
    let last: string | null = null
    try {
      last = localStorage.getItem(LAST_WORKSPACE_KEY)
    } catch {
      // Storage can be unavailable (private mode); fall through to the first workspace.
    }
    return (last ? workspaceByKey(last) : undefined) ?? workspaces.value[0]
  }

  function rememberWorkspace(urlKey: string) {
    try {
      localStorage.setItem(LAST_WORKSPACE_KEY, urlKey)
    } catch {
      // Best effort.
    }
  }

  return {
    me,
    loaded,
    user,
    workspaces,
    load,
    clear,
    verifyMagicLink,
    updateName,
    setEmailNotifications,
    setGithubLogin,
    createWorkspace,
    logout,
    workspaceByKey,
    defaultWorkspace,
    rememberWorkspace,
  }
})
