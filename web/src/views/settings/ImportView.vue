<script setup lang="ts">
import { ArrowLeft, FileJson, FileSpreadsheet, Github, Link2, RotateCw, SquareKanban, Undo2, Workflow } from 'lucide-vue-next'
import { computed, onBeforeUnmount, onMounted, ref, watch as watchValue } from 'vue'
import { RouterLink, useRoute, useRouter } from 'vue-router'

import { api, errorMessage, unwrap } from '@/api/client'
import type { components } from '@/api/schema'
import SettingsPage from '@/components/SettingsPage.vue'
import BaseButton from '@/components/ui/BaseButton.vue'
import BaseInput from '@/components/ui/BaseInput.vue'
import FormError from '@/components/ui/FormError.vue'
import { parseImportLink } from '@/lib/importLink'
import { importSecret, rememberImportSecret } from '@/lib/importSecrets'
import { timeAgo } from '@/lib/time'
import { useSyncStore } from '@/stores/sync'
import { useWorkspaceStore } from '@/stores/workspace'

/**
 * Bringing work in from another tracker: say where from (a pasted link is
 * enough for a public GitHub repository), then either import at once with
 * the defaults or first check where each team goes and who each person is.
 * Then see what happened, and undo it if it isn't right. Where the server is
 * set up for it, a tracker is connected through its own sign-in page and no
 * token is typed at all; one that is typed is kept no longer than the import
 * runs. The rest of an import's settings are kept with it, so running it
 * again takes at most the token.
 */
type ImportRequest = components['schemas']['ImportRequest']
type Preview = components['schemas']['ImportPreview']
type Import = components['schemas']['Import']
type Source = ImportRequest['source']
type Connections = components['schemas']['ImportConnections']
type Provider = components['schemas']['ImportProvider']

const workspace = useWorkspaceStore()
const sync = useSyncStore()
const route = useRoute()
const router = useRouter()
const path = () => ({ workspaceId: workspace.current!.id })

const SOURCES: { value: Source; label: string; icon: typeof Github; blurb: string }[] = [
  { value: 'github', label: 'GitHub Issues', icon: Github, blurb: 'Issues, comments, labels, milestones' },
  { value: 'linear', label: 'Linear', icon: Workflow, blurb: 'Teams, cycles, projects, history' },
  { value: 'jira', label: 'Jira', icon: SquareKanban, blurb: 'Cloud, Server or Data Center' },
  { value: 'csv', label: 'CSV file', icon: FileSpreadsheet, blurb: 'Any export or spreadsheet' },
  { value: 'bundle', label: 'JSON file', icon: FileJson, blurb: 'Cruuze import format' },
]

const step = ref<'choose' | 'connect' | 'map' | 'run'>('choose')
const source = ref<Source>('github')
const sourceInfo = computed(() => SOURCES.find((s) => s.value === source.value)!)
const error = ref('')
const busy = ref(false)

// --- connections ---------------------------------------------------------------
// A tracker the admin has signed in to from here is read without a token.
const connections = ref<Connections>({ available: [], connections: [], resumable: false })
const isProvider = (value: Source): value is Provider => value === 'github' || value === 'linear' || value === 'jira'
const offered = (value: Source) => isProvider(value) && connections.value.available.includes(value)
const connectionTo = (value: Source) => connections.value.connections.find((c) => c.provider === value)
/** Set to type a token although the tracker is connected. */
const useToken = ref(false)
const connection_ = computed(() => (useToken.value ? undefined : connectionTo(source.value)))
/** Where the browser goes to sign in at the tracker; it comes back here. */
const connectHref = computed(() => `/api/workspaces/${workspace.current!.id}/imports/connect/${source.value}`)
/** The repositories a GitHub connection can read. */
const repos = ref<string[]>([])

