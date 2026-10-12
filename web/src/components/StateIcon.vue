<script setup lang="ts">
import { computed } from 'vue'

import type { WorkflowState } from '@/sync/types'

// The state's own color is part of its record, but this theme draws every
// state in its own palette: dim for waiting, amber for in progress, green for done.
const props = defineProps<{ type: WorkflowState['type']; color: string }>()

const PALETTE: Partial<Record<WorkflowState['type'], string>> = {
  unstarted: 'var(--fg-muted)',
  started: 'var(--warning)',
  completed: 'var(--accent)',
}
const tone = computed(() => PALETTE[props.type] ?? 'var(--fg-faint)')
</script>

<template>
  <svg class="size-3.5 shrink-0" viewBox="0 0 14 14" fill="none" aria-hidden="true">
    <circle
      v-if="type === 'backlog'"
      cx="7" cy="7" r="6"
      :stroke="tone" stroke-width="1.5" stroke-dasharray="1.4 1.74"
    />
    <circle v-else-if="type === 'unstarted'" cx="7" cy="7" r="6" :stroke="tone" stroke-width="1.5" />
    <template v-else-if="type === 'started'">
      <circle cx="7" cy="7" r="6" :stroke="tone" stroke-width="1.5" />
      <path d="M7 3.5a3.5 3.5 0 0 1 0 7z" :fill="tone" />
    </template>
    <template v-else-if="type === 'completed'">
      <circle cx="7" cy="7" r="7" :fill="tone" />
      <path d="M4.2 7.2 6.1 9l3.7-4" stroke="var(--bg)" stroke-width="1.5" stroke-linecap="round" stroke-linejoin="round" />
    </template>
    <template v-else>
      <circle cx="7" cy="7" r="7" :fill="tone" />
      <path d="m4.8 4.8 4.4 4.4m0-4.4L4.8 9.2" stroke="var(--bg)" stroke-width="1.5" stroke-linecap="round" />
    </template>
  </svg>
</template>
