<script setup lang="ts">
import {
  Archive,
  CircleDot,
  CircleUser,
  Columns3,
  Copy,
  GitBranch,
  Inbox,
  Keyboard,
  Link,
  List,
  LogOut,
  Plus,
  Search,
  Settings,
  Square,
  Tag,
  Timer,
} from 'lucide-vue-next'
import { DialogContent, DialogDescription, DialogOverlay, DialogPortal, DialogRoot, DialogTitle } from 'reka-ui'
import { type Component, computed, h, nextTick, ref, useId, useTemplateRef, watch } from 'vue'
import { useRoute, useRouter } from 'vue-router'

import { useIssueActions } from '@/composables/useIssueActions'
import { stateKey } from '@/lib/filter'
import { fuzzyFilter } from '@/lib/fuzzy'
import { PRIORITIES } from '@/lib/issues'
import { useIssueViewStore } from '@/stores/issueView'
import { type PaletteStep, usePaletteStore } from '@/stores/palette'
import { useSessionStore } from '@/stores/session'
import { useSyncStore } from '@/stores/sync'
import { useUiStore } from '@/stores/ui'
import { useViewOptionsStore } from '@/stores/viewOptions'
import { useWorkspaceStore } from '@/stores/workspace'

import PriorityIcon from './PriorityIcon.vue'
import StateIcon from './StateIcon.vue'
import UserAvatar from './UserAvatar.vue'

interface Command {
  id: string
  label: string
  /** Section heading in the unfiltered list. */
  group: string
  icon?: Component
  /** Keys shown on the right, e.g. ['G', 'M']. */
  keys?: string[]
  /** Shown as checked, for commands that toggle. */
  checked?: boolean
  /** Extra text to match against. */
  keywords?: string
  /** Return 'stay' to keep the palette open. */
  run(): void | 'stay'
}

const route = useRoute()
const router = useRouter()
const palette = usePaletteStore()
const view = useIssueViewStore()
const sync = useSyncStore()
const session = useSessionStore()
const workspace = useWorkspaceStore()
const ui = useUiStore()
const viewOptions = useViewOptionsStore()
const actions = useIssueActions()

const query = ref('')
const active = ref(0)
const input = useTemplateRef('input')
const list = useTemplateRef('list')
const listId = useId()

// Fixed when the palette opens, so hovering other rows can't change it.
const targets = ref<string[]>([])
const targetIssues = computed(() => actions.issuesOf(targets.value))
const targetSummary = computed(() => {
  const issues = targetIssues.value
  if (issues.length === 0) return ''
  return issues.length === 1 ? actions.identifier(issues[0]!) : `${issues.length} issues`
})

const icon = (component: Component, props: Record<string, unknown> = {}) => () => h(component, props)
const lucide = (component: Component) => icon(component, { class: 'size-4 text-muted', 'aria-hidden': 'true' })

function go(name: string, params: Record<string, string> = {}) {
  void router.push({ name, params })
}

function goToStep(step: PaletteStep): 'stay' {
  palette.step = step
  query.value = ''
  active.value = 0
  return 'stay'
}

async function logout() {
  await session.logout()
  await workspace.close(true)
  await router.push({ name: 'home' })
}

