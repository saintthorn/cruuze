<script setup lang="ts">
import type { Progress } from '@/lib/issues'

defineProps<{ progress: Progress }>()
</script>

<template>
  <span class="flex items-center gap-2 text-xs text-muted">
    <span
      class="h-1.5 w-20 overflow-hidden rounded-full bg-active"
      role="progressbar"
      :aria-valuenow="progress.percent"
      aria-valuemin="0"
      aria-valuemax="100"
      :aria-label="`${progress.done} of ${progress.total} issues completed`"
    >
      <span class="block h-full rounded-full bg-accent" :style="{ width: `${progress.percent}%` }" />
    </span>
    <span class="tabular-nums">{{ progress.percent }}%</span>
    <span class="text-faint tabular-nums">{{ progress.done }}/{{ progress.total }}</span>
  </span>
</template>
