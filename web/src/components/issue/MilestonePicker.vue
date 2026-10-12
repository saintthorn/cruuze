<script setup lang="ts">
import { Milestone as MilestoneIcon } from 'lucide-vue-next'
import { computed } from 'vue'

import PickerMenu from '@/components/PickerMenu.vue'
import { type PickerVariant, TRIGGER_CLASS } from '@/lib/pickerVariants'
import { useSyncStore } from '@/stores/sync'

const props = defineProps<{ projectId: string; milestoneId?: string; variant?: PickerVariant }>()
const emit = defineEmits<{ update: [milestoneId: string | null] }>()

const sync = useSyncStore()

const NONE = ''
const current = computed(() => sync.milestone(props.milestoneId))
const options = computed(() => [
  { key: NONE, label: 'No milestone' },
  ...sync.milestonesForProject(props.projectId).map((m) => ({ key: m.id, label: m.name })),
])
</script>

<template>
  <PickerMenu
    :options="options"
    :selected="[current?.id ?? NONE]"
    :label="current ? `Milestone: ${current.name}. Change milestone` : 'No milestone. Set milestone'"
    :trigger-class="TRIGGER_CLASS[variant ?? 'field']"
    @select="emit('update', $event === NONE ? null : $event)"
  >
    <MilestoneIcon class="size-3.5 shrink-0 text-faint" aria-hidden="true" />
    <span class="truncate" :class="{ 'text-muted': !current }">{{ current?.name ?? 'Milestone' }}</span>
  </PickerMenu>
</template>
