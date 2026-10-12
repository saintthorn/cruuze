<script setup lang="ts">
import { computed } from 'vue'
import { RouterLink } from 'vue-router'

import { useIssueSelect } from '@/composables/useIssueSelect'
import { formatDueDate, isOverdue, issueIdentifier } from '@/lib/issues'
import { useIssueViewStore } from '@/stores/issueView'
import { useSyncStore } from '@/stores/sync'
import type { Issue } from '@/sync/types'

import IssueContextMenu from './IssueContextMenu.vue'
import LabelChip from './LabelChip.vue'
import PriorityIcon from './PriorityIcon.vue'
import UserAvatar from './UserAvatar.vue'

const props = defineProps<{ issue: Issue; dropBefore?: boolean }>()
const emit = defineEmits<{ dragstart: [event: DragEvent]; dragend: [] }>()

const sync = useSyncStore()
const view = useIssueViewStore()
const { issueRoute, onClick, onHover } = useIssueSelect()

const identifier = computed(() => issueIdentifier(props.issue, sync.team(props.issue.teamId)))
const labels = computed(() => sync.labelsById(props.issue.labelIds))
const assignee = computed(() => sync.user(props.issue.assigneeId))
const selected = computed(() => view.selected.has(props.issue.id))
const focused = computed(() => view.focusedId === props.issue.id)
const dragged = computed(() => view.dragging.includes(props.issue.id))
</script>

<template>
  <IssueContextMenu :issue="issue">
    <li
      :data-issue-id="issue.id"
      draggable="true"
      class="relative"
      :class="{ 'opacity-40': dragged }"
      @mousemove="onHover(issue)"
      @dragstart="emit('dragstart', $event)"
      @dragend="emit('dragend')"
    >
      <span v-if="dropBefore" class="pointer-events-none absolute inset-x-0 -top-1.5 h-0.5 rounded bg-accent" aria-hidden="true" />
      <RouterLink
        :to="issueRoute(issue)"
        draggable="false"
        class="flex flex-col gap-1.5 rounded-lg border bg-elevated p-3 transition-[transform,border-color] duration-150 hover:-translate-y-0.5 hover:border-accent"
        :class="selected ? 'border-accent bg-accent/15' : focused ? 'border-border-strong bg-hover' : 'border-border'"
        @click="onClick($event, issue)"
      >
        <span class="flex items-center gap-2 text-xs text-muted">
          <span class="tabular-nums" :class="{ 'text-faint': issue.number === 0 }">{{ identifier }}</span>
          <UserAvatar
            v-if="assignee"
            class="ml-auto"
            :name="assignee.name"
            :email="assignee.email"
            :avatar-url="assignee.avatarUrl"
            :size="18"
          />
        </span>
        <span class="line-clamp-3 font-medium">{{ issue.title }}</span>
        <span
          v-if="issue.priority > 0 || labels.length > 0 || issue.dueDate"
          class="flex flex-wrap items-center gap-1.5 text-xs text-muted"
        >
          <PriorityIcon v-if="issue.priority > 0" :priority="issue.priority" />
          <LabelChip v-for="label in labels.slice(0, 2)" :key="label.id" :label="label" />
          <span v-if="labels.length > 2" class="text-faint">+{{ labels.length - 2 }}</span>
          <span v-if="issue.dueDate" :class="{ 'text-danger': isOverdue(issue.dueDate) }">{{ formatDueDate(issue.dueDate) }}</span>
        </span>
      </RouterLink>
    </li>
  </IssueContextMenu>
</template>
