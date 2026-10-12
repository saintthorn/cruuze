<script setup lang="ts">
import { Plus, Trash2, TriangleAlert } from 'lucide-vue-next'
import { computed, ref, useTemplateRef } from 'vue'
import { RouterLink, useRouter } from 'vue-router'

import DueDateField from '@/components/issue/DueDateField.vue'
import PromptDialog from '@/components/PromptDialog.vue'
import BaseButton from '@/components/ui/BaseButton.vue'
import { projectStatusLabel, todayUTC } from '@/lib/issues'
import { dependencyProblems, type DragMode, dragDates, healthInfo } from '@/lib/roadmap'
import { useSyncStore } from '@/stores/sync'
import { useWorkspaceStore } from '@/stores/workspace'
import type { Initiative, Project } from '@/sync/types'

const router = useRouter()
const sync = useSyncStore()
const workspace = useWorkspaceStore()
const editable = computed(() => workspace.canManageLabels)

const DAY = 24 * 60 * 60 * 1000
const time = (date: string) => Date.parse(`${date}T00:00:00Z`)

type Dated = Project & { startDate: string; targetDate: string }

// A project needs both dates to be drawn as a bar.
const isDated = (p: Project): p is Dated => Boolean(p.startDate && p.targetDate && p.startDate <= p.targetDate)
const dated = computed(() => sync.projects.filter(isDated).sort((a, b) => a.startDate.localeCompare(b.startDate)))
const undated = computed(() => sync.projects.filter((p) => !isDated(p)))

// Projects are laid out under their initiative; the rest come last.
const groups = computed(() => {
  const out: { initiative?: Initiative; projects: Dated[]; done: number; total: number }[] = []
  const group = (initiative: Initiative | undefined, projects: Dated[], all: Project[]) => {
    let done = 0
    let total = 0
    for (const project of all) {
      const progress = sync.progressOfProject(project.id)
      done += progress.done
      total += progress.total
    }
    out.push({ initiative, projects, done, total })
  }
  for (const initiative of sync.initiatives) {
    group(initiative, dated.value.filter((p) => p.initiativeId === initiative.id), sync.projects.filter((p) => p.initiativeId === initiative.id))
  }
  const loose = dated.value.filter((p) => !sync.initiative(p.initiativeId))
  if (loose.length > 0 || out.length === 0) group(undefined, loose, [])
  return out
})

// The timeline runs over whole months, from the earliest start to the latest
// target, with a month to spare on each side to drag into.
const range = computed(() => {
  const today = time(todayUTC())
  const starts = dated.value.map((p) => time(p.startDate))
  const ends = [...dated.value.map((p) => time(p.targetDate)), ...sync.initiatives.flatMap((i) => (i.targetDate ? [time(i.targetDate)] : []))]
  const first = new Date(Math.min(today, ...starts))
  const last = new Date(Math.max(today, ...ends))
  const start = Date.UTC(first.getUTCFullYear(), first.getUTCMonth() - 1, 1)
  const end = Date.UTC(last.getUTCFullYear(), last.getUTCMonth() + 2, 1)
  return { start, end, span: end - start }
})

const months = computed(() => {
  const out: { label: string; left: number; width: number }[] = []
  const { start, end, span } = range.value
  for (let cursor = new Date(start); cursor.getTime() < end; ) {
    const next = new Date(Date.UTC(cursor.getUTCFullYear(), cursor.getUTCMonth() + 1, 1))
    out.push({
      label: cursor.toLocaleDateString('en-US', { month: 'short', year: cursor.getUTCMonth() === 0 ? 'numeric' : undefined, timeZone: 'UTC' }),
      left: ((cursor.getTime() - start) / span) * 100,
      width: ((next.getTime() - cursor.getTime()) / span) * 100,
    })
    cursor = next
  }
  return out
})

const leftOf = (date: string) => ((time(date) - range.value.start) / range.value.span) * 100
const todayLeft = computed(() => leftOf(todayUTC()))

// --- dependencies --------------------------------------------------------------
// Where each drawn project sits, top to bottom, in pixels: the rows have
// fixed heights (see the template), so this needs no measuring.
const MONTHS_HEIGHT = 32
const HEADING_HEIGHT = 36
const ROW_HEIGHT = 48
const rowCenters = computed(() => {
  const centers = new Map<string, number>()
  let y = MONTHS_HEIGHT
  for (const group of groups.value) {
    if (group.initiative || groups.value.length > 1) y += HEADING_HEIGHT
    for (const project of group.projects) {
      centers.set(project.id, y + ROW_HEIGHT / 2)
      y += ROW_HEIGHT
    }
    if (group.initiative && group.projects.length === 0) y += HEADING_HEIGHT
  }
  return centers
})

