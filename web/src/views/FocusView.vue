<script setup lang="ts">
import { Play, Plus, Search, Square } from 'lucide-vue-next'
import { computed } from 'vue'
import { RouterLink, useRouter } from 'vue-router'

import ForecastLine from '@/components/ForecastLine.vue'
import PriorityIcon from '@/components/PriorityIcon.vue'
import StateIcon from '@/components/StateIcon.vue'
import { useIssueSelect } from '@/composables/useIssueSelect'
import { describeNotification } from '@/lib/activity'
import { isClosed } from '@/lib/filter'
import { buildFocus, type FocusItem } from '@/lib/focus'
import { formatDueDate, issueIdentifier, progressOf, todayUTC } from '@/lib/issues'
import { timeAgo } from '@/lib/time'
import { usePaletteStore } from '@/stores/palette'
import { useSyncStore } from '@/stores/sync'
import { useUiStore } from '@/stores/ui'
import { useWorkspaceStore } from '@/stores/workspace'
import type { Issue, Notification } from '@/sync/types'

/**
 * Where the app opens for someone with work assigned: the one thing to do
 * now, how the cycle is going, what is new in the inbox, and below them the
 * rest of your work in the order to do it. Nobody else sees this page.
 */
const router = useRouter()
const sync = useSyncStore()
const ui = useUiStore()
const palette = usePaletteStore()
const workspace = useWorkspaceStore()
const { issueRoute } = useIssueSelect()

const focus = computed(() => {
  const currentCycles = new Set<string>()
  for (const team of sync.teams) {
    const cycle = sync.currentCycle(team.id)
    if (cycle) currentCycles.add(cycle.id)
  }
  return buildFocus(sync.myIssues, {
    stateType: (id) => sync.state(id)?.type,
    issue: sync.issue,
    relations: sync.relations,
    today: todayUTC(),
    currentCycles,
  })
})

const ref = (issue: Issue) => issueIdentifier(issue, sync.team(issue.teamId))
const who = (issue: Issue) => {
  const user = sync.user(issue.assigneeId)
  return user ? ` (${user.name || user.email})` : ''
}

/** Why an item is where it is, in a few words. */
function reasons(item: FocusItem): { text: string; class: string }[] {
  const out: { text: string; class: string }[] = []
  if (item.blocks.length > 0) out.push({ text: `unblocks ${item.blocks.map((i) => ref(i) + who(i)).join(', ')}`, class: 'text-accent' })
  if (item.blockedBy.length > 0) out.push({ text: `waits on ${item.blockedBy.map((i) => ref(i) + who(i)).join(', ')}`, class: 'text-warning' })
  if (item.issue.dueDate) out.push({ text: `due ${formatDueDate(item.issue.dueDate).toLowerCase()}`, class: item.overdue ? 'text-danger' : 'text-muted' })
  return out
}

/** Moves an issue to its team's first in-progress status. */
function start(issue: Issue) {
  const started = sync.statesForTeam(issue.teamId).find((s) => s.type === 'started')
  if (started) sync.updateIssue(issue.id, { stateId: started.id })
}

// The one thing to do now: what you have started, or else the top of the queue.
const top = computed(() => focus.value.inProgress[0] ?? focus.value.next[0])
const topStarted = computed(() => focus.value.inProgress.length > 0)
const timing = computed(() => !!top.value && sync.runningTimer?.issueId === top.value.issue.id)

// The current cycle of the first of your teams that has one.
const cycleTeam = computed(() => workspace.myTeams.find((team) => sync.currentCycle(team.id)))
const cycle = computed(() => (cycleTeam.value ? sync.currentCycle(cycleTeam.value.id) : undefined))
const cycleIssues = computed(() => sync.issues.filter((i) => i.cycleId === cycle.value?.id))
const cycleProgress = computed(() => progressOf(cycleIssues.value, sync.state))
const cycleRemaining = computed(() => cycleIssues.value.filter((i) => !isClosed(i, sync.state)).length)
const daysLeft = computed(() => {
  if (!cycle.value) return 0
  return Math.max(0, Math.round((Date.parse(cycle.value.endsAt) - Date.parse(todayUTC())) / 86_400_000) + 1)
})
// The ring is a circle of radius 40: this is how much of it to leave undrawn.
const RING = 2 * Math.PI * 40
const ringGap = computed(() => RING * (1 - cycleProgress.value.percent / 100))

