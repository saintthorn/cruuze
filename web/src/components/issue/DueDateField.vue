<script setup lang="ts">
import { Calendar, X } from 'lucide-vue-next'
import { useId } from 'vue'

import { isOverdue } from '@/lib/issues'
import { type PickerVariant, TRIGGER_CLASS } from '@/lib/pickerVariants'

const props = withDefaults(
  defineProps<{
    dueDate?: string
    variant?: PickerVariant
    /** Show a past date as overdue. Off for dates that aren't deadlines, like a start date. */
    warnOverdue?: boolean
  }>(),
  { warnOverdue: true },
)

const overdue = () => props.warnOverdue && props.dueDate !== undefined && isOverdue(props.dueDate)
const emit = defineEmits<{ update: [dueDate: string | null] }>()

const id = useId()

function onChange(event: Event) {
  const value = (event.target as HTMLInputElement).value
  emit('update', value || null)
}
</script>

<template>
  <div :class="[TRIGGER_CLASS[variant ?? 'field'], 'focus-within:bg-hover']">
    <Calendar class="size-3.5 shrink-0" :class="overdue() ? 'text-danger' : 'text-faint'" aria-hidden="true" />
    <label :for="id" class="sr-only">Due date</label>
    <input
      :id="id"
      type="date"
      :value="dueDate ?? ''"
      class="min-w-0 flex-1 bg-transparent outline-none"
      :class="dueDate ? (overdue() ? 'text-danger' : 'text-fg') : 'text-muted'"
      @change="onChange"
    />
    <button
      v-if="dueDate"
      type="button"
      class="flex size-5 shrink-0 items-center justify-center rounded text-faint hover:bg-active hover:text-fg"
      aria-label="Clear due date"
      @click="emit('update', null)"
    >
      <X class="size-3" aria-hidden="true" />
    </button>
  </div>
</template>