async function loadConnections() {
  try {
    connections.value = await unwrap(api.GET('/workspaces/{workspaceId}/imports/connections', { params: { path: path() } }))
  } catch {
    // Tokens still work.
  }
}
async function loadRepos() {
  repos.value = []
  if (!connectionTo('github')) return
  try {
    repos.value = await unwrap(api.GET('/workspaces/{workspaceId}/imports/connections/github/repos', { params: { path: path() } }))
  } catch (err) {
    error.value = errorMessage(err)
  }
}
async function disconnect() {
  if (!isProvider(source.value)) return
  error.value = ''
  try {
    await unwrap(api.DELETE('/workspaces/{workspaceId}/imports/connections/{provider}', { params: { path: { ...path(), provider: source.value } } }))
    await loadConnections()
  } catch (err) {
    error.value = errorMessage(err)
  }
}
/** Opens a source's form. */
function choose(value: Source) {
  source.value = value
  step.value = 'connect'
  error.value = ''
  useToken.value = false
  if (value === 'github') void loadRepos()
}

// --- connect -------------------------------------------------------------------
const github = ref({ repo: '', token: '', baseUrl: '' })
const linear = ref({ apiKey: '' })
const jira = ref({ baseUrl: '', email: '', token: '' })
const file = ref<{ name: string; text: string } | null>(null)
const csvTeam = ref('')

// A pasted link says which tracker and where. A public GitHub repository
// needs nothing more, so it is imported at once; the others still need a
// token, and go to their form with the address filled in.
const link = ref('')
async function followLink() {
  const found = parseImportLink(link.value)
  if (!found) {
    error.value = 'Paste a GitHub, Jira or Linear link.'
    return
  }
  choose(found.source)
  if (found.source === 'jira') jira.value.baseUrl = found.baseUrl
  if (found.source === 'github') github.value = { repo: found.repo, token: '', baseUrl: '' }
  // Nothing more to ask: a public repository, or a connected tracker.
  if (found.source === 'github' || connectionTo(found.source)) await connect(true)
}

async function pickFile(event: Event) {
  const chosen = (event.target as HTMLInputElement).files?.[0]
  file.value = chosen ? { name: chosen.name, text: await chosen.text() } : null
}

/** The request as far as the connection goes; the mapping is added when it runs. */
function connection(): ImportRequest {
  if (connection_.value) {
    return source.value === 'github'
      ? { source: 'github', github: { repo: github.value.repo.trim() }, useConnection: true }
      : { source: source.value, useConnection: true }
  }
  switch (source.value) {
    case 'github':
      return {
        source: 'github',
        github: { repo: github.value.repo.trim(), token: github.value.token.trim() || undefined, baseUrl: github.value.baseUrl.trim() || undefined },
      }
    case 'linear':
      return { source: 'linear', linear: { apiKey: linear.value.apiKey.trim() } }
    case 'jira':
      return { source: 'jira', jira: { baseUrl: jira.value.baseUrl.trim(), email: jira.value.email.trim() || undefined, token: jira.value.token.trim() } }
    case 'csv':
      return {
        source: 'csv',
        csv: { text: file.value?.text ?? '', name: file.value?.name.replace(/\.csv$/i, ''), teamName: csvTeam.value.trim() || undefined },
      }
    case 'bundle':
      return { source: 'bundle', bundle: JSON.parse(file.value?.text ?? '{}') }
  }
}

const canConnect = computed(() => {
  if (connection_.value) return source.value !== 'github' || github.value.repo.trim() !== ''
  switch (source.value) {
    case 'github':
      return github.value.repo.trim() !== ''
    case 'linear':
      return linear.value.apiKey.trim() !== ''
    case 'jira':
      return jira.value.baseUrl.trim() !== '' && jira.value.token.trim() !== ''
    default:
      return file.value !== null
  }
})

// --- map -----------------------------------------------------------------------
const preview = ref<Preview | null>(null)
const NEW_TEAM = ''
const NOBODY = ''
/** Per source team: whether to import it, and into which team (NEW_TEAM makes one). */
const teamChoice = ref<Record<string, { include: boolean; into: string }>>({})
/** Per source person: the member they are, or NOBODY. */
const userChoice = ref<Record<string, string>>({})

