<script setup lang="ts">
import { CircleUser } from 'lucide-vue-next'
import { computed } from 'vue'

import PickerMenu from '@/components/PickerMenu.vue'
import UserAvatar from '@/components/UserAvatar.vue'
import { type PickerVariant, TRIGGER_CLASS } from '@/lib/pickerVariants'
import { useSyncStore } from '@/stores/sync'

const props = defineProps<{ assigneeId?: string; variant?: PickerVariant }>()
const emit = defineEmits<{ update: [assigneeId: string | null] }>()

const sync = useSyncStore()

const NONE = ''
const assignee = computed(() => sync.user(props.assigneeId))
const name = computed(() => (assignee.value ? assignee.value.name || assignee.value.email : undefined))
const options = computed(() => [
  { key: NONE, label: 'No assignee' },
  ...sync.users.map((u) => ({ key: u.id, label: u.name || u.email })),
])
</script>

<template>
  <PickerMenu
    :options="options"
    :selected="[assigneeId ?? NONE]"
    :label="name ? `Assigned to ${name}. Change assignee` : 'Unassigned. Assign'"
    :trigger-class="TRIGGER_CLASS[variant ?? 'icon']"
    searchable
    align="end"
    @select="emit('update', $event === NONE ? null : $event)"
  >
    <UserAvatar v-if="assignee" :name="assignee.name" :email="assignee.email" :avatar-url="assignee.avatarUrl" :size="18" />
    <CircleUser v-else class="size-[18px] shrink-0 text-faint" aria-hidden="true" />
    <span v-if="variant && variant !== 'icon'" class="truncate" :class="{ 'text-muted': !assignee }">
      {{ name ?? 'Assignee' }}
    </span>
    <template #icon="{ option }">
      <UserAvatar
        v-if="sync.user(option.key)"
        :name="sync.user(option.key)!.name"
        :email="sync.user(option.key)!.email"
        :avatar-url="sync.user(option.key)!.avatarUrl"
        :size="18"
      />
      <CircleUser v-else class="size-[18px] shrink-0 text-faint" aria-hidden="true" />
    </template>
  </PickerMenu>
</template>
