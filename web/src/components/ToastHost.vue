<script setup lang="ts">
import { X } from 'lucide-vue-next'

import { useToastStore } from '@/stores/toasts'

const toasts = useToastStore()
</script>

<template>
  <div role="status" aria-live="polite" class="fixed right-4 bottom-4 z-50 flex w-80 max-w-[calc(100%-2rem)] flex-col gap-2">
    <div
      v-for="toast in toasts.toasts"
      :key="toast.id"
      class="flex items-start gap-2 rounded-lg border border-border-strong bg-elevated p-3 shadow-xl"
    >
      <span class="shrink-0 text-accent" aria-hidden="true">[!]</span>
      <p class="min-w-0 flex-1">{{ toast.message }}</p>
      <button
        type="button"
        class="flex size-5 shrink-0 items-center justify-center rounded text-faint hover:bg-hover hover:text-fg"
        aria-label="Dismiss"
        @click="toasts.dismiss(toast.id)"
      >
        <X class="size-3.5" aria-hidden="true" />
      </button>
    </div>
  </div>
</template>
