<script setup lang="ts">
import { computed } from 'vue'

import PickerMenu from '@/components/PickerMenu.vue'
import StateIcon from '@/components/StateIcon.vue'
import { issueIdentifier } from '@/lib/issues'
import { useSyncStore } from '@/stores/sync'

const props = defineProps<{
  /** Accessible name and tooltip of the trigger. */
  label: string
  /** Issues that can't be picked, e.g. the issue itself. */
  exclude?: string[]
  triggerClass?: string
}>()
const emit = defineEmits<{ select: [issueId: string] }>()

const sync = useSyncStore()

const options = computed(() =>
  sync.allIssues
    .filter((i) => !props.exclude?.includes(i.id))
    .map((i) => ({ key: i.id, label: `${issueIdentifier(i, sync.team(i.teamId))} ${i.title}` })),
)
const stateOf = (issueId: string) => sync.state(sync.issue(issueId)?.stateId ?? '')
</script>

<template>
  <PickerMenu :options="options" :selected="[]" :label="label" :trigger-class="triggerClass" searchable @select="emit('select', $event)">
    <slot />
    <template #icon="{ option }">
      <StateIcon v-if="stateOf(option.key)" :type="stateOf(option.key)!.type" :color="stateOf(option.key)!.color" />
    </template>
  </PickerMenu>
</template>
