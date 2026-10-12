<script setup lang="ts">
import { computed, ref, watch } from 'vue'
import { RouterLink, useRoute } from 'vue-router'

import { api, errorMessage, unwrap } from '@/api/client'
import type { components } from '@/api/schema'
import { useIssueSelect } from '@/composables/useIssueSelect'
import { formatDays } from '@/lib/forecast'
import { issueIdentifier } from '@/lib/issues'
import { useSyncStore } from '@/stores/sync'

type TeamInsights = components['schemas']['TeamInsights']

/**
 * How a team's work has been flowing. Everything here is about the team as a
 * whole: there are deliberately no numbers per person.
 */
const route = useRoute()
const sync = useSyncStore()
const { issueRoute } = useIssueSelect()

const team = computed(() => sync.teamByKey(String(route.params.teamKey)))
const insights = ref<TeamInsights | null>(null)
const error = ref('')

watch(
  () => team.value?.id,
  async (teamId) => {
    insights.value = null
    error.value = ''
    if (!teamId) return
    try {
      insights.value = await unwrap(api.GET('/teams/{teamId}/insights', { params: { path: { teamId } } }))
    } catch (err) {
      error.value = errorMessage(err)
    }
  },
  { immediate: true },
)

const shortDate = (date: string) => {
  const [, month, day] = date.split('-').map(Number)
  return `${month}/${day}`
}

// --- Throughput: bars per week, completed against created.
const throughput = computed(() => {
  const weeks = insights.value?.weeks ?? []
  const max = Math.max(1, ...weeks.map((w) => Math.max(w.completed, w.created)))
  // The current week isn't over; it is drawn but left out of the average.
  const full = weeks.slice(0, -1)
  const average = full.length > 0 ? full.reduce((sum, w) => sum + w.completed, 0) / full.length : 0
  return { weeks, max, average }
})

// --- Cumulative flow: stacked areas, bottom to top.
const FLOW = [
  { key: 'completed', label: 'Done', color: 'var(--accent)' },
  { key: 'started', label: 'In progress', color: 'var(--warning)' },
  { key: 'unstarted', label: 'Todo', color: 'var(--fg-muted)' },
  { key: 'backlog', label: 'Backlog', color: 'var(--fg-faint)' },
] as const
const FLOW_WIDTH = 600
const FLOW_HEIGHT = 160

const flow = computed(() => {
  const points = insights.value?.flow ?? []
  if (points.length < 2) return null
  const totals = points.map((p) => p.completed + p.started + p.unstarted + p.backlog)
  const max = Math.max(1, ...totals)
  const x = (i: number) => (i / (points.length - 1)) * FLOW_WIDTH
  const y = (value: number) => FLOW_HEIGHT - (value / max) * FLOW_HEIGHT
  const below = points.map(() => 0)
  const areas = FLOW.map((band) => {
    const lower = [...below]
    points.forEach((p, i) => (below[i] = below[i]! + p[band.key]))
    const top = below.map((value, i) => `${x(i).toFixed(1)},${y(value).toFixed(1)}`)
    const bottom = lower.map((value, i) => `${x(i).toFixed(1)},${y(value).toFixed(1)}`).reverse()
    return { ...band, points: [...top, ...bottom].join(' '), now: points[points.length - 1]![band.key] }
  })
  return { areas, first: points[0]!.date, last: points[points.length - 1]!.date, max }
})

// --- Work in progress, oldest first. From the local copy, so it is live.
const DAY_MS = 86_400_000
const inProgress = computed(() => {
  if (!team.value) return []
  const now = Date.now()
  return sync
    .issuesForTeam(team.value.id)
    .filter((issue) => sync.state(issue.stateId)?.type === 'started')
    .map((issue) => ({ issue, days: (now - Date.parse(issue.startedAt ?? issue.updatedAt)) / DAY_MS }))
    .sort((a, b) => b.days - a.days)
})
// Older than most work took to finish: likely stuck.
const slow = computed(() => insights.value?.cycleTime.p85 ?? Infinity)
</script>

