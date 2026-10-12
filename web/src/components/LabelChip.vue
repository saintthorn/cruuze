<script setup lang="ts">
import type { Label } from '@/sync/types'

defineProps<{ label: Label }>()
</script>

<template>
  <span class="inline-flex h-5 max-w-32 shrink-0 items-center gap-1.5 border border-border-strong px-1.5 text-xs text-muted">
    <span class="size-2 shrink-0 rounded-full" :style="{ background: label.color }" aria-hidden="true" />
    <span class="truncate">{{ label.name }}</span>
  </span>
</template>