/** A line from the end of each project to the start of the one that waits on it. */
const links = computed(() => {
  const out: { id: string; x1: number; y1: number; x2: number; y2: number; turn: number; late: boolean }[] = []
  for (const dependency of sync.projectDependencies) {
    const waiting = dated.value.find((p) => p.id === dependency.projectId)
    const needed = dated.value.find((p) => p.id === dependency.dependsOnId)
    const y1 = rowCenters.value.get(dependency.dependsOnId)
    const y2 = rowCenters.value.get(dependency.projectId)
    if (!waiting || !needed || y1 === undefined || y2 === undefined) continue
    const from = shown(needed)
    const to = shown(waiting)
    out.push({
      id: dependency.id,
      x1: leftOf(from.targetDate) + (DAY / range.value.span) * 100,
      y1,
      x2: leftOf(to.startDate),
      y2,
      // The line runs along the bars' ends and turns in the gap beside the
      // waiting project's bar, so it never crosses a name.
      turn: y2 + (y1 > y2 ? 1 : -1) * (ROW_HEIGHT / 2 - 4),
      // What it waits on is planned to finish after it does.
      late: needed.status !== 'completed' && from.targetDate > to.targetDate,
    })
  }
  return out
})

// --- dragging ----------------------------------------------------------------
// A bar moves whole; its ends pull the start or the target. Nothing is saved
// until the pointer lets go.
const track = useTemplateRef('track')
const drag = ref<{ id: string; mode: DragMode; startX: number; days: number } | null>(null)
let dragged = false

/** The dates to draw: the dragged ones while a drag is on. */
function shown(project: Dated): { startDate: string; targetDate: string } {
  const d = drag.value
  return d && d.id === project.id ? dragDates(project.startDate, project.targetDate, d.days, d.mode) : project
}

function bar(project: Dated) {
  const { startDate, targetDate } = shown(project)
  return {
    left: `${leftOf(startDate)}%`,
    // The target day is part of the project.
    width: `${((time(targetDate) + DAY - time(startDate)) / range.value.span) * 100}%`,
  }
}

function onPointerDown(event: PointerEvent, project: Dated, mode: DragMode) {
  if (!editable.value || event.button !== 0) return
  // Keeps the drag going when the pointer leaves the bar.
  ;(event.currentTarget as HTMLElement).setPointerCapture?.(event.pointerId)
  drag.value = { id: project.id, mode, startX: event.clientX, days: 0 }
  dragged = false
}

function onPointerMove(event: PointerEvent) {
  const d = drag.value
  if (!d || !track.value) return
  const pxPerDay = track.value.getBoundingClientRect().width / (range.value.span / DAY)
  d.days = Math.round((event.clientX - d.startX) / pxPerDay)
  if (d.days !== 0) dragged = true
}

function onPointerUp(project: Dated) {
  const d = drag.value
  drag.value = null
  if (d && d.id === project.id && d.days !== 0) {
    sync.updateRecord('Project', project.id, dragDates(project.startDate, project.targetDate, d.days, d.mode))
  }
}

function open(project: Project) {
  // The click that ends a drag isn't a click on the project.
  if (dragged) dragged = false
  else void router.push({ name: 'project', params: { projectId: project.id } })
}

/** Shift+arrow moves a project a week; with Alt as well, a day. */
function onKeydown(event: KeyboardEvent, project: Dated) {
  if (!editable.value || !event.shiftKey || (event.key !== 'ArrowLeft' && event.key !== 'ArrowRight')) return
  event.preventDefault()
  const days = (event.key === 'ArrowLeft' ? -1 : 1) * (event.altKey ? 1 : 7)
  sync.updateRecord('Project', project.id, dragDates(project.startDate, project.targetDate, days, 'move'))
}

const problems = (project: Project) =>
  dependencyProblems(project, sync.projectDependencies, sync.project, sync.projectHealth, todayUTC())

function describe(project: Dated): string {
  const { startDate, targetDate } = shown(project)
  const health = sync.projectHealth(project.id)
  return [
    `${project.name}: ${startDate} to ${targetDate}`,
    projectStatusLabel(project.status),
    `${sync.progressOfProject(project.id).percent}% done`,
    ...(health ? [healthInfo(health).label] : []),
    ...problems(project),
  ].join(' · ')
}

// --- initiatives ---------------------------------------------------------------
const createOpen = ref(false)

function rename(initiative: Initiative, event: Event) {
  const input = event.target as HTMLInputElement
  const name = input.value.trim()
  if (name && name !== initiative.name) sync.updateRecord('Initiative', initiative.id, { name })
  else input.value = initiative.name
}

