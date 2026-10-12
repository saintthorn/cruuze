<script setup lang="ts">
import { computed } from 'vue'
import { RouterLink, useRoute } from 'vue-router'

import { useNow } from '@/composables/useNow'
import { usePing } from '@/composables/usePing'
import { formatClock } from '@/lib/duration'
import { hex } from '@/lib/hacker'
import { issueIdentifier } from '@/lib/issues'
import { useSyncStore } from '@/stores/sync'
import { useWorkspaceStore } from '@/stores/workspace'

/**
 * The line along the bottom, in the manner of an editor's status line: the
 * state of the connection where an editor shows its mode, then where you
 * are, then a few exact readings of the app's own state.
 */
const route = useRoute()
const sync = useSyncStore()
const workspace = useWorkspaceStore()
const now = useNow()
const { latest: ping } = usePing()

const MODES = {
  loading: { label: 'BOOT', class: 'bg-border-strong text-fg' },
  connecting: { label: 'DIAL', class: 'bg-warning text-accent-fg' },
  live: { label: 'LIVE', class: 'bg-accent text-accent-fg' },
  offline: { label: 'DOWN', class: 'bg-danger text-accent-fg' },
} as const
const mode = computed(() => MODES[sync.status])

const done = computed(() => sync.closedCount)
const total = computed(() => sync.issues.length)
const percent = computed(() => (total.value === 0 ? 0 : Math.round((done.value / total.value) * 100)))

// The running timer, wherever you are in the app.
const timer = computed(() => {
  const entry = sync.runningTimer
  const issue = sync.issue(entry?.issueId)
  if (!entry || !issue) return undefined
  const ref = issue.number > 0 ? issueIdentifier(issue, sync.team(issue.teamId)) : issue.id
  return { ref, elapsed: formatClock((now.value - Date.parse(entry.startedAt)) / 1000) }
})

const clock = computed(() => new Date(now.value).toISOString().slice(11, 19))
</script>

<template>
  <footer
    aria-label="Status"
    class="flex h-6 shrink-0 items-center overflow-hidden border-t border-border bg-sidebar text-[11px] whitespace-nowrap text-muted tabular-nums"
  >
    <span class="flex h-full items-center px-2.5 font-semibold" :class="mode.class" :title="`Sync connection: ${sync.status}`">
      {{ mode.label }}
    </span>
    <span class="flex h-full items-center bg-active px-2.5 text-fg">{{ workspace.current?.urlKey }}</span>
    <span class="min-w-0 truncate px-2.5">~{{ route.path }}</span>
    <span v-if="timer" class="flex h-full shrink-0 items-center gap-1.5 border-l border-border px-2.5 text-accent">
      <RouterLink :to="{ name: 'issue', params: { issueRef: timer.ref } }" class="hover:underline" title="The issue being timed">
        rec {{ timer.ref }}
      </RouterLink>
      <span role="timer">{{ timer.elapsed }}</span>
      <button type="button" class="px-1 text-muted hover:text-fg" aria-label="Stop timer" title="Stop timer" @click="sync.stopTimer()">■</button>
    </span>

    <dl class="ml-auto flex h-full items-center gap-4 px-2.5">
      <div class="hidden gap-1.5 md:flex" title="Issues completed or canceled, of all issues">
        <dt class="text-faint">done</dt>
        <dd>{{ done }}/{{ total }} · {{ percent }}%</dd>
      </div>
      <div class="hidden gap-1.5 lg:flex" title="Round trip to the API">
        <dt class="text-faint">ping</dt>
        <dd>{{ ping === null ? '—' : `${ping}ms` }}</dd>
      </div>
      <div class="flex gap-1.5" title="Changes waiting to reach the server">
        <dt class="text-faint">txq</dt>
        <dd :class="{ 'text-accent': sync.pendingCount > 0 }">{{ sync.pendingCount }}</dd>
      </div>
      <div class="flex gap-1.5" title="Sync ID the local copy is current as of">
        <dt class="text-faint">seq</dt>
        <dd>{{ hex(sync.syncId) }}</dd>
      </div>
    </dl>
    <span class="flex h-full items-center bg-active px-2.5 text-fg" title="UTC">{{ clock }}Z</span>
  </footer>
</template>
