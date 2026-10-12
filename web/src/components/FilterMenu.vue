<script setup lang="ts">
import { Check, ChevronRight, ListFilter } from 'lucide-vue-next'
import {
  DropdownMenuCheckboxItem,
  DropdownMenuContent,
  DropdownMenuPortal,
  DropdownMenuRoot,
  DropdownMenuSub,
  DropdownMenuSubContent,
  DropdownMenuSubTrigger,
  DropdownMenuTrigger,
} from 'reka-ui'
import { computed } from 'vue'

import { filterCount, type IssueFilter, NO_ASSIGNEE, NO_PROJECT, stateKey } from '@/lib/filter'
import { PRIORITIES } from '@/lib/issues'
import { useSyncStore } from '@/stores/sync'
import { useUiStore } from '@/stores/ui'
import type { WorkflowState } from '@/sync/types'

import PriorityIcon from './PriorityIcon.vue'
import StateIcon from './StateIcon.vue'

const props = defineProps<{
  filter: IssueFilter
  /** The states issues in this view can be in. */
  states: WorkflowState[]
}>()

const sync = useSyncStore()
const ui = useUiStore()

// Same-named states of different teams are one status here.
const statuses = computed(() => {
  const byKey = new Map<string, WorkflowState>()
  for (const state of props.states) {
    if (!byKey.has(stateKey(state))) byKey.set(stateKey(state), state)
  }
  return [...byKey.entries()].map(([key, state]) => ({ key, state }))
})

function toggle<T>(values: T[], value: T) {
  const at = values.indexOf(value)
  if (at === -1) values.push(value)
  else values.splice(at, 1)
}

const count = computed(() => filterCount(props.filter))

const contentClass = 'z-50 max-h-80 min-w-48 overflow-y-auto rounded-lg border border-border-strong bg-elevated p-1 shadow-xl'
const itemClass =
  'flex h-8 items-center gap-2 rounded px-2 text-[13px] outline-none select-none data-[highlighted]:bg-active data-[state=open]:bg-active'
</script>

