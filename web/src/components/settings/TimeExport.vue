<script setup lang="ts">
import { Download } from 'lucide-vue-next'
import { computed, ref } from 'vue'

import { useWorkspaceStore } from '@/stores/workspace'

/** A timesheet as CSV: everyone's for an admin, your own otherwise. */
const workspace = useWorkspaceStore()

const iso = (date: Date) => date.toLocaleDateString('en-CA')
const today = new Date()
const from = ref(iso(new Date(today.getFullYear(), today.getMonth(), 1)))
const to = ref(iso(today))
const valid = computed(() => from.value !== '' && to.value !== '' && from.value <= to.value)
const href = computed(
  () => `/api/workspaces/${workspace.current?.id}/time-entries.csv?from=${from.value}&to=${to.value}`,
)
</script>

<template>
  <section aria-labelledby="time-export-heading" class="rounded-lg border border-border p-4">
    <h2 id="time-export-heading" class="font-medium">Time</h2>
    <p class="mt-0.5 text-muted">{{ workspace.isAdmin ? 'Everyone’s logged time, as CSV.' : 'Your logged time, as CSV.' }}</p>
    <div class="mt-3 flex flex-wrap items-center gap-2">
      <input v-model="from" type="date" aria-label="From" :max="to" class="h-8 rounded-md border border-border-strong bg-elevated px-2 outline-none focus:border-accent" />
      <span class="text-faint">to</span>
      <input v-model="to" type="date" aria-label="To" :min="from" class="h-8 rounded-md border border-border-strong bg-elevated px-2 outline-none focus:border-accent" />
      <a
        :href="valid ? href : undefined"
        download
        class="flex h-8 items-center gap-1.5 rounded-md border border-border-strong px-3 font-medium hover:bg-hover"
        :class="{ 'pointer-events-none opacity-50': !valid }"
        :aria-disabled="!valid"
      >
        <Download class="size-3.5" aria-hidden="true" /> Export CSV
      </a>
    </div>
  </section>
</template>