const rootCommands = computed<Command[]>(() => {
  const commands: Command[] = []
  const ids = targets.value

  if (ids.length > 0) {
    const group = targetSummary.value
    commands.push(
      { id: 'status', label: 'Change status…', group, icon: lucide(CircleDot), keys: ['S'], run: () => goToStep('status') },
      { id: 'priority', label: 'Set priority…', group, icon: icon(PriorityIcon, { priority: 2, class: 'text-muted' }), keys: ['P'], run: () => goToStep('priority') },
      { id: 'assignee', label: 'Assign to…', group, icon: lucide(CircleUser), keys: ['A'], run: () => goToStep('assignee') },
      { id: 'labels', label: 'Add or remove labels…', group, icon: lucide(Tag), keys: ['L'], run: () => goToStep('labels') },
      { id: 'copy-id', label: ids.length === 1 ? 'Copy issue ID' : 'Copy issue IDs', group, icon: lucide(Copy), run: () => void actions.copyIds(ids) },
    )
    if (ids.length === 1) {
      const issue = targetIssues.value[0]
      if (issue) {
        const path = router.resolve({ name: 'issue', params: { issueRef: issue.number > 0 ? actions.identifier(issue) : issue.id } }).href
        commands.push({ id: 'copy-link', label: 'Copy issue link', group, icon: lucide(Link), run: () => void actions.copyLink(path) })
        if (issue.number > 0) {
          commands.push({ id: 'copy-branch', label: 'Copy git branch name', group, icon: lucide(GitBranch), run: () => void actions.copyBranch(issue) })
          if (sync.runningTimer?.issueId !== issue.id) {
            commands.push({ id: 'start-timer', label: 'Start timer', group, icon: lucide(Timer), run: () => sync.startTimer(issue.id) })
          }
        }
      }
    }
    commands.push({
      id: 'archive',
      label: ids.length === 1 ? 'Archive issue' : `Archive ${ids.length} issues`,
      group,
      icon: lucide(Archive),
      keys: ['⌘', '⌫'],
      run: () => {
        actions.archive(ids)
        view.clearSelection()
        if (route.name === 'issue') go('workspace')
      },
    })
  }

  if (sync.runningTimer) {
    commands.push({ id: 'stop-timer', label: 'Stop timer', group: 'Time', icon: lucide(Square), run: () => sync.stopTimer() })
  }
  commands.push({ id: 'new-issue', label: 'New issue', group: 'Issues', icon: lucide(Plus), keys: ['C'], run: () => ui.openNewIssue() })

  const options = viewOptions.current
  if (options) {
    const toBoard = options.layout === 'list'
    commands.push({
      id: 'layout',
      label: toBoard ? 'Switch to board view' : 'Switch to list view',
      group: 'View',
      icon: lucide(toBoard ? Columns3 : List),
      keys: ['⌘', 'B'],
      run: () => {
        options.layout = toBoard ? 'board' : 'list'
      },
    })
    commands.push({
      id: 'closed',
      label: options.showClosed ? 'Hide completed issues' : 'Show completed issues',
      group: 'View',
      icon: lucide(CircleDot),
      run: () => {
        options.showClosed = !options.showClosed
      },
    })
  }

  commands.push({ id: 'go-search', label: 'Search descriptions and comments', group: 'Navigation', icon: lucide(Search), keys: ['/'], run: () => go('search') })
  commands.push({ id: 'go-inbox', label: 'Go to Inbox', group: 'Navigation', icon: lucide(Inbox), keys: ['G', 'I'], run: () => go('inbox') })
  commands.push({ id: 'go-projects', label: 'Go to Projects', group: 'Navigation', icon: lucide(Columns3), run: () => go('projects') })
  commands.push({ id: 'go-roadmap', label: 'Go to Roadmap', group: 'Navigation', icon: lucide(Columns3), run: () => go('roadmap') })
  for (const saved of sync.views) {
    commands.push({ id: `view-${saved.id}`, label: `Go to view: ${saved.name}`, group: 'Navigation', icon: lucide(List), run: () => go('view', { viewId: saved.id }) })
  }
  commands.push({ id: 'go-my', label: 'Go to My issues', group: 'Navigation', icon: lucide(CircleUser), keys: ['G', 'M'], run: () => go('my-issues') })
  for (const team of workspace.myTeams) {
    commands.push({
      id: `go-team-${team.id}`,
      label: `Go to ${team.name} issues`,
      group: 'Navigation',
      icon: lucide(CircleDot),
      keywords: team.key,
      run: () => go('team-issues', { teamKey: team.key }),
    })
  }
  commands.push(
    { id: 'go-general', label: 'Go to settings: General', group: 'Navigation', icon: lucide(Settings), keywords: 'workspace rename leave', run: () => go('settings-workspace') },
    { id: 'go-members', label: 'Go to settings: Members', group: 'Navigation', icon: lucide(Settings), keys: ['G', 'S'], run: () => go('settings-members') },
    { id: 'go-teams', label: 'Go to settings: Teams', group: 'Navigation', icon: lucide(Settings), run: () => go('settings-teams') },
    { id: 'go-labels', label: 'Go to settings: Labels', group: 'Navigation', icon: lucide(Settings), run: () => go('settings-labels') },
    { id: 'go-api', label: 'Go to settings: API keys and webhooks', group: 'Navigation', icon: lucide(Settings), run: () => go('settings-api') },
    { id: 'go-integrations', label: 'Go to settings: Integrations', group: 'Navigation', icon: lucide(Settings), keywords: 'github', run: () => go('settings-integrations') },
    { id: 'go-account', label: 'Go to settings: Account', group: 'Navigation', icon: lucide(Settings), keywords: 'profile email notifications', run: () => go('settings-account') },
    { id: 'go-themes', label: 'Go to settings: Themes', group: 'Navigation', icon: lucide(Settings), keywords: 'appearance colors dark light mode', run: () => go('settings-themes') },
  )
  for (const other of session.workspaces) {
    if (other.id === workspace.current?.id) continue
    commands.push({
      id: `ws-${other.id}`,
      label: `Switch to ${other.name}`,
      group: 'Navigation',
      icon: lucide(Settings),
      run: () => go('workspace', { workspaceKey: other.urlKey }),
    })
  }

  commands.push(
    { id: 'shortcuts', label: 'Keyboard shortcuts', group: 'Help', icon: lucide(Keyboard), keys: ['?'], run: () => void (ui.shortcutsOpen = true) },
    { id: 'logout', label: 'Log out', group: 'Help', icon: lucide(LogOut), run: () => void logout() },
  )
  return commands
})

