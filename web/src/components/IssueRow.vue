<script setup lang="ts">
import { computed } from 'vue'
import { RouterLink } from 'vue-router'

import { useIssueSelect } from '@/composables/useIssueSelect'
import { formatDueDate, isOverdue, issueIdentifier } from '@/lib/issues'
import { useIssueViewStore } from '@/stores/issueView'
import { useSyncStore } from '@/stores/sync'
import type { Issue } from '@/sync/types'

import AssigneePicker from './issue/AssigneePicker.vue'
import PriorityPicker from './issue/PriorityPicker.vue'
import StatusPicker from './issue/StatusPicker.vue'
import IssueContextMenu from './IssueContextMenu.vue'
import LabelChip from './LabelChip.vue'

const props = defineProps<{
  issue: Issue
  /** Show the line that marks where dragged issues would land. */
  dropBefore?: boolean
}>()

const emit = defineEmits<{ dragstart: [event: DragEvent]; dragend: [] }>()

const sync = useSyncStore()
const view = useIssueViewStore()
const { issueRoute, onClick, onHover } = useIssueSelect()

const team = computed(() => sync.team(props.issue.teamId))
const states = computed(() => sync.statesForTeam(props.issue.teamId))
const labels = computed(() => sync.labelsById(props.issue.labelIds))
const identifier = computed(() => issueIdentifier(props.issue, team.value))
const selected = computed(() => view.selected.has(props.issue.id))
const focused = computed(() => view.focusedId === props.issue.id)
const dragged = computed(() => view.dragging.includes(props.issue.id))
</script>

<template>
  <IssueContextMenu :issue="issue">
    <li
      :data-issue-id="issue.id"
      draggable="true"
      class="group relative flex h-10 items-center gap-2 border-b border-border pr-5 pl-1.5"
      :class="[selected ? 'bg-accent/15' : focused ? 'bg-hover' : '', { 'opacity-40': dragged }]"
      @mousemove="onHover(issue)"
      @dragstart="emit('dragstart', $event)"
      @dragend="emit('dragend')"
    >
      <span v-if="dropBefore" class="pointer-events-none absolute inset-x-0 -top-px z-10 h-0.5 bg-accent" aria-hidden="true" />
      <input
        type="checkbox"
        :checked="selected"
        :aria-label="`Select ${identifier}`"
        class="size-3.5 shrink-0 accent-(--accent) opacity-0 group-hover:opacity-100 focus-visible:opacity-100"
        :class="{ '!opacity-100': selected || view.selected.size > 0 }"
        @click.stop="$event.shiftKey ? view.selectRangeTo(issue.id) : view.toggle(issue.id)"
      />
      <PriorityPicker :priority="issue.priority" @update="sync.updateIssue(issue.id, { priority: $event })" />
      <span class="w-16 shrink-0 truncate text-muted tabular-nums" :class="{ 'text-faint': issue.number === 0 }">
        {{ identifier }}
      </span>
      <StatusPicker :states="states" :state-id="issue.stateId" @update="sync.updateIssue(issue.id, { stateId: $event })" />

      <RouterLink
        :to="issueRoute(issue)"
        draggable="false"
        class="flex h-full min-w-0 flex-1 items-center rounded px-1 font-medium"
        @click="onClick($event, issue)"
      >
        <span class="truncate">{{ issue.title }}</span>
      </RouterLink>

      <span v-if="labels.length > 0" class="hidden shrink-0 items-center gap-1 md:flex">
        <LabelChip v-for="label in labels.slice(0, 3)" :key="label.id" :label="label" />
        <span v-if="labels.length > 3" class="text-xs text-faint">+{{ labels.length - 3 }}</span>
      </span>
      <span
        v-if="issue.dueDate"
        class="shrink-0 text-xs"
        :class="isOverdue(issue.dueDate) ? 'text-danger' : 'text-muted'"
        :title="`Due ${issue.dueDate}`"
      >
        {{ formatDueDate(issue.dueDate) }}
      </span>
      <AssigneePicker :assignee-id="issue.assigneeId" @update="sync.updateIssue(issue.id, { assigneeId: $event })" />
    </li>
  </IssueContextMenu>
</template>
