<script setup lang="ts">
import { Columns3, List, SlidersHorizontal } from 'lucide-vue-next'
import { PopoverContent, PopoverPortal, PopoverRoot, PopoverTrigger } from 'reka-ui'
import { useId } from 'vue'

import { GROUPINGS, ORDERINGS } from '@/lib/issues'
import { useUiStore } from '@/stores/ui'
import type { ViewOptions } from '@/stores/viewOptions'

defineProps<{
  /** The view's options; changed in place. */
  options: ViewOptions
  /** Whether this view can be shown as a board. */
  board?: boolean
}>()

const ui = useUiStore()
const id = useId()
const selectClass =
  'h-7 rounded-md border border-border-strong bg-elevated px-2 text-xs text-fg outline-none focus:border-accent'
</script>

<template>
  <PopoverRoot>
    <PopoverTrigger
      class="inline-flex h-7 items-center gap-1.5 rounded-md border border-border-strong bg-elevated px-2 text-xs font-medium hover:bg-hover"
    >
      <SlidersHorizontal class="size-3.5" aria-hidden="true" /> Display
    </PopoverTrigger>
    <PopoverPortal>
      <PopoverContent
        align="end"
        :side-offset="4"
        class="z-50 flex w-64 flex-col gap-3 rounded-lg border border-border-strong bg-elevated p-3 shadow-xl"
      >
        <div v-if="board" role="radiogroup" aria-label="Layout" class="grid grid-cols-2 gap-1 rounded-md bg-active p-0.5">
          <button
            v-for="layout in ['list', 'board'] as const"
            :key="layout"
            type="button"
            role="radio"
            :aria-checked="options.layout === layout"
            class="flex h-7 items-center justify-center gap-1.5 rounded text-xs font-medium capitalize"
            :class="options.layout === layout ? 'bg-elevated text-fg shadow-sm' : 'text-muted hover:text-fg'"
            @click="options.layout = layout"
          >
            <component :is="layout === 'list' ? List : Columns3" class="size-3.5" aria-hidden="true" /> {{ layout }}
          </button>
        </div>
        <div v-if="!board || options.layout === 'list'" class="flex items-center justify-between gap-3">
          <label :for="`${id}-grouping`" class="text-xs text-muted">Grouping</label>
          <select :id="`${id}-grouping`" v-model="options.grouping" :class="selectClass">
            <option v-for="option in GROUPINGS" :key="option.value" :value="option.value">{{ option.label }}</option>
          </select>
        </div>
        <div class="flex items-center justify-between gap-3">
          <label :for="`${id}-ordering`" class="text-xs text-muted">Ordering</label>
          <select :id="`${id}-ordering`" v-model="options.ordering" :class="selectClass">
            <option v-for="option in ORDERINGS" :key="option.value" :value="option.value">{{ option.label }}</option>
          </select>
        </div>
        <label class="flex items-center justify-between gap-3 text-xs text-muted">
          Show completed issues
          <input v-model="options.showClosed" type="checkbox" class="size-3.5 accent-(--accent)" />
        </label>
        <button
          type="button"
          class="h-7 rounded-md border border-border-strong text-xs font-medium hover:bg-hover"
          @click="ui.saveViewOpen = true"
        >
          Save as view…
        </button>
      </PopoverContent>
    </PopoverPortal>
  </PopoverRoot>
</template>