function remove(initiative: Initiative) {
  if (window.confirm(`Delete the initiative "${initiative.name}"? Its projects are kept, without an initiative.`)) {
    sync.deleteRecord('Initiative', initiative.id)
  }
}
</script>

<template>
  <header class="flex h-11 shrink-0 items-center gap-2 border-b border-border px-5">
    <RouterLink :to="{ name: 'projects' }" class="text-muted hover:text-fg">Projects</RouterLink>
    <span class="text-faint" aria-hidden="true">›</span>
    <h1 class="font-medium">Roadmap</h1>
    <BaseButton v-if="editable" size="sm" class="ml-auto" @click="createOpen = true">
      <Plus class="size-3.5" aria-hidden="true" /> New initiative
    </BaseButton>
  </header>

  <div class="flex-1 overflow-auto">
    <div
      v-if="dated.length > 0 || sync.initiatives.length > 0"
      ref="track"
      class="relative min-w-[720px]"
      :style="{ width: `${Math.max(100, months.length * 12)}%` }"
    >
      <div class="sticky top-0 z-20 flex h-8 border-b border-border bg-sidebar text-xs text-muted" aria-hidden="true">
        <span
          v-for="month in months"
          :key="month.left"
          class="absolute flex h-8 items-center border-l border-border px-2"
          :style="{ left: `${month.left}%`, width: `${month.width}%` }"
        >
          {{ month.label }}
        </span>
      </div>
      <span
        class="pointer-events-none absolute top-8 bottom-0 z-10 w-px bg-accent"
        :style="{ left: `${todayLeft}%` }"
        aria-hidden="true"
      />

      <section v-for="group in groups" :key="group.initiative?.id ?? 'none'" :aria-label="group.initiative?.name ?? 'No initiative'">
        <div v-if="group.initiative || groups.length > 1" class="relative h-9 border-b border-border bg-elevated">
          <div class="sticky left-0 z-10 flex h-9 w-max max-w-[100vw] items-center gap-2 px-3 text-xs">
            <template v-if="group.initiative">
              <span class="size-2.5 shrink-0 rounded-sm" :style="{ background: group.initiative.color }" aria-hidden="true" />
              <input
                :value="group.initiative.name"
                aria-label="Initiative name"
                maxlength="80"
                :readonly="!editable"
                class="w-56 bg-transparent font-medium text-fg outline-none"
                @change="rename(group.initiative, $event)"
                @keydown.enter.prevent="($event.target as HTMLInputElement).blur()"
              />
              <span class="text-faint tabular-nums" title="Issues done in its projects">{{ group.done }}/{{ group.total }}</span>
              <span class="flex items-center gap-1 text-muted">
                Target
                <DueDateField
                  variant="chip"
                  :due-date="group.initiative.targetDate"
                  @update="sync.updateRecord('Initiative', group.initiative.id, { targetDate: $event })"
                />
              </span>
              <button
                v-if="editable"
                type="button"
                class="flex size-6 items-center justify-center rounded text-faint hover:bg-hover hover:text-danger"
                :aria-label="`Delete initiative ${group.initiative.name}`"
                @click="remove(group.initiative)"
              >
                <Trash2 class="size-3.5" aria-hidden="true" />
              </button>
            </template>
            <span v-else class="font-medium text-muted">No initiative</span>
          </div>
          <span
            v-if="group.initiative?.targetDate"
            class="pointer-events-none absolute top-0 bottom-0 w-0.5"
            :style="{ left: `${leftOf(group.initiative.targetDate)}%`, background: group.initiative.color }"
            :title="`${group.initiative.name} target: ${group.initiative.targetDate}`"
          />
        </div>
        <ul>
          <li v-for="project in group.projects" :key="project.id" class="relative h-12 border-b border-border">
            <div
              class="absolute top-2 flex h-8 min-w-24 items-center gap-2 overflow-hidden rounded-md border bg-elevated px-2 text-xs select-none hover:bg-hover"
              :class="[
                drag?.id === project.id ? 'z-10 border-accent' : 'border-border-strong',
                editable ? 'cursor-grab touch-none active:cursor-grabbing' : 'cursor-pointer',
              ]"
              :style="bar(project)"
              :title="describe(project)"
              @pointerdown="onPointerDown($event, project, 'move')"
              @pointermove="onPointerMove"
              @pointerup="onPointerUp(project)"
              @pointercancel="drag = null"
              @click="open(project)"
            >
              <span
                v-if="editable"
                class="absolute inset-y-0 left-0 w-1.5 cursor-ew-resize hover:bg-accent"
                aria-hidden="true"
                @pointerdown.stop="onPointerDown($event, project, 'start')"
                @pointermove="onPointerMove"
                @pointerup="onPointerUp(project)"
                @click.stop="dragged = false"
              />
              <span class="size-2.5 shrink-0 rounded-sm" :style="{ background: project.color }" aria-hidden="true" />
              <RouterLink
                :to="{ name: 'project', params: { projectId: project.id } }"
                class="truncate font-medium"
                draggable="false"
                :aria-label="describe(project)"
                @click.prevent
                @keydown="onKeydown($event, project)"
              >
                {{ project.name }}
              </RouterLink>
              <TriangleAlert v-if="problems(project).length > 0" class="size-3.5 shrink-0 text-warning" aria-hidden="true" />
              <span
                v-if="sync.projectHealth(project.id)"
                class="size-2 shrink-0 rounded-full bg-current"
                :class="healthInfo(sync.projectHealth(project.id)!).class"
                aria-hidden="true"
              />
              <span class="ml-auto shrink-0 text-faint tabular-nums">
                {{ drag?.id === project.id ? `${shown(project).startDate.slice(5)} → ${shown(project).targetDate.slice(5)}` : `${sync.progressOfProject(project.id).percent}%` }}
              </span>
              <span
                class="absolute bottom-0 left-0 h-0.5"
                :style="{ width: `${sync.progressOfProject(project.id).percent}%`, background: project.color }"
                aria-hidden="true"
              />
              <span
                v-if="editable"
                class="absolute inset-y-0 right-0 w-1.5 cursor-ew-resize hover:bg-accent"
                aria-hidden="true"
                @pointerdown.stop="onPointerDown($event, project, 'end')"
                @pointermove="onPointerMove"
                @pointerup="onPointerUp(project)"
                @click.stop="dragged = false"
              />
            </div>
          </li>
          <li v-if="group.initiative && group.projects.length === 0" class="h-9 border-b border-border">
            <p class="sticky left-0 flex h-9 w-max items-center px-3 text-xs text-faint">
              No dated projects
            </p>
          </li>
        </ul>
      </section>

      <!-- Dependencies, drawn over the rows: from the end of a project to the start of the one waiting on it. -->
      <svg v-if="links.length > 0" class="pointer-events-none absolute inset-0 size-full" aria-hidden="true">
        <g v-for="link in links" :key="link.id" :class="link.late ? 'text-warning' : 'text-muted'" stroke="currentColor" fill="currentColor">
          <g stroke-width="1" :stroke-dasharray="link.late ? undefined : '3 3'">
            <line :x1="`${link.x1}%`" :y1="link.y1" :x2="`${link.x1}%`" :y2="link.turn" />
            <line :x1="`${link.x1}%`" :y1="link.turn" :x2="`${link.x2}%`" :y2="link.turn" />
            <line :x1="`${link.x2}%`" :y1="link.turn" :x2="`${link.x2}%`" :y2="link.y2" />
          </g>
          <circle :cx="`${link.x1}%`" :cy="link.y1" r="2" />
          <circle :cx="`${link.x2}%`" :cy="link.y2" r="3" />
        </g>
      </svg>
    </div>

    <p v-if="links.length > 0" class="flex items-center gap-4 px-5 pt-3 text-xs text-faint">
      <span class="flex items-center gap-1.5"><span class="h-px w-5 bg-muted" aria-hidden="true" /> waits on</span>
      <span class="flex items-center gap-1.5 text-warning"><span class="h-px w-5 bg-warning" aria-hidden="true" /> due too early</span>
    </p>

    <section v-if="undated.length > 0" class="p-5" aria-labelledby="undated-heading">
      <h2 id="undated-heading" class="text-xs font-medium text-faint">
        No dates
      </h2>
      <ul class="mt-2 flex flex-wrap gap-2">
        <li v-for="project in undated" :key="project.id">
          <RouterLink
            :to="{ name: 'project', params: { projectId: project.id } }"
            class="flex h-8 items-center gap-2 rounded-md border border-border-strong bg-elevated px-2 text-xs hover:bg-hover"
          >
            <span class="size-2.5 rounded-sm" :style="{ background: project.color }" aria-hidden="true" />
            {{ project.name }}
            <span v-if="sync.initiative(project.initiativeId)" class="text-faint">· {{ sync.initiative(project.initiativeId)!.name }}</span>
          </RouterLink>
        </li>
      </ul>
    </section>

    <p v-if="sync.projects.length === 0 && sync.initiatives.length === 0" class="p-8 text-center text-muted">No projects</p>
  </div>

  <PromptDialog
    v-model:open="createOpen"
    title="New initiative"
    label="Initiative name"
    placeholder="Self-serve onboarding"
    action="Create initiative"
    @submit="sync.createInitiative($event)"
  />
</template>
