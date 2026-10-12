<script setup lang="ts">
import { RefreshCw } from 'lucide-vue-next'
import { computed } from 'vue'

import PickerMenu from '@/components/PickerMenu.vue'
import { formatDateRange, todayUTC } from '@/lib/issues'
import { type PickerVariant, TRIGGER_CLASS } from '@/lib/pickerVariants'
import { useSyncStore } from '@/stores/sync'

const props = defineProps<{ teamId: string; cycleId?: string; variant?: PickerVariant }>()
const emit = defineEmits<{ update: [cycleId: string | null] }>()

const sync = useSyncStore()

const NONE = ''
const current = computed(() => sync.cycle(props.cycleId))
// The current and upcoming cycles, plus the issue's own if it is a past one.
const options = computed(() => {
  const today = todayUTC()
  const cycles = sync.cyclesForTeam(props.teamId).filter((c) => c.endsAt >= today || c.id === props.cycleId)
  return [
    { key: NONE, label: 'No cycle' },
    ...cycles.map((c) => ({
      key: c.id,
      label: `${sync.cycleName(c)} · ${formatDateRange(c.startsAt, c.endsAt)}${c.startsAt <= today && today <= c.endsAt ? ' · Current' : ''}`,
    })),
  ]
})
</script>

<template>
  <PickerMenu
    :options="options"
    :selected="[current?.id ?? NONE]"
    :label="current ? `Cycle: ${sync.cycleName(current)}. Change cycle` : 'No cycle. Add to cycle'"
    :trigger-class="TRIGGER_CLASS[variant ?? 'field']"
    @select="emit('update', $event === NONE ? null : $event)"
  >
    <RefreshCw class="size-3.5 shrink-0 text-faint" aria-hidden="true" />
    <span class="truncate" :class="{ 'text-muted': !current }">{{ current ? sync.cycleName(current) : 'Cycle' }}</span>
  </PickerMenu>
</template>
