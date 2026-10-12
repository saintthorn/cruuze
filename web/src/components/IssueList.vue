<script setup lang="ts">
import { CircleUser } from 'lucide-vue-next'
import { computed, onBeforeUnmount, onMounted, ref, useTemplateRef, watchEffect } from 'vue'

import { useIssueActions } from '@/composables/useIssueActions'
import { type DropZone, useIssueDrag } from '@/composables/useIssueDrag'
import { type Grouping, groupIssues, type IssueGroup, type Ordering, sortIssues } from '@/lib/issues'
import { useIssueViewStore } from '@/stores/issueView'
import { useSyncStore } from '@/stores/sync'
import type { Issue } from '@/sync/types'

import IssueRow from './IssueRow.vue'
import PriorityIcon from './PriorityIcon.vue'
import StateIcon from './StateIcon.vue'
import UserAvatar from './UserAvatar.vue'

const props = defineProps<{ issues: Issue[]; grouping: Grouping; ordering: Ordering }>()

// Heights in pixels, matching the classes on the header (h-9) and rows (h-10).
// Rows have a fixed height so that which ones are on screen can be worked
// out from the scroll position alone.
const HEADER = 36
const ROW = 40
/** Rows rendered beyond each edge of the viewport, so scrolling doesn't show gaps. */
const OVERSCAN = 12

const sync = useSyncStore()
const view = useIssueViewStore()
const actions = useIssueActions()
const drag = useIssueDrag(() => props.ordering === 'manual')

const groups = computed(() => {
  const states = sync.stateIndex
  return groupIssues(sortIssues(props.issues, props.ordering), props.grouping, {
    state: (id) => states.get(id),
    user: sync.user,
    project: sync.project,
  })
})

// Dropping into a group gives the issue that group's status, priority or assignee.
const zones = computed(() =>
  groups.value.map((group): DropZone & { group: IssueGroup } => ({
    key: group.key,
    issues: group.issues,
    group,
    patch: (issue) => actions.groupPatch(issue, group, props.grouping),
  })),
)

watchEffect(() => view.setVisible(groups.value.flatMap((g) => g.issues.map((i) => i.id))))

// --- Windowing: a list can hold tens of thousands of issues, but only the
// rows near the viewport are rendered. Padding stands in for the rest.
const scroller = useTemplateRef('scroller')
const scrollTop = ref(0)
const viewport = ref(800)
let observer: ResizeObserver | undefined

const sections = computed(() => {
  let top = 0
  return zones.value.map((zone) => {
    const count = zone.issues.length
    // Where this zone's first row starts, measured from the top of the list.
    const rowsTop = top + (zone.group.label ? HEADER : 0)
    top = rowsTop + count * ROW
    const first = Math.min(count, Math.max(0, Math.floor((scrollTop.value - rowsTop) / ROW) - OVERSCAN))
    const last = Math.min(count, Math.max(first, Math.ceil((scrollTop.value + viewport.value - rowsTop) / ROW) + OVERSCAN))
    return {
      zone,
      rowsTop,
      rows: zone.issues.slice(first, last),
      padding: { paddingTop: `${first * ROW}px`, paddingBottom: `${(count - last) * ROW}px` },
    }
  })
})

/** Scrolls just enough to show an issue's row, for moving through the list by keyboard. */
function reveal(id: string) {
  const el = scroller.value
  if (!el) return
  for (const section of sections.value) {
    const index = section.zone.issues.findIndex((issue) => issue.id === id)
    if (index === -1) continue
    const top = section.rowsTop + index * ROW
    // Sticky headers cover the top of the viewport.
    if (top < el.scrollTop + HEADER) el.scrollTop = top - HEADER
    else if (top + ROW > el.scrollTop + el.clientHeight) el.scrollTop = top + ROW - el.clientHeight
    return
  }
}

onMounted(() => {
  const el = scroller.value
  if (!el) return
  viewport.value = el.clientHeight
  if (typeof ResizeObserver !== 'undefined') {
    observer = new ResizeObserver(() => (viewport.value = el.clientHeight))
    observer.observe(el)
  }
  view.setRevealer(reveal)
})
onBeforeUnmount(() => {
  observer?.disconnect()
  view.setRevealer(null)
})
</script>

<template>
  <div ref="scroller" class="flex-1 overflow-y-auto" @scroll.passive="scrollTop = ($event.target as HTMLElement).scrollTop">
    <section
      v-for="{ zone, rows, padding } in sections"
      :key="zone.key"
      :aria-label="zone.group.label || 'Issues'"
      @dragover="drag.onDragOver($event, zone)"
      @dragleave="drag.onDragLeave($event, zone)"
      @drop="drag.onDrop($event, zone)"
    >
      <h2
        v-if="zone.group.label"
        class="sticky top-0 z-10 flex h-9 items-center gap-2 border-b border-border bg-sidebar pr-5 pl-7 font-medium"
      >
        <span class="flex size-6 items-center justify-center text-muted">
          <StateIcon v-if="zone.group.state" :type="zone.group.state.type" :color="zone.group.state.color" />
          <PriorityIcon v-else-if="zone.group.priority !== undefined" :priority="zone.group.priority" />
          <UserAvatar
            v-else-if="zone.group.user"
            :name="zone.group.user.name"
            :email="zone.group.user.email"
            :avatar-url="zone.group.user.avatarUrl"
            :size="18"
          />
          <span
            v-else-if="grouping === 'project'"
            class="size-2.5 rounded-sm"
            :style="{ background: zone.group.project?.color ?? 'var(--fg-faint)' }"
            aria-hidden="true"
          />
          <CircleUser v-else class="size-[18px] text-faint" aria-hidden="true" />
        </span>
        {{ zone.group.label }}
        <span class="font-normal text-faint">{{ zone.group.issues.length }}</span>
      </h2>
      <ul class="relative" :class="{ 'bg-accent/5': drag.dropTarget.value?.key === zone.key }" :style="padding">
        <IssueRow
          v-for="issue in rows"
          :key="issue.id"
          :issue="issue"
          :drop-before="drag.dropsBefore(zone, issue.id)"
          @dragstart="drag.onDragStart($event, issue)"
          @dragend="drag.onDragEnd"
        />
        <li
          v-if="drag.dropsAtEnd(zone) && ordering === 'manual'"
          class="pointer-events-none absolute inset-x-0 -bottom-px z-10 h-0.5 bg-accent"
          aria-hidden="true"
        />
      </ul>
    </section>
  </div>
</template>