/** Issues whose title or ID matches the query. */
const issueResults = computed<Command[]>(() => {
  if (!query.value.trim()) return []
  const searchable = sync.issues.map((issue) => ({ issue, id: actions.identifier(issue) }))
  return fuzzyFilter(searchable, query.value, (entry) => `${entry.id} ${entry.issue.title}`)
    .slice(0, 8)
    .map(({ issue, id }) => {
      const state = sync.state(issue.stateId)
      return {
        id: `issue-${issue.id}`,
        label: `${id}  ${issue.title}`,
        group: 'Issues',
        icon: state ? icon(StateIcon, { type: state.type, color: state.color }) : undefined,
        run: () => go('issue', { issueRef: issue.number > 0 ? id : issue.id }),
      }
    })
})

/** Hands the query to full-text search, which also reads descriptions and comments. */
const fullTextSearch = computed<Command[]>(() => {
  const q = query.value.trim()
  if (!q) return []
  return [
    {
      id: 'full-text',
      label: `Search descriptions and comments for “${q}”`,
      group: 'Issues',
      icon: lucide(Search),
      run: () => void router.push({ name: 'search', query: { q } }),
    },
  ]
})

const stepCommands = computed<Command[]>(() => {
  const ids = targets.value
  const issues = targetIssues.value
  switch (palette.step) {
    case 'status':
      return actions.statusOptions(ids).map((state) => ({
        id: state.id,
        label: state.name,
        group: '',
        icon: icon(StateIcon, { type: state.type, color: state.color }),
        checked: issues.length > 0 && issues.every((i) => {
          const current = sync.state(i.stateId)
          return current !== undefined && stateKey(current) === stateKey(state)
        }),
        run: () => actions.setStatus(ids, stateKey(state)),
      }))
    case 'priority':
      return PRIORITIES.map((p) => ({
        id: String(p.value),
        label: p.label,
        group: '',
        icon: icon(PriorityIcon, { priority: p.value, class: 'text-muted' }),
        checked: issues.length > 0 && issues.every((i) => i.priority === p.value),
        run: () => actions.setPriority(ids, p.value),
      }))
    case 'assignee':
      return [
        {
          id: 'none',
          label: 'No assignee',
          group: '',
          icon: lucide(CircleUser),
          checked: issues.length > 0 && issues.every((i) => !i.assigneeId),
          run: () => actions.setAssignee(ids, null),
        },
        ...sync.users.map((user) => ({
          id: user.id,
          label: user.name || user.email,
          group: '',
          keywords: user.email,
          icon: icon(UserAvatar, { name: user.name, email: user.email, avatarUrl: user.avatarUrl, size: 18 }),
          checked: issues.length > 0 && issues.every((i) => i.assigneeId === user.id),
          run: () => actions.setAssignee(ids, user.id),
        })),
      ]
    case 'labels':
      return sync.labels.map((label) => ({
        id: label.id,
        label: label.name,
        group: '',
        icon: () => h('span', { class: 'size-2.5 rounded-full', style: { background: label.color } }),
        checked: actions.labelState(ids, label.id) === 'all',
        // Labels are toggled one after another, so stay open.
        run: () => {
          actions.toggleLabel(ids, label.id)
          return 'stay' as const
        },
      }))
    default:
      return []
  }
})

const results = computed<Command[]>(() => {
  if (palette.step !== 'root') return fuzzyFilter(stepCommands.value, query.value, (c) => `${c.label} ${c.keywords ?? ''}`)
  const commands = fuzzyFilter(rootCommands.value, query.value, (c) => `${c.label} ${c.keywords ?? ''}`)
  return [...commands, ...issueResults.value, ...fullTextSearch.value]
})

