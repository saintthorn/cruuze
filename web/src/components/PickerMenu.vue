<script setup lang="ts" generic="K extends string | number">
import { Check, Plus } from 'lucide-vue-next'
import {
  DropdownMenuContent,
  DropdownMenuItem,
  DropdownMenuPortal,
  DropdownMenuRoot,
  DropdownMenuTrigger,
} from 'reka-ui'
import { computed, nextTick, ref, useTemplateRef } from 'vue'

export interface PickerOption<Key> {
  key: Key
  label: string
}

const props = defineProps<{
  options: PickerOption<K>[]
  /** Keys shown as checked. */
  selected: K[]
  /** Accessible name of the trigger, e.g. "Status: Todo. Change status". */
  label: string
  /** Stay open after choosing, for picking several. */
  multiple?: boolean
  /** Show a filter box above the options. */
  searchable?: boolean
  /** With `searchable`, offer to create an option from text that matches none. */
  creatable?: boolean
  align?: 'start' | 'end'
  triggerClass?: string
}>()

const emit = defineEmits<{ select: [key: K]; create: [name: string] }>()

defineSlots<{
  /** The trigger's content. */
  default(): unknown
  /** Shown before each option's label. */
  icon(props: { option: PickerOption<K> }): unknown
}>()

const query = ref('')
const search = useTemplateRef('search')

const filtered = computed(() => {
  const q = query.value.trim().toLowerCase()
  return q ? props.options.filter((o) => o.label.toLowerCase().includes(q)) : props.options
})

const canCreate = computed(() => {
  const q = query.value.trim().toLowerCase()
  return props.creatable && q !== '' && !props.options.some((o) => o.label.toLowerCase() === q)
})

async function onOpenChange(open: boolean) {
  if (!open) return
  query.value = ''
  if (props.searchable) {
    await nextTick()
    search.value?.focus()
  }
}

function choose(event: Event, key: K) {
  // Preventing the default keeps the menu open.
  if (props.multiple) event.preventDefault()
  emit('select', key)
}

function create(event?: Event) {
  if (props.multiple) event?.preventDefault()
  emit('create', query.value.trim())
  query.value = ''
}

function onSearchKeydown(event: KeyboardEvent) {
  if (event.key === 'Enter') {
    event.preventDefault()
    const first = filtered.value[0]
    if (first) emit('select', first.key)
    else if (canCreate.value) create()
    return
  }
  // Let the menu handle navigation and closing; keep typing out of its typeahead.
  if (!['ArrowDown', 'ArrowUp', 'Escape', 'Tab'].includes(event.key)) event.stopPropagation()
}
</script>

<template>
  <DropdownMenuRoot @update:open="onOpenChange">
    <DropdownMenuTrigger :class="triggerClass" :aria-label="label" :title="label">
      <slot />
    </DropdownMenuTrigger>
    <DropdownMenuPortal>
      <DropdownMenuContent
        :align="align ?? 'start'"
        :side-offset="4"
        class="z-50 max-h-80 min-w-48 overflow-y-auto rounded-lg border border-border-strong bg-elevated p-1 shadow-xl"
      >
        <input
          v-if="searchable"
          ref="search"
          v-model="query"
          :aria-label="creatable ? 'Filter or create' : 'Filter'"
          :placeholder="creatable ? 'Filter or create…' : 'Filter…'"
          class="mb-1 h-8 w-full border-b border-border bg-transparent px-2 text-[13px] text-fg outline-none placeholder:text-faint"
          @keydown="onSearchKeydown"
        />
        <DropdownMenuItem
          v-for="option in filtered"
          :key="option.key"
          class="flex h-8 items-center gap-2 rounded px-2 text-[13px] outline-none select-none data-[highlighted]:bg-active"
          @select="choose($event, option.key)"
        >
          <slot name="icon" :option="option" />
          <span class="truncate">{{ option.label }}</span>
          <Check v-if="selected.includes(option.key)" class="ml-auto size-4 shrink-0 text-muted" aria-label="Selected" />
        </DropdownMenuItem>
        <DropdownMenuItem
          v-if="canCreate"
          class="flex h-8 items-center gap-2 rounded px-2 text-[13px] outline-none select-none data-[highlighted]:bg-active"
          @select="create($event)"
        >
          <Plus class="size-3.5 text-muted" aria-hidden="true" />
          <span class="truncate">Create “{{ query.trim() }}”</span>
        </DropdownMenuItem>
        <p v-if="filtered.length === 0 && !canCreate" class="px-2 py-1.5 text-xs text-faint">No matches</p>
      </DropdownMenuContent>
    </DropdownMenuPortal>
  </DropdownMenuRoot>
</template>
