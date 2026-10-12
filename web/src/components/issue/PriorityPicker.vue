<script setup lang="ts">
import PickerMenu from '@/components/PickerMenu.vue'
import PriorityIcon from '@/components/PriorityIcon.vue'
import { PRIORITIES, priorityLabel } from '@/lib/issues'
import { type PickerVariant, TRIGGER_CLASS } from '@/lib/pickerVariants'

defineProps<{ priority: number; variant?: PickerVariant }>()
const emit = defineEmits<{ update: [priority: number] }>()

const options = PRIORITIES.map((p) => ({ key: p.value, label: p.label }))
</script>

<template>
  <PickerMenu
    :options="options"
    :selected="[priority]"
    :label="`Priority: ${priorityLabel(priority)}. Change priority`"
    :trigger-class="[TRIGGER_CLASS[variant ?? 'icon'], priority === 0 ? 'text-faint' : 'text-muted'].join(' ')"
    @select="emit('update', $event)"
  >
    <PriorityIcon :priority="priority" />
    <span v-if="variant && variant !== 'icon'" class="truncate" :class="{ 'text-fg': priority !== 0 }">
      {{ priority === 0 ? 'Priority' : priorityLabel(priority) }}
    </span>
    <template #icon="{ option }">
      <span class="text-muted"><PriorityIcon :priority="option.key" /></span>
    </template>
  </PickerMenu>
</template>