const latest = computed(() => sync.notifications.filter((n) => sync.issue(n.issueId)).slice(0, 4))

function openNotification(notification: Notification) {
  const issue = sync.issue(notification.issueId)
  if (!issue) return
  sync.markNotification(notification.id, true)
  void router.push({ name: 'issue', params: { issueRef: issue.number > 0 ? ref(issue) : issue.id } })
}

// A list this long is not something to read; show the top of it.
const SHOWN = 25

const sections = computed(() => [
  { id: 'now', title: 'In progress', dot: 'bg-warning', items: focus.value.inProgress, empty: 'Nothing started.', startable: false },
  { id: 'next', title: 'Up next', dot: 'bg-accent', items: focus.value.next, empty: 'Nothing queued.', startable: true },
  { id: 'waiting', title: 'Waiting on others', dot: 'bg-faint', items: focus.value.waiting, empty: '', startable: false },
])

const card = 'rounded-xl border border-border bg-elevated p-5 transition-[transform,border-color] duration-200 hover:-translate-y-0.5 hover:border-border-strong'
const eyebrow = 'text-[11px] font-semibold tracking-[0.08em] text-faint uppercase'
</script>

<template>
  <div class="flex-1 overflow-y-auto">
    <div class="mx-auto flex max-w-[1240px] flex-col gap-5 px-6 pt-6 pb-16">
      <header class="flex flex-wrap items-center gap-3">
        <div class="mr-auto">
          <h1 class="text-2xl font-semibold tracking-tight">Focus</h1>
          <p v-if="cycleTeam && cycle" class="text-muted">{{ cycleTeam.name }} · {{ sync.cycleName(cycle) }}</p>
        </div>
        <button
          type="button"
          class="hidden h-9 w-72 items-center gap-2.5 rounded-full border border-border-strong bg-elevated pr-2 pl-3.5 text-faint transition hover:border-accent sm:flex"
          @click="palette.show()"
        >
          <Search class="size-4" aria-hidden="true" /> Search
          <kbd class="ml-auto rounded-sm bg-hover px-1.5 py-0.5 text-[11px] text-muted">⌘K</kbd>
        </button>
        <button
          v-if="workspace.teams.length > 0"
          type="button"
          class="flex h-9 items-center gap-1.5 rounded-full bg-accent pr-4 pl-3 font-semibold text-accent-fg transition hover:bg-accent-hover"
          @click="ui.openNewIssue()"
        >
          <Plus class="size-4" aria-hidden="true" /> New issue
        </button>
        <RouterLink :to="{ name: 'my-issues' }" class="text-xs text-muted hover:text-fg">All ({{ sync.myIssues.length }})</RouterLink>
      </header>

      <div class="grid gap-3.5 lg:grid-cols-[1.4fr_1fr_1fr]">
        <section aria-labelledby="focus-top" :class="card" class="next">
          <h2 id="focus-top" :class="eyebrow">{{ topStarted ? 'Now' : 'Up next' }}</h2>
          <template v-if="top">
            <RouterLink :to="issueRoute(top.issue)" class="mt-2.5 block text-[22px] leading-tight font-semibold tracking-tight hover:underline">
              {{ top.issue.title }}
            </RouterLink>
            <p class="mt-1.5 flex flex-wrap items-center gap-x-2 text-muted">
              <span class="tabular-nums">{{ ref(top.issue) }}</span>
              <span aria-hidden="true">·</span>
              <span>{{ sync.state(top.issue.stateId)?.name }}</span>
              <template v-for="reason in reasons(top)" :key="reason.text">
                <span aria-hidden="true">·</span><span :class="reason.class">{{ reason.text }}</span>
              </template>
            </p>
            <div class="mt-5 flex flex-wrap gap-2">
              <button
                v-if="!topStarted"
                type="button"
                class="flex h-9 items-center gap-2 rounded-full bg-fg pr-4 pl-3 font-semibold text-bg"
                :aria-label="`Start ${top.issue.title}`"
                @click="start(top.issue)"
              >
                <Play class="size-3.5 fill-current" aria-hidden="true" /> Start
              </button>
              <button
                v-else-if="timing"
                type="button"
                class="flex h-9 items-center gap-2 rounded-full bg-fg pr-4 pl-3 font-semibold text-bg"
                @click="sync.stopTimer()"
              >
                <Square class="size-3 fill-current" aria-hidden="true" /> Stop timer
              </button>
              <button
                v-else
                type="button"
                class="flex h-9 items-center gap-2 rounded-full bg-fg pr-4 pl-3 font-semibold text-bg"
                @click="sync.startTimer(top.issue.id)"
              >
                <Play class="size-3.5 fill-current" aria-hidden="true" /> Start timer
              </button>
              <RouterLink
                :to="issueRoute(top.issue)"
                class="flex h-9 items-center rounded-full border border-border-strong px-4 text-muted hover:text-fg"
              >
                Open
              </RouterLink>
            </div>
          </template>
          <p v-else class="mt-2.5 text-muted">Nothing queued.</p>
        </section>

        <section v-if="cycleTeam && cycle" aria-labelledby="focus-cycle" :class="card">
          <h2 id="focus-cycle" :class="eyebrow">
            <RouterLink :to="{ name: 'cycle', params: { teamKey: cycleTeam.key, number: cycle.number } }" class="hover:text-fg">
              {{ sync.cycleName(cycle) }}
            </RouterLink>
          </h2>
          <div class="mt-3 flex items-center gap-4">
            <svg viewBox="0 0 100 100" class="size-[88px] shrink-0 -rotate-90" aria-hidden="true">
              <circle cx="50" cy="50" r="40" fill="none" stroke="var(--bg-hover)" stroke-width="9" />
              <circle
                cx="50"
                cy="50"
                r="40"
                fill="none"
                stroke="var(--accent)"
                stroke-width="9"
                stroke-linecap="round"
                class="transition-[stroke-dashoffset] duration-700"
                :stroke-dasharray="RING"
                :stroke-dashoffset="ringGap"
              />
            </svg>
            <p>
              <span class="block text-[26px] leading-none font-semibold tracking-tight tabular-nums">{{ cycleProgress.percent }}%</span>
              <span class="mt-1 block text-xs text-muted">{{ daysLeft === 1 ? '1 day left' : `${daysLeft} days left` }}</span>
              <span class="block text-xs text-faint">{{ cycleProgress.done }} of {{ cycleProgress.total }} done</span>
            </p>
          </div>
          <div class="mt-3.5 border-t border-border pt-3">
            <ForecastLine :scope="{ cycleId: cycle.id }" :remaining="cycleRemaining" />
          </div>
        </section>

        <section aria-labelledby="focus-inbox" :class="card">
          <h2 id="focus-inbox" :class="eyebrow" class="flex items-center justify-between">
            <RouterLink :to="{ name: 'inbox' }" class="hover:text-fg">Inbox</RouterLink>
            <span v-if="sync.unreadCount > 0" class="rounded-full bg-accent px-1.5 text-[10px] tracking-normal text-accent-fg">{{ sync.unreadCount }}</span>
          </h2>
          <ul v-if="latest.length > 0" class="mt-2 divide-y divide-border">
            <li v-for="notification in latest" :key="notification.id">
              <button type="button" class="flex w-full items-center gap-2.5 py-2 text-left hover:text-fg" @click="openNotification(notification)">
                <span class="size-1.5 shrink-0 rounded-full" :class="notification.readAt ? 'bg-transparent' : 'bg-accent'" aria-hidden="true" />
                <span class="min-w-0 flex-1 truncate" :class="notification.readAt ? 'text-muted' : 'text-fg'">
                  {{ ref(sync.issue(notification.issueId)!) }} · {{ describeNotification(notification, sync.user(notification.actorId)) }}
                </span>
                <time class="shrink-0 text-xs text-faint" :datetime="notification.createdAt">{{ timeAgo(notification.createdAt) }}</time>
              </button>
            </li>
          </ul>
          <p v-else class="mt-2.5 text-muted">Inbox empty</p>
        </section>
      </div>

      <div class="grid items-start gap-3.5 md:auto-cols-fr md:grid-flow-col">
        <template v-for="section in sections" :key="section.id">
          <section
            v-if="section.items.length > 0 || section.empty"
            :aria-labelledby="`focus-${section.id}`"
            class="rounded-xl border border-border bg-elevated/50 p-3"
          >
            <h2 :id="`focus-${section.id}`" class="flex items-center gap-2 px-1 pb-3 text-xs font-semibold">
              <span class="size-2 rounded-full" :class="section.dot" aria-hidden="true" />
              {{ section.title }}
              <span v-if="section.items.length > 0" class="font-normal text-faint">{{ section.items.length }}</span>
            </h2>
            <ul v-if="section.items.length > 0" class="flex flex-col gap-2">
              <li
                v-for="item in section.items.slice(0, SHOWN)"
                :key="item.issue.id"
                class="rounded-lg border border-border bg-elevated p-3 transition-[transform,border-color] duration-150 hover:-translate-y-0.5 hover:border-accent"
              >
                <p class="flex items-center gap-2 text-xs text-muted">
                  <StateIcon
                    v-if="sync.state(item.issue.stateId)"
                    :type="sync.state(item.issue.stateId)!.type"
                    :color="sync.state(item.issue.stateId)!.color"
                    class="shrink-0"
                  />
                  <span class="tabular-nums">{{ ref(item.issue) }}</span>
                  <PriorityIcon :priority="item.issue.priority" class="ml-auto shrink-0" />
                </p>
                <RouterLink :to="issueRoute(item.issue)" class="mt-1.5 line-clamp-2 font-medium hover:underline">{{ item.issue.title }}</RouterLink>
                <p v-if="reasons(item).length > 0 || section.startable" class="mt-2 flex flex-wrap items-center gap-x-3 text-xs">
                  <span v-for="reason in reasons(item)" :key="reason.text" :class="reason.class">{{ reason.text }}</span>
                  <button
                    v-if="section.startable"
                    type="button"
                    class="ml-auto flex h-6 shrink-0 items-center gap-1.5 rounded-full border border-border-strong px-2.5 text-muted hover:border-accent hover:text-fg"
                    :aria-label="`Start ${item.issue.title}`"
                    @click="start(item.issue)"
                  >
                    <Play class="size-3" aria-hidden="true" /> Start
                  </button>
                </p>
              </li>
            </ul>
            <p v-else class="px-1 pb-1 text-muted">{{ section.empty }}</p>
            <p v-if="section.items.length > SHOWN" class="px-1 pt-2 text-xs text-faint">
              <RouterLink :to="{ name: 'my-issues' }" class="hover:text-fg">+{{ section.items.length - SHOWN }} more</RouterLink>
            </p>
            <p v-if="section.id === 'next' && focus.later > 0" class="px-1 pt-2 text-xs text-faint">
              <RouterLink :to="{ name: 'my-issues' }" class="hover:text-fg">+{{ focus.later }} more</RouterLink>
            </p>
          </section>
        </template>
      </div>
    </div>
  </div>
</template>

<style scoped>
/* The card for the one thing to do now carries the theme's glow. */
.next {
  background:
    radial-gradient(ellipse 90% 120% at 0% 0%, color-mix(in srgb, var(--accent) 16%, transparent), transparent 60%),
    var(--bg-elevated);
}
</style>
