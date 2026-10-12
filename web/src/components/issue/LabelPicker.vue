<script setup lang="ts">
import { Tag } from 'lucide-vue-next'
import { computed } from 'vue'

import PickerMenu from '@/components/PickerMenu.vue'
import { LABEL_COLORS } from '@/lib/issues'
import { type PickerVariant, TRIGGER_CLASS } from '@/lib/pickerVariants'
import { useSyncStore } from '@/stores/sync'
import { useWorkspaceStore } from '@/stores/workspace'

const props = defineProps<{ labelIds: string[]; variant?: PickerVariant }>()
const emit = defineEmits<{ add: [labelId: string]; remove: [labelId: string] }>()

const sync = useSyncStore()
const workspace = useWorkspaceStore()

const chosen = computed(() => sync.labelsById(props.labelIds))
const options = computed(() => sync.labels.map((l) => ({ key: l.id, label: l.name })))
const summary = computed(() => chosen.value.map((l) => l.name).join(', '))

function toggle(labelId: string) {
  if (props.labelIds.includes(labelId)) emit('remove', labelId)
  else emit('add', labelId)
}

function create(name: string) {
  // Cycle through the palette so new labels are easy to tell apart.
  const color = LABEL_COLORS[sync.labels.length % LABEL_COLORS.length]!
  const label = sync.createLabel(name, color)
  if (label) emit('add', label.id)
}

const colorOf = (id: string) => sync.labels.find((l) => l.id === id)?.color
</script>

<template>
  <PickerMenu
    :options="options"
    :selected="labelIds"
    :label="summary ? `Labels: ${summary}. Change labels` : 'No labels. Add labels'"
    :trigger-class="TRIGGER_CLASS[variant ?? 'field']"
    multiple
    searchable
    :creatable="workspace.canManageLabels"
    @select="toggle"
    @create="create"
  >
    <template v-if="chosen.length > 0">
      <span class="flex shrink-0 -space-x-0.5" aria-hidden="true">
        <span
          v-for="label in chosen.slice(0, 3)"
          :key="label.id"
          class="size-2.5 rounded-full ring-1 ring-elevated"
          :style="{ background: label.color }"
        />
      </span>
      <span class="truncate">{{ summary }}</span>
    </template>
    <template v-else>
      <Tag class="size-3.5 shrink-0 text-faint" aria-hidden="true" />
      <span class="text-muted">{{ variant === 'chip' ? 'Labels' : 'Add label' }}</span>
    </template>
    <template #icon="{ option }">
      <span class="size-2.5 shrink-0 rounded-full" :style="{ background: colorOf(option.key) }" aria-hidden="true" />
    </template>
  </PickerMenu>
</template>
