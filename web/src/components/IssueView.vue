<script setup lang="ts">
import { computed, onBeforeUnmount, watch } from 'vue'

import { FILTER_PROPERTIES, isClosed, isEmptyFilter, matchesFilter } from '@/lib/filter'
import { useIssueViewStore } from '@/stores/issueView'
import { useSyncStore } from '@/stores/sync'
import { useViewOptionsStore } from '@/stores/viewOptions'
import type { Issue, WorkflowState } from '@/sync/types'

import BulkBar from './BulkBar.vue'
import FilterChips from './FilterChips.vue'
import IssueBoard from './IssueBoard.vue'
import IssueList from './IssueList.vue'
import BaseButton from './ui/BaseButton.vue'

const props = defineProps<{
  /** Names this view for remembering its options, e.g. `team:ENG`. */
  viewKey: string
  /** Everything the view could show, before filtering. */
  issues: Issue[]
  /** The states those issues can be in, for the filter. */
  states: WorkflowState[]
  /** A board has one column per state, so it needs a single team's states. */
  boardStates?: WorkflowState[]
}>()

const sync = useSyncStore()
const view = useIssueViewStore()
const viewOptions = useViewOptionsStore()

const options = computed(() => viewOptions.optionsFor(props.viewKey))

// Tell shortcuts and the command menu which view is on screen.
watch(
  () => props.viewKey,
  (key) => {
    viewOptions.currentView = key
    view.reset()
  },
  { immediate: true },
)
onBeforeUnmount(() => {
  if (viewOptions.currentView === props.viewKey) viewOptions.currentView = null
  view.reset()
  view.setVisible([])
})

const shown = computed(() => {
  // One read of the store per pass, not one per issue.
  const states = sync.stateIndex
  const stateOf = (id: string) => states.get(id)
  const { filter, showClosed } = options.value
  if (isEmptyFilter(filter) && showClosed) return props.issues
  return props.issues.filter((issue) => matchesFilter(issue, filter, stateOf) && (showClosed || !isClosed(issue, stateOf)))
})
const hidden = computed(() => props.issues.length - shown.value.length)
const asBoard = computed(() => options.value.layout === 'board' && props.boardStates !== undefined)

function showEverything() {
  const { filter } = options.value
  for (const property of FILTER_PROPERTIES) filter[property].length = 0
  options.value.showClosed = true
}
</script>

<template>
  <div class="relative flex min-h-0 flex-1 flex-col">
    <FilterChips :filter="options.filter" :states="states" />

    <IssueBoard v-if="asBoard" :issues="shown" :states="boardStates!" :ordering="options.ordering" />
    <IssueList v-else-if="shown.length > 0" :issues="shown" :grouping="options.grouping" :ordering="options.ordering" />
    <div v-else class="flex flex-1 flex-col items-center justify-center gap-2 p-8 text-center">
      <p class="text-muted">{{ hidden }} hidden by {{ isEmptyFilter(options.filter) ? 'display options' : 'filters' }}</p>
      <BaseButton class="mt-2" @click="showEverything">Show all issues</BaseButton>
    </div>

    <BulkBar />
  </div>
</template>
