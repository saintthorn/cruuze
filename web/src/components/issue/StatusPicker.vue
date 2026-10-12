<script setup lang="ts">
import { computed } from 'vue'

import PickerMenu from '@/components/PickerMenu.vue'
import StateIcon from '@/components/StateIcon.vue'
import { type PickerVariant, TRIGGER_CLASS } from '@/lib/pickerVariants'
import type { WorkflowState } from '@/sync/types'

const props = defineProps<{ states: WorkflowState[]; stateId: string; variant?: PickerVariant }>()
const emit = defineEmits<{ update: [stateId: string] }>()

const current = computed(() => props.states.find((s) => s.id === props.stateId))
const options = computed(() => props.states.map((s) => ({ key: s.id, label: s.name })))
const byId = (id: string) => props.states.find((s) => s.id === id)
</script>

<template>
  <PickerMenu
    :options="options"
    :selected="[stateId]"
    :label="`Status: ${current?.name ?? 'Unknown'}. Change status`"
    :trigger-class="TRIGGER_CLASS[variant ?? 'icon']"
    @select="emit('update', $event)"
  >
    <StateIcon v-if="current" :type="current.type" :color="current.color" />
    <span v-if="variant && variant !== 'icon'" class="truncate">{{ current?.name ?? 'Status' }}</span>
    <template #icon="{ option }">
      <StateIcon v-if="byId(option.key)" :type="byId(option.key)!.type" :color="byId(option.key)!.color" />
    </template>
  </PickerMenu>
</template>