// What each of the source's teams (a Jira project, a repository) becomes: a
// project in one of the teams here, or a team of its own. The last choice is
// remembered in this browser.
type Shape = 'projects' | 'teams'
const SHAPE_KEY = 'cruuze.import.as'
function storedShape(): Shape {
  try {
    return localStorage.getItem(SHAPE_KEY) === 'teams' ? 'teams' : 'projects'
  } catch {
    return 'projects'
  }
}
/** The team a project goes into unless another is picked: the first of yours. */
const homeTeam = computed(() => (workspace.myTeams[0] ?? workspace.teams[0])?.id ?? NEW_TEAM)
// Without a team to put projects in, teams are all there can be.
const shape = ref<Shape>(storedShape())
const asProjects = computed(() => shape.value === 'projects' && homeTeam.value !== NEW_TEAM)

/** Where a source team goes by default in the current shape. */
function defaultInto(team: Preview['teams'][number]): string {
  return asProjects.value ? (team.teamId ?? homeTeam.value) : (team.teamId ?? NEW_TEAM)
}
watchValue(shape, (value) => {
  try {
    localStorage.setItem(SHAPE_KEY, value)
  } catch {
    // Not remembered, but applied.
  }
  for (const team of preview.value?.teams ?? []) {
    const choice = teamChoice.value[team.id]
    if (choice) choice.into = defaultInto(team)
  }
})

/**
 * Reads what the source has. Then either shows it for checking, or (quick)
 * imports it at once as it would be shown: every team, people matched as
 * the preview matched them.
 */
async function connect(quick = false) {
  error.value = ''
  busy.value = true
  try {
    preview.value = await unwrap(api.POST('/workspaces/{workspaceId}/imports/preview', { params: { path: path() }, body: connection() }))
    teamChoice.value = Object.fromEntries(preview.value.teams.map((t) => [t.id, { include: true, into: defaultInto(t) }]))
    userChoice.value = Object.fromEntries(preview.value.users.map((u) => [u.id, u.userId ?? NOBODY]))
    changesOnly.value = true
    if (quick) await start()
    else step.value = 'map'
  } catch (err) {
    error.value = err instanceof SyntaxError ? 'That file isn’t valid JSON.' : errorMessage(err)
  } finally {
    busy.value = false
  }
}

const chosenTeams = computed(() => preview.value?.teams.filter((t) => teamChoice.value[t.id]?.include) ?? [])
const issueCount = computed(() => {
  const counts = chosenTeams.value.map((t) => t.issues)
  return counts.some((n) => n < 0) ? null : counts.reduce((sum, n) => sum + n, 0)
})
const unmatched = computed(() => preview.value?.users.filter((u) => userChoice.value[u.id] === NOBODY).length ?? 0)

// GitHub and Jira can be asked for only what changed since the last import
// of the same thing that ran to the end. That is what happens unless this
// is turned off, which reads everything again.
const changesOnly = ref(true)
const importedBefore = computed(
  () =>
    (source.value === 'github' || source.value === 'jira') &&
    history.value.some((i) => i.status === 'done' && i.source === source.value && i.label === preview.value?.label),
)

// --- run -----------------------------------------------------------------------
const running = ref<Import | null>(null)
let poll: ReturnType<typeof setTimeout> | undefined

async function start() {
  if (!preview.value) return
  error.value = ''
  busy.value = true
  try {
    const body: ImportRequest = {
      ...connection(),
      teams: chosenTeams.value.map((t) => t.id),
      teamMap: Object.fromEntries(chosenTeams.value.filter((t) => teamChoice.value[t.id]!.into !== NEW_TEAM).map((t) => [t.id, teamChoice.value[t.id]!.into])),
      userMap: userChoice.value,
      asProjects: asProjects.value,
      full: !changesOnly.value || undefined,
    }
    running.value = await unwrap(api.POST('/workspaces/{workspaceId}/imports', { params: { path: path() }, body }))
    rememberImportSecret(running.value.source, running.value.label, secretOf(body))
    // The credentials have done their work.
    github.value.token = linear.value.apiKey = jira.value.token = ''
    step.value = 'run'
    watch(running.value.id)
  } catch (err) {
    error.value = errorMessage(err)
  } finally {
    busy.value = false
  }
}

