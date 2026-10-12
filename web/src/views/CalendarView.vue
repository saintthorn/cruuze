<script setup lang="ts">
import { ChevronLeft, ChevronRight } from 'lucide-vue-next'
import { computed, ref } from 'vue'
import { RouterLink, useRoute } from 'vue-router'

import StateIcon from '@/components/StateIcon.vue'
import BaseButton from '@/components/ui/BaseButton.vue'
import { useIssueSelect } from '@/composables/useIssueSelect'
import { inMonth, monthGrid, monthLabel, monthOf, shiftMonth } from '@/lib/calendar'
import { isClosed } from '@/lib/filter'
import { issueIdentifier } from '@/lib/issues'
import { useSyncStore } from '@/stores/sync'
import { useUiStore } from '@/stores/ui'
import type { Issue } from '@/sync/types'

/** A team's work by due date, a month at a time. Dragging an item to another day changes its date. */
const route = useRoute()
const sync = useSyncStore()
const ui = useUiStore()
const { issueRoute } = useIssueSelect()

const team = computed(() => sync.teamByKey(String(route.params.teamKey)))

const today = () => new Date().toLocaleDateString('en-CA')
const month = ref(monthOf(today()))
const weeks = computed(() => monthGrid(month.value))

const issues = computed(() => (team.value ? sync.issuesForTeam(team.value.id) : []))
const byDay = computed(() => {
  const days = new Map<string, Issue[]>()
  for (const issue of issues.value) {
    if (!issue.dueDate) continue
    const day = days.get(issue.dueDate)
    if (day) day.push(issue)
    else days.set(issue.dueDate, [issue])
  }
  return days
})
const undated = computed(() => issues.value.filter((i) => !i.dueDate && !isClosed(i, sync.state)).length)

// --- moving an item to another day -----------------------------------------
const dragging = ref<string | null>(null)
const over = ref<string | null>(null)

function onDragStart(event: DragEvent, issue: Issue) {
  dragging.value = issue.id
  event.dataTransfer?.setData('text/plain', issue.id)
  if (event.dataTransfer) event.dataTransfer.effectAllowed = 'move'
}

function onDrop(day: string) {
  const id = dragging.value
  dragging.value = over.value = null
  if (id && sync.issue(id)?.dueDate !== day) sync.updateIssue(id, { dueDate: day })
}

const WEEKDAYS = ['Mon', 'Tue', 'Wed', 'Thu', 'Fri', 'Sat', 'Sun']
</script>

<template>
  <template v-if="team">
    <header class="flex h-11 shrink-0 items-center gap-2 border-b border-border px-5">
      <RouterLink :to="{ name: 'team-issues', params: { teamKey: team.key } }" class="font-medium hover:underline">{{ team.name }}</RouterLink>
      <span class="text-faint" aria-hidden="true">›</span>
      <h1 class="font-medium">Calendar</h1>
      <div class="ml-4 flex items-center gap-1">
        <button type="button" class="flex size-7 items-center justify-center rounded hover:bg-hover" aria-label="Previous month" @click="month = shiftMonth(month, -1)">
          <ChevronLeft class="size-4" aria-hidden="true" />
        </button>
        <span class="w-36 text-center tabular-nums" aria-live="polite">{{ monthLabel(month) }}</span>
        <button type="button" class="flex size-7 items-center justify-center rounded hover:bg-hover" aria-label="Next month" @click="month = shiftMonth(month, 1)">
          <ChevronRight class="size-4" aria-hidden="true" />
        </button>
        <BaseButton size="sm" variant="ghost" @click="month = monthOf(today())">Today</BaseButton>
      </div>
      <span v-if="undated > 0" class="ml-auto text-xs text-faint">{{ undated }} undated</span>
    </header>

    <div class="flex min-h-0 flex-1 flex-col overflow-auto">
      <div class="grid min-w-[840px] shrink-0 grid-cols-7 border-b border-border bg-sidebar text-xs text-muted" aria-hidden="true">
        <span v-for="day in WEEKDAYS" :key="day" class="border-l border-border px-2 py-1 first:border-l-0">{{ day }}</span>
      </div>
      <div class="grid min-w-[840px] flex-1 auto-rows-fr" role="grid" :aria-label="monthLabel(month)">
        <div v-for="week in weeks" :key="week[0]" class="grid min-h-28 grid-cols-7 border-b border-border" role="row">
          <div
            v-for="day in week"
            :key="day"
            role="gridcell"
            class="flex min-w-0 flex-col gap-0.5 border-l border-border p-1 first:border-l-0"
            :class="{ 'bg-hover': over === day, 'opacity-50': !inMonth(day, month) }"
            @dragover.prevent="over = day"
            @dragleave="over === day && (over = null)"
            @drop.prevent="onDrop(day)"
            @dblclick.self="ui.openNewIssue(team.id, undefined, { dueDate: day })"
          >
            <span
              class="flex size-5 items-center justify-center self-end text-[11px] tabular-nums"
              :class="day === today() ? 'bg-accent font-semibold text-accent-fg' : 'text-faint'"
              :aria-label="day"
            >
              {{ Number(day.slice(8)) }}
            </span>
            <RouterLink
              v-for="issue in byDay.get(day) ?? []"
              :key="issue.id"
              :to="issueRoute(issue)"
              draggable="true"
              class="flex h-6 shrink-0 items-center gap-1.5 rounded border border-border bg-elevated px-1.5 text-xs hover:bg-hover"
              :class="{ 'text-faint line-through': isClosed(issue, sync.state), 'opacity-40': dragging === issue.id }"
              :title="`${issueIdentifier(issue, team)} ${issue.title}`"
              @dragstart="onDragStart($event, issue)"
              @dragend="dragging = over = null"
            >
              <StateIcon v-if="sync.state(issue.stateId)" :type="sync.state(issue.stateId)!.type" :color="sync.state(issue.stateId)!.color" class="shrink-0" />
              <span class="truncate">{{ issue.title }}</span>
            </RouterLink>
          </div>
        </div>
      </div>
    </div>
  </template>
  <div v-else class="flex flex-1 items-center justify-center p-8 text-muted">Team not found.</div>
</template>