<template>
  <template v-if="team">
    <header class="flex h-11 shrink-0 items-center gap-2 border-b border-border px-5">
      <span class="font-medium">{{ team.name }}</span>
      <span class="text-faint" aria-hidden="true">›</span>
      <h1 class="text-muted">Insights</h1>
      <span class="ml-auto text-xs text-faint">the team as a whole, last 12 weeks</span>
    </header>

    <div class="flex-1 overflow-y-auto">
      <p v-if="error" role="alert" class="px-5 py-3 text-danger">{{ error }}</p>
      <p v-else-if="!insights" class="px-5 py-3 text-muted" role="status">Loading…</p>

      <div v-else class="mx-auto grid max-w-5xl gap-4 p-5 lg:grid-cols-2">
        <section aria-labelledby="throughput-heading" class="border border-border p-4">
          <h2 id="throughput-heading" class="font-medium">Throughput</h2>
          <p class="mt-0.5 text-xs text-muted">
            Completed vs created per week. Average <span class="text-fg">{{ throughput.average.toFixed(1) }}</span> completed.
          </p>
          <ol class="mt-4 flex h-36 items-end gap-1.5" aria-label="Issues completed and created per week">
            <li
              v-for="(w, index) in throughput.weeks"
              :key="w.start"
              class="flex h-full flex-1 flex-col justify-end"
              :title="`Week of ${w.start}: ${w.completed} completed, ${w.created} created${index === throughput.weeks.length - 1 ? ' (so far)' : ''}`"
            >
              <span class="flex h-full items-end gap-px">
                <span class="flex-1 bg-accent" :class="{ 'opacity-50': index === throughput.weeks.length - 1 }" :style="{ height: `${(w.completed / throughput.max) * 100}%` }" />
                <span class="flex-1 bg-border-strong" :style="{ height: `${(w.created / throughput.max) * 100}%` }" />
              </span>
              <span class="mt-1 text-center text-[10px] text-faint tabular-nums">{{ shortDate(w.start) }}</span>
            </li>
          </ol>
        </section>

        <section aria-labelledby="cycle-time-heading" class="border border-border p-4">
          <h2 id="cycle-time-heading" class="font-medium">Cycle time</h2>
          <p class="mt-0.5 text-xs text-muted">Started to completed, {{ insights.cycleTime.samples }} {{ insights.cycleTime.samples === 1 ? 'issue' : 'issues' }}.</p>
          <dl v-if="insights.cycleTime.samples > 0" class="mt-4 grid grid-cols-3 gap-3">
            <div v-for="row in [
              { label: '50%', value: insights.cycleTime.p50 },
              { label: '85%', value: insights.cycleTime.p85 },
              { label: '95%', value: insights.cycleTime.p95 ?? 0 },
            ]" :key="row.label">
              <dt class="text-xs text-faint">{{ row.label }}</dt>
              <dd class="text-2xl font-semibold tabular-nums">{{ formatDays(row.value) }}</dd>
            </div>
          </dl>
          <p v-else class="mt-4 text-muted">Nothing completed yet.</p>
          <p v-if="insights.leadTime.samples > 0" class="mt-4 text-xs text-muted">
            Created to completed: 50% <span class="text-fg">{{ formatDays(insights.leadTime.p50) }}</span>, 85%
            <span class="text-fg">{{ formatDays(insights.leadTime.p85) }}</span>.
          </p>
          <p v-if="insights.calibration.windows > 0" class="mt-2 text-xs text-muted">
            Forecast accuracy, last six months:
            <span class="text-fg">{{ insights.calibration.held }}/{{ insights.calibration.windows }}</span> held.
          </p>
        </section>

        <section aria-labelledby="flow-heading" class="border border-border p-4 lg:col-span-2">
          <h2 id="flow-heading" class="font-medium">Cumulative flow</h2>
          <template v-if="flow">
            <svg :viewBox="`0 0 ${FLOW_WIDTH} ${FLOW_HEIGHT}`" preserveAspectRatio="none" class="mt-4 h-40 w-full" role="img" aria-label="Cumulative flow diagram">
              <polygon v-for="area in flow.areas" :key="area.key" :points="area.points" :fill="area.color" fill-opacity="0.55" :stroke="area.color" stroke-width="1" vector-effect="non-scaling-stroke" />
            </svg>
            <div class="mt-1 flex justify-between text-[10px] text-faint tabular-nums">
              <span>{{ flow.first }}</span>
              <span>{{ flow.last }}</span>
            </div>
            <ul class="mt-2 flex flex-wrap gap-x-4 gap-y-1 text-xs text-muted">
              <li v-for="area in [...flow.areas].reverse()" :key="area.key" class="flex items-center gap-1.5">
                <span class="size-2" :style="{ background: area.color }" aria-hidden="true" /> {{ area.label }}
                <span class="text-fg tabular-nums">{{ area.now }}</span>
              </li>
            </ul>
          </template>
        </section>

        <section aria-labelledby="wip-heading" class="border border-border p-4 lg:col-span-2">
          <h2 id="wip-heading" class="font-medium">In progress now <span class="font-normal text-faint">{{ inProgress.length }}</span></h2>
          <ul v-if="inProgress.length > 0" class="mt-3 divide-y divide-border border-y border-border">
            <li v-for="{ issue, days } in inProgress.slice(0, 12)" :key="issue.id" class="flex h-9 items-center gap-3">
              <span class="w-20 shrink-0 text-muted tabular-nums">{{ issueIdentifier(issue, team) }}</span>
              <RouterLink :to="issueRoute(issue)" class="min-w-0 flex-1 truncate hover:underline">{{ issue.title }}</RouterLink>
              <span class="shrink-0 text-xs tabular-nums" :class="days > slow ? 'text-warning' : 'text-faint'">{{ formatDays(days) }}</span>
            </li>
          </ul>
          <p v-if="inProgress.length > 12" class="mt-2 text-xs text-faint">and {{ inProgress.length - 12 }} more</p>
          <p v-else-if="inProgress.length === 0" class="mt-3 text-muted">Nothing is in progress.</p>
        </section>
      </div>
    </div>
  </template>
  <p v-else class="p-8 text-center text-muted">Team not found.</p>
</template>