const placeholder = computed(
  () =>
    ({
      root: 'Type a command or search issues…',
      status: 'Change status to…',
      priority: 'Set priority to…',
      assignee: 'Assign to…',
      labels: 'Add or remove labels…',
    })[palette.step],
)

// Headings only make sense while the list is in its natural order.
const showGroups = computed(() => palette.step === 'root' && !query.value.trim())

watch(
  () => palette.open,
  (open) => {
    if (!open) return
    targets.value = [...view.targets]
    query.value = ''
    active.value = 0
  },
)
watch(query, () => (active.value = 0))
watch(results, (r) => {
  if (active.value >= r.length) active.value = Math.max(0, r.length - 1)
})

function run(command: Command | undefined) {
  if (!command) return
  if (command.run() !== 'stay') palette.open = false
}

async function move(delta: number) {
  const count = results.value.length
  if (count === 0) return
  active.value = (active.value + delta + count) % count
  await nextTick()
  list.value?.querySelector('[aria-selected="true"]')?.scrollIntoView({ block: 'nearest' })
}

function onKeydown(event: KeyboardEvent) {
  if (event.key === 'ArrowDown') {
    event.preventDefault()
    void move(1)
  } else if (event.key === 'ArrowUp') {
    event.preventDefault()
    void move(-1)
  } else if (event.key === 'Enter') {
    event.preventDefault()
    run(results.value[active.value])
  } else if (event.key === 'Backspace' && query.value === '' && palette.step !== 'root') {
    // Back out of a value picker to the command list.
    event.preventDefault()
    goToStep('root')
  }
}
</script>

<template>
  <DialogRoot v-model:open="palette.open">
    <DialogPortal>
      <DialogOverlay class="fixed inset-0 z-40 bg-black/50" />
      <DialogContent
        class="fixed top-[14%] left-1/2 z-50 flex max-h-[60vh] w-[calc(100%-2rem)] max-w-xl -translate-x-1/2 flex-col overflow-hidden rounded-xl border border-border-strong bg-elevated shadow-2xl"
        @open-auto-focus.prevent="input?.focus()"
      >
        <DialogTitle class="sr-only">Command menu</DialogTitle>
        <DialogDescription class="sr-only">
          Search for a command or an issue. Use the arrow keys to move and Enter to choose.
        </DialogDescription>

        <div v-if="targetSummary" class="px-4 pt-3">
          <span class="rounded bg-active px-1.5 py-0.5 text-xs text-muted">{{ targetSummary }}</span>
        </div>
        <input
          ref="input"
          v-model="query"
          role="combobox"
          aria-expanded="true"
          aria-autocomplete="list"
          :aria-controls="listId"
          :aria-activedescendant="results[active] ? `${listId}-${results[active]!.id}` : undefined"
          :aria-label="placeholder"
          :placeholder="placeholder"
          class="h-12 shrink-0 border-b border-border bg-transparent px-4 text-[15px] text-fg outline-none placeholder:text-faint"
          @keydown="onKeydown"
        />

        <ul :id="listId" ref="list" role="listbox" aria-label="Commands" class="min-h-0 flex-1 overflow-y-auto p-1.5">
          <template v-for="(command, index) in results" :key="command.id">
            <li
              v-if="showGroups && command.group && command.group !== results[index - 1]?.group"
              role="presentation"
              class="px-2.5 pt-2 pb-1 text-xs text-faint"
            >
              {{ command.group }}
            </li>
            <li
              :id="`${listId}-${command.id}`"
              role="option"
              :aria-selected="index === active"
              class="flex h-9 cursor-pointer items-center gap-2.5 rounded-md px-2.5 text-[13px] aria-selected:bg-active"
              @click="run(command)"
              @mousemove="active = index"
            >
              <span class="flex size-5 shrink-0 items-center justify-center">
                <component :is="command.icon" v-if="command.icon" />
              </span>
              <span class="min-w-0 flex-1 truncate whitespace-pre">{{ command.label }}</span>
              <span v-if="command.checked" class="text-xs text-muted">Current</span>
              <span v-if="command.keys" class="flex shrink-0 gap-1" aria-hidden="true">
                <kbd
                  v-for="key in command.keys"
                  :key="key"
                  class="flex h-5 min-w-5 items-center justify-center rounded border border-border-strong px-1 text-[11px] text-muted"
                >
                  {{ key }}
                </kbd>
              </span>
            </li>
          </template>
          <li v-if="results.length === 0" role="presentation" class="px-2.5 py-6 text-center text-muted">
            No results
          </li>
        </ul>
      </DialogContent>
    </DialogPortal>
  </DialogRoot>
</template>
