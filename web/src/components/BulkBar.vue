<script setup lang="ts">
import { Archive, CircleDot, CircleUser, Tag, X } from 'lucide-vue-next'

import { useIssueActions } from '@/composables/useIssueActions'
import { useIssueViewStore } from '@/stores/issueView'
import { type PaletteStep, usePaletteStore } from '@/stores/palette'

import PriorityIcon from './PriorityIcon.vue'

const view = useIssueViewStore()
const palette = usePaletteStore()
const actions = useIssueActions()

const buttons: { step: PaletteStep; label: string; key: string }[] = [
  { step: 'status', label: 'Status', key: 'S' },
  { step: 'priority', label: 'Priority', key: 'P' },
  { step: 'assignee', label: 'Assignee', key: 'A' },
  { step: 'labels', label: 'Labels', key: 'L' },
]

function archive() {
  actions.archive(view.targets)
  view.clearSelection()
}

const buttonClass = 'flex h-7 items-center gap-1.5 rounded-md px-2 text-xs hover:bg-hover'
</script>

<template>
  <div
    v-if="view.selected.size > 0"
    role="toolbar"
    aria-label="Selected issues"
    class="absolute bottom-5 left-1/2 z-20 flex -translate-x-1/2 items-center gap-1 rounded-xl border border-border-strong bg-elevated p-1.5 shadow-2xl"
  >
    <span class="px-2 text-xs font-medium whitespace-nowrap" aria-live="polite">{{ view.selected.size }} selected</span>
    <span class="h-4 w-px bg-border-strong" aria-hidden="true" />
    <button
      v-for="button in buttons"
      :key="button.step"
      type="button"
      :class="buttonClass"
      :title="`${button.label} (${button.key})`"
      @click="palette.show(button.step)"
    >
      <CircleDot v-if="button.step === 'status'" class="size-3.5 text-muted" aria-hidden="true" />
      <span v-else-if="button.step === 'priority'" class="text-muted"><PriorityIcon :priority="2" /></span>
      <CircleUser v-else-if="button.step === 'assignee'" class="size-3.5 text-muted" aria-hidden="true" />
      <Tag v-else class="size-3.5 text-muted" aria-hidden="true" />
      {{ button.label }}
    </button>
    <button type="button" :class="buttonClass" title="Archive (⌘⌫)" @click="archive">
      <Archive class="size-3.5 text-muted" aria-hidden="true" /> Archive
    </button>
    <span class="h-4 w-px bg-border-strong" aria-hidden="true" />
    <button
      type="button"
      class="flex size-7 items-center justify-center rounded-md text-muted hover:bg-hover hover:text-fg"
      aria-label="Clear selection"
      title="Clear selection (Esc)"
      @click="view.clearSelection()"
    >
      <X class="size-3.5" aria-hidden="true" />
    </button>
  </div>
</template>