function watch(id: string) {
  clearTimeout(poll)
  poll = setTimeout(async () => {
    try {
      running.value = await unwrap(api.GET('/imports/{importId}', { params: { path: { importId: id } } }))
    } catch {
      // A dropped request: ask again.
    }
    if (running.value?.status === 'running') watch(id)
    else void loadHistory()
  }, 1500)
}
onBeforeUnmount(() => clearTimeout(poll))

// --- run again -----------------------------------------------------------------
const SECRET_LABEL: Partial<Record<Source, string>> = {
  github: 'GitHub token, for a private repository',
  linear: 'Linear API key',
  jira: 'Jira API token',
}
const secretOf = (request: ImportRequest) => request.jira?.token || request.linear?.apiKey || request.github?.token || ''

/** The import being run again, while its token is asked for. */
const rerunning = ref<Import | null>(null)
const rerunSecret = ref('')

/** Whether an import kept enough of its request to be run again. */
const canRerun = (item: Import) => item.status !== 'running' && item.request !== undefined

/** Runs an import again: at once if its token is still known in this tab, otherwise after asking for it. */
function rerun(item: Import) {
  // Read through a connection: there is no token to ask for.
  if (item.request?.useConnection) return void runAgain(item, '')
  const known = importSecret(item.source, item.label)
  if (known !== undefined) return void runAgain(item, known)
  rerunSecret.value = ''
  rerunning.value = item
}

async function runAgain(item: Import, secret: string) {
  error.value = ''
  busy.value = true
  try {
    // What the import was asked the first time, with the token put back.
    // (Copied through JSON: the record is reactive, which structuredClone refuses.)
    const body = JSON.parse(JSON.stringify(item.request)) as ImportRequest
    if (!body.useConnection) {
      if (body.jira) body.jira.token = secret
      if (body.linear) body.linear.apiKey = secret
      if (body.github && secret) body.github.token = secret
    }
    running.value = await unwrap(api.POST('/workspaces/{workspaceId}/imports', { params: { path: path() }, body }))
    rememberImportSecret(item.source, item.label, secret)
    rerunning.value = null
    rerunSecret.value = ''
    step.value = 'run'
    watch(running.value.id)
  } catch (err) {
    error.value = errorMessage(err)
  } finally {
    busy.value = false
  }
}

function reset() {
  step.value = 'choose'
  preview.value = running.value = file.value = null
  error.value = ''
}

// --- history -------------------------------------------------------------------
const history = ref<Import[]>([])

async function loadHistory() {
  try {
    history.value = await unwrap(api.GET('/workspaces/{workspaceId}/imports', { params: { path: path() } }))
    // An import started in another tab, or before a reload: pick it up.
    const active = history.value.find((i) => i.status === 'running')
    if (active && step.value === 'choose') {
      running.value = active
      step.value = 'run'
      watch(active.id)
    }
  } catch (err) {
    error.value = errorMessage(err)
  }
}
onMounted(async () => {
  void loadHistory()
  await loadConnections()
  // Back from a tracker's sign-in page: carry on where that began.
  const connected = route.query.connected
  if (typeof connected === 'string' && connectionTo(connected as Source)) choose(connected as Source)
  if (route.query.connect_error !== undefined) error.value = 'Couldn’t connect. Try again, or use a token.'
  if (connected !== undefined || route.query.connect_error !== undefined) void router.replace({ query: {} })
})

async function undo(item: Import) {
  if (!window.confirm(`Undo the import of ${item.label}? Everything it created is deleted.`)) return
  error.value = ''
  try {
    const undone = await unwrap(api.POST('/imports/{importId}/undo', { params: { path: { importId: item.id } } }))
    if (running.value?.id === undone.id) running.value = undone
    await loadHistory()
  } catch (err) {
    error.value = errorMessage(err)
  }
}

