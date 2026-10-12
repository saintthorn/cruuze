<script setup lang="ts">
import { Play, Plus, Square, X } from 'lucide-vue-next'
import { computed, nextTick, ref, useTemplateRef } from 'vue'

import { useNow } from '@/composables/useNow'
import { formatClock, formatDuration, parseDuration, timeInProgress } from '@/lib/duration'
import { useSyncStore } from '@/stores/sync'
import type { Issue } from '@/sync/types'

const props = defineProps<{
  issue: Issue
  /** Whether the issue's activity is loaded: time in progress is read from it. */
  detailsLoaded: boolean
}>()

const sync = useSyncStore()
const now = useNow()

const entries = computed(() => sync.timeEntriesForIssue(props.issue.id))
const finished = computed(() => entries.value.filter((entry) => entry.endedAt))
const running = computed(() => (sync.runningTimer?.issueId === props.issue.id ? sync.runningTimer : undefined))
const elapsed = computed(() => (running.value ? (now.value - Date.parse(running.value.startedAt)) / 1000 : 0))

const inProgress = computed(() =>
  props.detailsLoaded ? timeInProgress(props.issue, sync.activitiesForIssue(props.issue.id), (id) => (id ? sync.state(id) : undefined), now.value) : null,
)
// My own time shows at once; the total for everyone follows from the server.
const mine = computed(() => finished.value.reduce((sum, entry) => sum + entry.seconds, 0))
const logged = computed(() => Math.max(props.issue.loggedSeconds ?? 0, mine.value))

const today = () => new Date().toLocaleDateString('en-CA')
const adding = ref(false)
const amount = ref('')
const note = ref('')
const day = ref(today())
const amountField = useTemplateRef('amountField')
const seconds = computed(() => parseDuration(amount.value))

async function openForm() {
  adding.value = true
  amount.value = ''
  note.value = ''
  day.value = today()
  await nextTick()
  amountField.value?.focus()
}

function submit() {
  if (seconds.value === null || !day.value || day.value > today()) return
  sync.logTime(props.issue.id, seconds.value, note.value, day.value)
  adding.value = false
}

const entryDay = (iso: string) => new Date(iso).toLocaleDateString('en-US', { month: 'short', day: 'numeric' })
</script>

<template>
  <section aria-labelledby="time-heading" class="mt-3 flex flex-col gap-1 border-t border-border pt-3">
    <h2 id="time-heading" class="px-2 pb-1 text-xs font-medium text-faint">Time</h2>
    <dl class="flex flex-col gap-1 px-2 text-[13px] tabular-nums">
      <div class="flex justify-between gap-2" title="Time in started statuses">
        <dt class="text-muted">In progress</dt>
        <dd>{{ inProgress === null ? '…' : inProgress > 0 ? formatDuration(inProgress) : '—' }}</dd>
      </div>
      <div class="flex justify-between gap-2" title="Time logged on this issue by everyone">
        <dt class="text-muted">Logged</dt>
        <dd>{{ logged > 0 ? formatDuration(logged) : '—' }}</dd>
      </div>
    </dl>

    <template v-if="issue.number > 0 && !issue.archivedAt">
      <button
        v-if="running"
        type="button"
        class="mt-1 flex h-8 w-full items-center gap-2 rounded px-2 text-left text-[13px] text-accent hover:bg-hover"
        @click="sync.stopTimer()"
      >
        <Square class="size-3.5 shrink-0" aria-hidden="true" />
        <span>Stop timer</span>
        <span class="ml-auto tabular-nums" role="timer">{{ formatClock(elapsed) }}</span>
      </button>
      <button
        v-else
        type="button"
        class="mt-1 flex h-8 w-full items-center gap-2 rounded px-2 text-left text-[13px] hover:bg-hover"
        :title="sync.runningTimer ? 'Stops the timer running on another issue' : undefined"
        @click="sync.startTimer(issue.id)"
      >
        <Play class="size-3.5 shrink-0 text-faint" aria-hidden="true" />
        <span class="text-muted">Start timer</span>
      </button>

      <form v-if="adding" class="flex flex-col gap-1.5 px-2 py-1" @submit.prevent="submit" @keydown.esc.stop="adding = false">
        <input
          ref="amountField"
          v-model="amount"
          aria-label="Time spent"
          placeholder="1h 30m"
          autocomplete="off"
          class="h-7 rounded border border-border-strong bg-elevated px-2 text-[13px] outline-none placeholder:text-faint focus:border-accent"
        />
        <input
          v-model="note"
          aria-label="Note"
          placeholder="Note (optional)"
          maxlength="500"
          autocomplete="off"
          class="h-7 rounded border border-border-strong bg-elevated px-2 text-[13px] outline-none placeholder:text-faint focus:border-accent"
        />
        <input
          v-model="day"
          type="date"
          aria-label="Day"
          :max="today()"
          class="h-7 rounded border border-border-strong bg-elevated px-2 text-[13px] outline-none focus:border-accent"
        />
        <div class="flex items-center gap-2 text-xs">
          <button type="submit" class="rounded bg-accent px-2 py-1 font-medium text-accent-fg disabled:opacity-50" :disabled="seconds === null">
            Log {{ seconds === null ? 'time' : formatDuration(seconds) }}
          </button>
          <button type="button" class="rounded px-2 py-1 text-muted hover:text-fg" @click="adding = false">Cancel</button>
        </div>
      </form>
      <button
        v-else
        type="button"
        class="flex h-8 w-full items-center gap-2 rounded px-2 text-left text-[13px] hover:bg-hover"
        @click="openForm"
      >
        <Plus class="size-3.5 shrink-0 text-faint" aria-hidden="true" />
        <span class="text-muted">Log time</span>
      </button>
    </template>

    <ul v-if="finished.length > 0" aria-label="Your time on this issue" class="mt-1 flex flex-col px-2 text-xs">
      <li v-for="entry in finished" :key="entry.id" class="group flex h-6 items-center gap-2">
        <span class="w-12 shrink-0 text-faint">{{ entryDay(entry.startedAt) }}</span>
        <span class="shrink-0 tabular-nums">{{ formatDuration(entry.seconds) }}</span>
        <span class="min-w-0 flex-1 truncate text-muted" :title="entry.note">{{ entry.note }}</span>
        <button
          type="button"
          class="shrink-0 rounded p-0.5 text-faint opacity-0 group-hover:opacity-100 hover:text-danger focus-visible:opacity-100"
          :aria-label="`Delete ${formatDuration(entry.seconds)} on ${entryDay(entry.startedAt)}`"
          @click="sync.deleteRecord('TimeEntry', entry.id)"
        >
          <X class="size-3" aria-hidden="true" />
        </button>
      </li>
    </ul>
  </section>
</template>
