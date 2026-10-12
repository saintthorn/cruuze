<script setup lang="ts">
import { Plus } from 'lucide-vue-next'
import { computed, reactive, watchEffect } from 'vue'

import { type DropZone, useIssueDrag } from '@/composables/useIssueDrag'
import { type Ordering, sortIssues } from '@/lib/issues'
import { useIssueViewStore } from '@/stores/issueView'
import { useUiStore } from '@/stores/ui'
import type { Issue, WorkflowState } from '@/sync/types'

import IssueCard from './IssueCard.vue'
import StateIcon from './StateIcon.vue'

const props = defineProps<{
  issues: Issue[]
  /** One column per state, in this order. */
  states: WorkflowState[]
  ordering: Ordering
}>()

const view = useIssueViewStore()
const ui = useUiStore()
const drag = useIssueDrag(() => props.ordering === 'manual')

// Every status gets a column, even an empty one, so there is somewhere to drop.
const columns = computed(() => {
  const sorted = sortIssues(props.issues, props.ordering)
  return props.states.map((state): DropZone & { state: WorkflowState } => ({
    key: state.id,
    state,
    issues: sorted.filter((i) => i.stateId === state.id),
    patch: (issue) => (issue.stateId === state.id ? {} : { stateId: state.id }),
  }))
})

watchEffect(() => view.setVisible(columns.value.flatMap((c) => c.issues.map((i) => i.id))))

// A column shows its first cards and more on request: a status with
// thousands of issues would otherwise render every one of them.
const PAGE = 60
const limits = reactive(new Map<string, number>())
const limitOf = (key: string) => limits.get(key) ?? PAGE
</script>

<template>
  <div class="flex min-h-0 flex-1 gap-3.5 overflow-x-auto p-4">
    <section
      v-for="column in columns"
      :key="column.key"
      :aria-label="column.state.name"
      class="flex w-72 shrink-0 flex-col rounded-xl border transition-colors"
      :class="drag.dropTarget.value?.key === column.key ? 'border-accent bg-accent/10' : 'border-border bg-elevated/50'"
      @dragover="drag.onDragOver($event, column)"
      @dragleave="drag.onDragLeave($event, column)"
      @drop="drag.onDrop($event, column)"
    >
      <h2 class="flex h-10 shrink-0 items-center gap-2 px-3 font-medium">
        <StateIcon :type="column.state.type" :color="column.state.color" />
        {{ column.state.name }}
        <span class="font-normal text-faint">{{ column.issues.length }}</span>
        <button
          type="button"
          class="ml-auto flex size-6 items-center justify-center rounded text-faint hover:bg-hover hover:text-fg"
          :aria-label="`New issue in ${column.state.name}`"
          @click="ui.openNewIssue(column.state.teamId, column.state.id)"
        >
          <Plus class="size-3.5" aria-hidden="true" />
        </button>
      </h2>
      <ul class="flex min-h-16 flex-1 flex-col gap-2 overflow-y-auto px-2 pb-2">
        <IssueCard
          v-for="issue in column.issues.slice(0, limitOf(column.key))"
          :key="issue.id"
          :issue="issue"
          :drop-before="drag.dropsBefore(column, issue.id)"
          @dragstart="drag.onDragStart($event, issue)"
          @dragend="drag.onDragEnd"
        />
        <li
          v-if="drag.dropsAtEnd(column) && ordering === 'manual' && column.issues.length > 0"
          class="pointer-events-none -mt-1 h-0.5 shrink-0 rounded bg-accent"
          aria-hidden="true"
        />
        <li v-if="column.issues.length > limitOf(column.key)" class="shrink-0">
          <button
            type="button"
            class="h-8 w-full rounded text-xs text-muted hover:bg-hover hover:text-fg"
            @click="limits.set(column.key, limitOf(column.key) + PAGE)"
          >
            Show {{ Math.min(PAGE, column.issues.length - limitOf(column.key)) }} more of
            {{ column.issues.length - limitOf(column.key) }}
          </button>
        </li>
      </ul>
    </section>
  </div>
</template>
