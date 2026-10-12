<script setup lang="ts">
import { Box } from 'lucide-vue-next'
import { computed } from 'vue'

import PickerMenu from '@/components/PickerMenu.vue'
import { type PickerVariant, TRIGGER_CLASS } from '@/lib/pickerVariants'
import { useSyncStore } from '@/stores/sync'

const props = defineProps<{ projectId?: string; variant?: PickerVariant }>()
const emit = defineEmits<{ update: [projectId: string | null] }>()

const sync = useSyncStore()

const NONE = ''
const current = computed(() => sync.project(props.projectId))
const options = computed(() => [{ key: NONE, label: 'No project' }, ...sync.projects.map((p) => ({ key: p.id, label: p.name }))])
</script>

<template>
  <PickerMenu
    :options="options"
    :selected="[current?.id ?? NONE]"
    :label="current ? `Project: ${current.name}. Change project` : 'No project. Add to project'"
    :trigger-class="TRIGGER_CLASS[variant ?? 'field']"
    searchable
    @select="emit('update', $event === NONE ? null : $event)"
  >
    <span v-if="current" class="size-2.5 shrink-0 rounded-sm" :style="{ background: current.color }" aria-hidden="true" />
    <Box v-else class="size-3.5 shrink-0 text-faint" aria-hidden="true" />
    <span class="truncate" :class="{ 'text-muted': !current }">{{ current?.name ?? 'Project' }}</span>
    <template #icon="{ option }">
      <span
        class="size-2.5 shrink-0 rounded-sm"
        :style="{ background: sync.project(option.key)?.color ?? 'transparent' }"
        aria-hidden="true"
      />
    </template>
  </PickerMenu>
</template>