const COUNT_LABELS: [key: string, one: string, many: string][] = [
  ['issues', 'issue', 'issues'],
  ['updated', 'issue updated', 'issues updated'],
  ['unchanged', 'issue unchanged', 'issues unchanged'],
  ['comments', 'comment', 'comments'],
  ['files', 'file', 'files'],
  ['teams', 'team', 'teams'],
  ['statuses', 'status', 'statuses'],
  ['labels', 'label', 'labels'],
  ['projects', 'project', 'projects'],
  ['milestones', 'milestone', 'milestones'],
  ['cycles', 'cycle', 'cycles'],
]
function summary(counts: Import['counts']): string {
  const parts = COUNT_LABELS.filter(([key]) => (counts[key] ?? 0) > 0).map(([key, one, many]) => `${counts[key]!.toLocaleString()} ${counts[key] === 1 ? one : many}`)
  return parts.length > 0 ? parts.join(', ') : 'nothing new'
}
const STATUS = {
  running: { label: 'Running', class: 'text-accent' },
  done: { label: 'Done', class: 'text-success' },
  failed: { label: 'Stopped', class: 'text-danger' },
  undone: { label: 'Undone', class: 'text-faint' },
} as const

const selectClass = 'h-8 max-w-full rounded-md border border-border-strong bg-elevated px-2 text-fg outline-none focus:border-accent'
</script>

