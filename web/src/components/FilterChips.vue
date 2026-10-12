<script setup lang="ts">
import { X } from 'lucide-vue-next'
import { computed } from 'vue'

import { FILTER_PROPERTIES, type IssueFilter, NO_ASSIGNEE, NO_PROJECT, stateKey } from '@/lib/filter'
import { priorityLabel } from '@/lib/issues'
import { useSyncStore } from '@/stores/sync'
import type { WorkflowState } from '@/sync/types'

const props = defineProps<{ filter: IssueFilter; states: WorkflowState[] }>()

const sync = useSyncStore()

interface Chip {
  property: keyof IssueFilter
  label: string
  values: string
}

const chips = computed<Chip[]>(() => {
  const { filter } = props
  const out: Chip[] = []
  if (filter.states.length > 0) {
    const names = filter.states.map((key) => props.states.find((s) => stateKey(s) === key)?.name ?? key.split(':')[1])
    out.push({ property: 'states', label: 'Status', values: names.join(', ') })
  }
  if (filter.priorities.length > 0) {
    out.push({ property: 'priorities', label: 'Priority', values: filter.priorities.map(priorityLabel).join(', ') })
  }
  if (filter.assignees.length > 0) {
    const names = filter.assignees.map((id) => {
      if (id === NO_ASSIGNEE) return 'No assignee'
      const user = sync.user(id)
      return user ? user.name || user.email : 'Unknown'
    })
    out.push({ property: 'assignees', label: 'Assignee', values: names.join(', ') })
  }
  if (filter.labels.length > 0) {
    const names = sync.labelsById(filter.labels).map((l) => l.name)
    out.push({ property: 'labels', label: 'Label', values: names.join(', ') || 'Deleted label' })
  }
  if (filter.projects.length > 0) {
    const names = filter.projects.map((id) => (id === NO_PROJECT ? 'No project' : (sync.project(id)?.name ?? 'Deleted project')))
    out.push({ property: 'projects', label: 'Project', values: names.join(', ') })
  }
  return out
})

function clear(property?: keyof IssueFilter) {
  for (const key of property ? [property] : FILTER_PROPERTIES) {
    props.filter[key].length = 0
  }
}
</script>

<template>
  <div v-if="chips.length > 0" class="flex shrink-0 flex-wrap items-center gap-1.5 border-b border-border px-5 py-2">
    <span
      v-for="chip in chips"
      :key="chip.property"
      class="inline-flex h-6 max-w-full items-center gap-1 rounded-md border border-border-strong pr-0.5 pl-2 text-xs"
    >
      <span class="text-muted">{{ chip.label }} is</span>
      <span class="truncate font-medium">{{ chip.values }}</span>
      <button
        type="button"
        class="flex size-5 shrink-0 items-center justify-center rounded text-faint hover:bg-hover hover:text-fg"
        :aria-label="`Remove ${chip.label.toLowerCase()} filter`"
        @click="clear(chip.property)"
      >
        <X class="size-3" aria-hidden="true" />
      </button>
    </span>
    <button type="button" class="h-6 rounded px-2 text-xs text-muted hover:bg-hover hover:text-fg" @click="clear()">
      Clear
    </button>
  </div>
</template>
