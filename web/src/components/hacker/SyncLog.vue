<script setup lang="ts">
import { hex } from '@/lib/hacker'
import { useSyncStore } from '@/stores/sync'

/** The last few changes received over the sync socket, like the tail of a log. */
const sync = useSyncStore()

const VERBS = { I: 'add', U: 'mod', D: 'del' } as const
</script>

<template>
  <section aria-labelledby="sync-log-heading" class="sync-log px-2 text-[11px] leading-[18px] tabular-nums">
    <h2 id="sync-log-heading" class="text-xs font-medium text-faint">sync.log</h2>
    <ol class="flex max-h-[90px] flex-col overflow-hidden">
      <li v-for="entry in sync.feed" :key="entry.syncId" class="flex gap-2">
        <span class="text-faint">{{ hex(entry.syncId) }}</span>
        <span :class="entry.action === 'D' ? 'text-danger' : entry.action === 'I' ? 'text-accent' : 'text-warning'">{{ VERBS[entry.action] }}</span>
        <span class="truncate text-muted">{{ entry.model }}</span>
      </li>
      <li v-if="sync.feed.length === 0" class="text-faint">waiting for changes</li>
    </ol>
  </section>
</template>
