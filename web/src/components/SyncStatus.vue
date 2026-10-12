<script setup lang="ts">
import { computed } from 'vue'

import { useSyncStore } from '@/stores/sync'

const sync = useSyncStore()

const label = computed(() => {
  const pending = sync.pendingCount
  const changes = pending === 1 ? '1 change' : `${pending} changes`
  if (sync.status === 'offline') {
    return pending > 0 ? `Offline · ${changes} will sync when you're back` : 'Offline · showing saved data'
  }
  if (sync.status === 'connecting') return 'Connecting…'
  return pending > 0 ? `Syncing ${changes}…` : ''
})
</script>

<template>
  <p role="status" class="flex items-center gap-2 px-2 text-xs text-muted" :class="{ 'min-h-7': label }">
    <template v-if="label">
      <span
        class="size-2 shrink-0 rounded-full"
        :class="sync.status === 'offline' ? 'bg-danger' : 'animate-pulse bg-accent'"
        aria-hidden="true"
      />
      {{ label }}
    </template>
  </p>
</template>