<template>
  <DropdownMenuRoot v-model:open="ui.filterOpen">
    <DropdownMenuTrigger
      class="inline-flex h-7 items-center gap-1.5 rounded-md border border-border-strong bg-elevated px-2 text-xs font-medium hover:bg-hover"
      :aria-label="count > 0 ? `Filter, ${count} active` : 'Filter'"
    >
      <ListFilter class="size-3.5" aria-hidden="true" /> Filter
      <span v-if="count > 0" class="rounded-full bg-accent px-1.5 text-[10px] text-accent-fg">{{ count }}</span>
    </DropdownMenuTrigger>
    <DropdownMenuPortal>
      <DropdownMenuContent align="end" :side-offset="4" :class="contentClass">
        <DropdownMenuSub>
          <DropdownMenuSubTrigger :class="itemClass">
            Status <ChevronRight class="ml-auto size-3.5 text-faint" aria-hidden="true" />
          </DropdownMenuSubTrigger>
          <DropdownMenuPortal>
            <DropdownMenuSubContent :class="contentClass">
              <DropdownMenuCheckboxItem
                v-for="status in statuses"
                :key="status.key"
                :model-value="filter.states.includes(status.key)"
                :class="itemClass"
                @select.prevent="toggle(filter.states, status.key)"
              >
                <StateIcon :type="status.state.type" :color="status.state.color" />
                {{ status.state.name }}
                <Check v-if="filter.states.includes(status.key)" class="ml-auto size-4 text-muted" aria-hidden="true" />
              </DropdownMenuCheckboxItem>
            </DropdownMenuSubContent>
          </DropdownMenuPortal>
        </DropdownMenuSub>

        <DropdownMenuSub>
          <DropdownMenuSubTrigger :class="itemClass">
            Priority <ChevronRight class="ml-auto size-3.5 text-faint" aria-hidden="true" />
          </DropdownMenuSubTrigger>
          <DropdownMenuPortal>
            <DropdownMenuSubContent :class="contentClass">
              <DropdownMenuCheckboxItem
                v-for="priority in PRIORITIES"
                :key="priority.value"
                :model-value="filter.priorities.includes(priority.value)"
                :class="itemClass"
                @select.prevent="toggle(filter.priorities, priority.value)"
              >
                <span class="text-muted"><PriorityIcon :priority="priority.value" /></span>
                {{ priority.label }}
                <Check v-if="filter.priorities.includes(priority.value)" class="ml-auto size-4 text-muted" aria-hidden="true" />
              </DropdownMenuCheckboxItem>
            </DropdownMenuSubContent>
          </DropdownMenuPortal>
        </DropdownMenuSub>

        <DropdownMenuSub>
          <DropdownMenuSubTrigger :class="itemClass">
            Assignee <ChevronRight class="ml-auto size-3.5 text-faint" aria-hidden="true" />
          </DropdownMenuSubTrigger>
          <DropdownMenuPortal>
            <DropdownMenuSubContent :class="contentClass">
              <DropdownMenuCheckboxItem
                :model-value="filter.assignees.includes(NO_ASSIGNEE)"
                :class="itemClass"
                @select.prevent="toggle(filter.assignees, NO_ASSIGNEE)"
              >
                No assignee
                <Check v-if="filter.assignees.includes(NO_ASSIGNEE)" class="ml-auto size-4 text-muted" aria-hidden="true" />
              </DropdownMenuCheckboxItem>
              <DropdownMenuCheckboxItem
                v-for="user in sync.users"
                :key="user.id"
                :model-value="filter.assignees.includes(user.id)"
                :class="itemClass"
                @select.prevent="toggle(filter.assignees, user.id)"
              >
                <span class="truncate">{{ user.name || user.email }}</span>
                <Check v-if="filter.assignees.includes(user.id)" class="ml-auto size-4 shrink-0 text-muted" aria-hidden="true" />
              </DropdownMenuCheckboxItem>
            </DropdownMenuSubContent>
          </DropdownMenuPortal>
        </DropdownMenuSub>

        <DropdownMenuSub>
          <DropdownMenuSubTrigger :class="itemClass" :disabled="sync.labels.length === 0">
            Label <ChevronRight class="ml-auto size-3.5 text-faint" aria-hidden="true" />
          </DropdownMenuSubTrigger>
          <DropdownMenuPortal>
            <DropdownMenuSubContent :class="contentClass">
              <DropdownMenuCheckboxItem
                v-for="label in sync.labels"
                :key="label.id"
                :model-value="filter.labels.includes(label.id)"
                :class="itemClass"
                @select.prevent="toggle(filter.labels, label.id)"
              >
                <span class="size-2.5 shrink-0 rounded-full" :style="{ background: label.color }" aria-hidden="true" />
                <span class="truncate">{{ label.name }}</span>
                <Check v-if="filter.labels.includes(label.id)" class="ml-auto size-4 shrink-0 text-muted" aria-hidden="true" />
              </DropdownMenuCheckboxItem>
            </DropdownMenuSubContent>
          </DropdownMenuPortal>
        </DropdownMenuSub>

        <DropdownMenuSub>
          <DropdownMenuSubTrigger :class="itemClass">
            Project <ChevronRight class="ml-auto size-3.5 text-faint" aria-hidden="true" />
          </DropdownMenuSubTrigger>
          <DropdownMenuPortal>
            <DropdownMenuSubContent :class="contentClass">
              <DropdownMenuCheckboxItem
                :model-value="filter.projects.includes(NO_PROJECT)"
                :class="itemClass"
                @select.prevent="toggle(filter.projects, NO_PROJECT)"
              >
                No project
                <Check v-if="filter.projects.includes(NO_PROJECT)" class="ml-auto size-4 text-muted" aria-hidden="true" />
              </DropdownMenuCheckboxItem>
              <DropdownMenuCheckboxItem
                v-for="project in sync.projects"
                :key="project.id"
                :model-value="filter.projects.includes(project.id)"
                :class="itemClass"
                @select.prevent="toggle(filter.projects, project.id)"
              >
                <span class="size-2.5 shrink-0 rounded-sm" :style="{ background: project.color }" aria-hidden="true" />
                <span class="truncate">{{ project.name }}</span>
                <Check v-if="filter.projects.includes(project.id)" class="ml-auto size-4 shrink-0 text-muted" aria-hidden="true" />
              </DropdownMenuCheckboxItem>
            </DropdownMenuSubContent>
          </DropdownMenuPortal>
        </DropdownMenuSub>
      </DropdownMenuContent>
    </DropdownMenuPortal>
  </DropdownMenuRoot>
</template>
