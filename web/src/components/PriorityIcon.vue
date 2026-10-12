<script setup lang="ts">
defineProps<{ priority: number }>()
</script>

<template>
  <svg class="size-3.5 shrink-0" viewBox="0 0 14 14" fill="currentColor" aria-hidden="true">
    <!-- No priority: three dashes -->
    <template v-if="priority === 0">
      <rect x="1" y="6.25" width="3" height="1.5" rx="0.5" opacity="0.6" />
      <rect x="5.5" y="6.25" width="3" height="1.5" rx="0.5" opacity="0.6" />
      <rect x="10" y="6.25" width="3" height="1.5" rx="0.5" opacity="0.6" />
    </template>
    <!-- Urgent: filled square with an exclamation mark -->
    <template v-else-if="priority === 1">
      <rect x="1" y="1" width="12" height="12" rx="3" fill="#f2994a" />
      <rect x="6.25" y="3.5" width="1.5" height="4.5" rx="0.5" fill="var(--bg)" />
      <rect x="6.25" y="9.25" width="1.5" height="1.5" rx="0.5" fill="var(--bg)" />
    </template>
    <!-- High, medium, low: three, two or one bars lit -->
    <template v-else>
      <rect x="1.5" y="8" width="3" height="5" rx="1" />
      <rect x="5.5" y="5" width="3" height="8" rx="1" :opacity="priority <= 3 ? 1 : 0.3" />
      <rect x="9.5" y="2" width="3" height="11" rx="1" :opacity="priority <= 2 ? 1 : 0.3" />
    </template>
  </svg>
</template>