<template>
  <SettingsPage title="Import">
    <section v-if="!workspace.isAdmin" class="rounded-lg border border-border p-4">
      <p class="text-muted">Admins only.</p>
    </section>

    <template v-else>
      <FormError :message="error" />

      <!-- 1. Where from -->
      <section v-if="step === 'choose'" aria-labelledby="import-from" class="rounded-lg border border-border p-4">
        <h2 id="import-from" class="font-medium">Source</h2>
        <form class="mt-3 flex items-end gap-2" @submit.prevent="followLink">
          <BaseInput v-model="link" class="min-w-0 flex-1" label="Link" placeholder="github.com/owner/repo · acme.atlassian.net · linear.app/acme" />
          <BaseButton type="submit" variant="primary" :loading="busy" :disabled="link.trim() === ''">Import</BaseButton>
        </form>
        <ul class="mt-3 grid gap-2 sm:grid-cols-2">
          <li v-for="option in SOURCES" :key="option.value">
            <button
              type="button"
              class="flex h-full w-full items-start gap-3 rounded-md border border-border-strong p-3 text-left hover:border-accent hover:bg-hover"
              @click="choose(option.value)"
            >
              <component :is="option.icon" class="mt-0.5 size-4 shrink-0 text-muted" aria-hidden="true" />
              <span>
                <span class="font-medium">{{ option.label }}</span>
                <span class="block text-xs text-muted">{{ connectionTo(option.value)?.label ?? option.blurb }}</span>
              </span>
            </button>
          </li>
        </ul>
      </section>

      <!-- 2. Connect -->
      <section v-else-if="step === 'connect'" aria-labelledby="import-connect" class="rounded-lg border border-border p-4">
        <button type="button" class="mb-2 flex items-center gap-1 text-xs text-muted hover:text-fg" @click="reset">
          <ArrowLeft class="size-3" aria-hidden="true" /> Back
        </button>
        <h2 id="import-connect" class="font-medium">{{ sourceInfo.label }}</h2>
        <form class="mt-3 flex flex-col gap-3" @submit.prevent="connect(true)">
          <template v-if="connection_">
            <p class="flex flex-wrap items-center gap-x-3 gap-y-1">
              <span class="flex items-center gap-1.5"><Link2 class="size-3.5 text-success" aria-hidden="true" /> {{ connection_.label }}</span>
              <button type="button" class="text-xs text-muted hover:text-fg" @click="disconnect">Disconnect</button>
              <button type="button" class="text-xs text-muted hover:text-fg" @click="useToken = true">Use a token</button>
            </p>
            <template v-if="source === 'github'">
              <label v-if="repos.length > 0" class="flex flex-col gap-1.5 text-xs font-medium text-muted">
                Repository
                <select v-model="github.repo" required :class="selectClass" class="font-normal">
                  <option value="" disabled>Choose…</option>
                  <option v-if="github.repo && !repos.includes(github.repo)" :value="github.repo">{{ github.repo }}</option>
                  <option v-for="repo in repos" :key="repo" :value="repo">{{ repo }}</option>
                </select>
              </label>
              <BaseInput v-else v-model="github.repo" label="Repository" placeholder="owner/name" required />
            </template>
          </template>
          <a
            v-else-if="offered(source) && !useToken"
            :href="connectHref"
            class="flex h-8 items-center gap-2 self-start rounded-md bg-accent px-3 text-sm font-medium text-accent-fg hover:bg-accent-hover"
          >
            <Link2 class="size-3.5" aria-hidden="true" /> Connect {{ sourceInfo.label.replace(' Issues', '') }}
          </a>
          <p v-if="!connection_ && offered(source) && !useToken" class="text-xs text-faint">Or use a token:</p>
          <template v-if="connection_"></template>
          <template v-else-if="source === 'github'">
            <BaseInput v-model="github.repo" label="Repository" placeholder="owner/name" required autofocus />
            <BaseInput
              v-model="github.token"
              label="Personal access token"
              type="password"
              placeholder="github_pat_…"
              hint="Required for private repositories. Needs read access to Issues."
            />
            <BaseInput
              v-model="github.baseUrl"
              label="Enterprise API URL (optional)"
              placeholder="https://github.example.com/api/v3"
            />
          </template>
          <template v-else-if="source === 'linear'">
            <BaseInput
              v-model="linear.apiKey"
              label="API key"
              type="password"
              placeholder="lin_api_…"
              required
              autofocus
              hint="Linear → Settings → Security & access → Personal API keys"
            />
          </template>
          <template v-else-if="source === 'jira'">
            <BaseInput v-model="jira.baseUrl" label="Site address" placeholder="https://acme.atlassian.net" required autofocus />
            <BaseInput
              v-model="jira.email"
              label="Account email"
              type="email"
              placeholder="you@company.com"
              hint="Cloud only. Leave empty for Server or Data Center."
            />
            <BaseInput
              v-model="jira.token"
              label="API token"
              type="password"
              required
              hint="Cloud: id.atlassian.com → Security → API tokens. Server: Profile → Personal Access Tokens."
            />
          </template>
          <template v-else>
            <label class="flex flex-col gap-1.5 text-xs font-medium text-muted">
              {{ source === 'csv' ? 'CSV file' : 'JSON file' }}
              <input
                type="file"
                :accept="source === 'csv' ? '.csv,text/csv' : '.json,application/json'"
                required
                class="text-sm font-normal text-fg file:mr-3 file:h-8 file:rounded-md file:border file:border-border-strong file:bg-elevated file:px-3 file:text-fg"
                @change="pickFile"
              />
            </label>
            <template v-if="source === 'csv'">
              <BaseInput
                v-model="csvTeam"
                label="Default team (optional)"
                placeholder="Imported"
              />
              <p class="text-xs text-faint">A Title column is required.</p>
            </template>
            <p v-else class="text-xs text-faint">Format: docs/import-format.md</p>
          </template>
          <p v-if="!connection_ && source !== 'csv' && source !== 'bundle'" class="text-xs text-faint">
            {{ connections.resumable ? 'Tokens are kept encrypted until the import ends.' : 'Credentials are not stored.' }}
          </p>
          <div class="flex flex-wrap gap-2">
            <BaseButton type="submit" variant="primary" :loading="busy" :disabled="!canConnect">Import</BaseButton>
            <BaseButton :disabled="busy || !canConnect" @click="connect()">Review first</BaseButton>
          </div>
        </form>
      </section>

      <!-- 3. Where it goes -->
      <section v-else-if="step === 'map' && preview" aria-labelledby="import-map" class="rounded-lg border border-border p-4">
        <button type="button" class="mb-2 flex items-center gap-1 text-xs text-muted hover:text-fg" @click="step = 'connect'">
          <ArrowLeft class="size-3" aria-hidden="true" /> Back
        </button>
        <h2 id="import-map" class="font-medium">{{ preview.label }}</h2>

        <div v-if="homeTeam !== NEW_TEAM" role="radiogroup" aria-label="Bring in as" class="mt-4 inline-flex rounded-md border border-border-strong p-0.5 text-xs">
          <button
            v-for="option in [{ value: 'projects', label: 'Projects' }, { value: 'teams', label: 'Teams' }] as const"
            :key="option.value"
            type="button"
            role="radio"
            :aria-checked="shape === option.value"
            class="rounded-sm px-3 py-1"
            :class="shape === option.value ? 'bg-active text-fg' : 'text-muted hover:text-fg'"
            @click="shape = option.value"
          >
            {{ option.label }}
          </button>
        </div>

        <h3 class="mt-4 text-xs font-medium text-faint">// {{ asProjects ? 'projects' : 'teams' }}</h3>
        <ul class="mt-1 divide-y divide-border border-y border-border">
          <li v-for="team in preview.teams" :key="team.id" class="flex flex-wrap items-center gap-x-3 gap-y-1 py-2">
            <label class="flex min-w-0 flex-1 items-center gap-2 select-none">
              <input v-model="teamChoice[team.id]!.include" type="checkbox" class="size-3.5 accent-(--accent)" />
              <span class="truncate font-medium">{{ team.name }}</span>
              <span class="shrink-0 text-xs text-faint tabular-nums">{{ team.issues >= 0 ? `${team.issues.toLocaleString()} issues` : '' }}</span>
            </label>
            <label class="flex items-center gap-2 text-xs text-muted">
              {{ asProjects ? 'in' : 'into' }}
              <select v-model="teamChoice[team.id]!.into" :class="selectClass" :disabled="!teamChoice[team.id]!.include" :aria-label="`Where ${team.name} goes`">
                <option v-if="!asProjects" :value="NEW_TEAM">a new team, {{ team.key }}</option>
                <option v-for="existing in workspace.teams" :key="existing.id" :value="existing.id">{{ existing.name }} ({{ existing.key }})</option>
              </select>
            </label>
          </li>
        </ul>
        <p class="mt-1 text-xs text-faint">{{ asProjects ? 'Each becomes a project in the team chosen.' : 'Issue numbers are kept in new or empty teams.' }}</p>

        <template v-if="preview.users.length > 0">
          <h3 class="mt-5 text-xs font-medium text-faint">// people · {{ unmatched }}/{{ preview.users.length }} unmatched</h3>
          <ul class="mt-1 max-h-72 divide-y divide-border overflow-y-auto border-y border-border">
            <li v-for="person in preview.users" :key="person.id" class="flex flex-wrap items-center gap-x-3 gap-y-1 py-1.5">
              <span class="min-w-0 flex-1 truncate">
                {{ person.name }} <span v-if="person.email && person.email !== person.name" class="text-xs text-faint">{{ person.email }}</span>
              </span>
              <select v-model="userChoice[person.id]" :class="selectClass" :aria-label="`Who ${person.name} is here`">
                <option :value="NOBODY">(nobody)</option>
                <option v-for="member in sync.users" :key="member.id" :value="member.id">{{ member.name || member.email }}</option>
              </select>
            </li>
          </ul>
          <p class="mt-1 text-xs text-faint">Unmatched people are credited by name in the text.</p>
        </template>

        <div class="mt-5 flex flex-wrap items-center gap-3">
          <BaseButton variant="primary" :loading="busy" :disabled="chosenTeams.length === 0" @click="start">
            Import {{ issueCount === null ? '' : issueCount.toLocaleString() }} {{ issueCount === 1 ? 'issue' : 'issues' }}
          </BaseButton>
          <label v-if="importedBefore" class="flex items-center gap-1.5 text-xs text-muted select-none">
            <input v-model="changesOnly" type="checkbox" class="size-3.5 accent-(--accent)" /> Changes only
          </label>
          <span class="text-xs text-faint">Can be undone.</span>
        </div>
      </section>

      <!-- 4. Running, and what happened -->
      <section v-else-if="step === 'run' && running" aria-labelledby="import-run" aria-live="polite" class="rounded-lg border border-border p-4">
        <h2 id="import-run" class="flex items-center gap-2 font-medium">
          {{ running.label }}
          <span class="text-xs font-normal" :class="STATUS[running.status].class">{{ STATUS[running.status].label }}</span>
        </h2>
        <p v-if="running.status === 'running'" class="mt-2 text-muted">
          {{ running.stage || 'Working' }}…
        </p>
        <p class="mt-2 tabular-nums">{{ summary(running.counts) }}</p>
        <p v-if="running.error" role="alert" class="mt-2 text-danger">{{ running.error }}</p>
        <ul v-if="running.warnings.length > 0" class="mt-2 flex list-disc flex-col gap-1 pl-5 text-xs text-warning">
          <li v-for="warning in running.warnings" :key="warning">{{ warning }}</li>
        </ul>
        <div v-if="running.status !== 'running'" class="mt-4 flex flex-wrap gap-2">
          <RouterLink
            v-if="running.status === 'done'"
            :to="{ name: 'workspace' }"
            class="flex h-8 items-center rounded-md bg-accent px-3 text-sm font-medium text-accent-fg hover:bg-accent-hover"
          >
            View issues
          </RouterLink>
          <BaseButton v-if="canRerun(running)" :loading="busy && !rerunning" @click="rerun(running)">
            <RotateCw class="size-3.5" aria-hidden="true" /> Re-run
          </BaseButton>
          <BaseButton @click="reset">New import</BaseButton>
          <BaseButton v-if="running.status !== 'undone'" variant="ghost" @click="undo(running)"><Undo2 class="size-3.5" aria-hidden="true" /> Undo</BaseButton>
        </div>
      </section>

      <!-- Running one again: only its token is asked for -->
      <form
        v-if="rerunning"
        aria-labelledby="import-rerun"
        class="rounded-lg border border-border-strong p-4"
        @submit.prevent="runAgain(rerunning, rerunSecret.trim())"
      >
        <h2 id="import-rerun" class="font-medium">Re-run {{ rerunning.label }}</h2>
        <div class="mt-3 flex flex-wrap items-end gap-2">
          <BaseInput
            v-model="rerunSecret"
            class="min-w-64 flex-1"
            type="password"
            autocomplete="off"
            autofocus
            :label="SECRET_LABEL[rerunning.source as Source] ?? 'Token'"
            :required="rerunning.source !== 'github'"
          />
          <BaseButton type="submit" variant="primary" :loading="busy">Run</BaseButton>
          <BaseButton variant="ghost" @click="rerunning = null">Cancel</BaseButton>
        </div>
      </form>

      <!-- History -->
      <section v-if="history.length > 0" aria-labelledby="import-history" class="rounded-lg border border-border p-4">
        <h2 id="import-history" class="font-medium">History</h2>
        <ul class="mt-2 divide-y divide-border border-t border-border">
          <li v-for="item in history" :key="item.id" class="flex flex-wrap items-center gap-x-3 gap-y-0.5 py-2">
            <span class="min-w-0 flex-1">
              <span class="font-medium">{{ item.label }}</span>
              <span class="ml-2 text-xs" :class="STATUS[item.status].class">{{ STATUS[item.status].label }}</span>
              <span class="block truncate text-xs text-muted">{{ summary(item.counts) }} · {{ timeAgo(item.createdAt) }}</span>
            </span>
            <BaseButton v-if="canRerun(item)" size="sm" variant="ghost" :disabled="busy" :aria-label="`Re-run ${item.label}`" @click="rerun(item)">
              <RotateCw class="size-3.5" aria-hidden="true" /> Re-run
            </BaseButton>
            <BaseButton v-if="item.status === 'done' || item.status === 'failed'" size="sm" variant="ghost" @click="undo(item)">
              <Undo2 class="size-3.5" aria-hidden="true" /> Undo
            </BaseButton>
          </li>
        </ul>
      </section>
    </template>
  </SettingsPage>
</template>
