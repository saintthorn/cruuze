<script setup lang="ts">
import { ArrowDown, ArrowUp, Trash2 } from 'lucide-vue-next'
import { computed, ref } from 'vue'

import StateIcon from '@/components/StateIcon.vue'
import BaseButton from '@/components/ui/BaseButton.vue'
import FormError from '@/components/ui/FormError.vue'
import { useSyncStore } from '@/stores/sync'
import type { TeamView } from '@/stores/workspace'
import type { WorkflowState } from '@/sync/types'

/** The statuses a team's issues move through: add, rename, reorder, remove. */
const props = defineProps<{ team: TeamView; editable: boolean }>()

const sync = useSyncStore()

type Kind = Exclude<WorkflowState['type'], 'triage'>
const KINDS: { value: Kind; label: string; hint: string; color: string }[] = [
  { value: 'backlog', label: 'Backlog', hint: 'not planned yet', color: '#95a2b3' },
  { value: 'unstarted', label: 'Unstarted', hint: 'planned, not begun', color: '#e2e2e2' },
  { value: 'started', label: 'Started', hint: 'being worked on', color: '#f2c94c' },
  { value: 'completed', label: 'Completed', hint: 'done', color: '#5e6ad2' },
  { value: 'canceled', label: 'Canceled', hint: "won't be done", color: '#95a2b3' },
]
const kindLabel = (kind: WorkflowState['type']) => KINDS.find((k) => k.value === kind)?.label ?? 'Triage'

const states = computed(() => sync.statesForTeam(props.team.id))
const usage = computed(() => {
  const counts = new Map<string, number>()
  for (const issue of sync.allIssues) counts.set(issue.stateId, (counts.get(issue.stateId) ?? 0) + 1)
  return counts
})

const name = ref('')
const kind = ref<Kind>('started')
const error = ref('')

function taken(candidate: string, except?: string) {
  const wanted = candidate.trim().toLowerCase()
  return sync.statesForTeam(props.team.id, true).some((s) => s.id !== except && s.name.toLowerCase() === wanted)
}

function add() {
  error.value = ''
  const trimmed = name.value.trim()
  if (!trimmed) return
  if (taken(trimmed)) {
    error.value = 'Another status of this team already has that name.'
    return
  }
  const last = states.value[states.value.length - 1]
  sync.createRecord('WorkflowState', {
    id: crypto.randomUUID(),
    teamId: props.team.id,
    name: trimmed,
    type: kind.value,
    color: KINDS.find((k) => k.value === kind.value)!.color,
    position: (last?.position ?? 0) + 1,
  })
  name.value = ''
}

function rename(state: WorkflowState, event: Event) {
  const input = event.target as HTMLInputElement
  const next = input.value.trim()
  if (!next || next === state.name || taken(next, state.id)) {
    input.value = state.name
    return
  }
  sync.updateRecord('WorkflowState', state.id, { name: next })
}

/** Moves a status one place up or down by giving it a position between its new neighbours. */
function move(index: number, delta: -1 | 1) {
  const list = states.value
  const state = list[index]
  const target = list[index + delta]
  if (!state || !target) return
  const beyond = list[index + delta * 2]
  const position = beyond ? (target.position + beyond.position) / 2 : target.position + delta
  sync.updateRecord('WorkflowState', state.id, { position })
}

// The server has the final say (archived issues count too); these are the
// cases that can be told from here.
function blocked(state: WorkflowState): string {
  if ((usage.value.get(state.id) ?? 0) > 0) return 'Move its issues to another status first'
  const others = states.value.filter((s) => s.id !== state.id)
  if (!others.some((s) => s.type === 'backlog' || s.type === 'unstarted')) return 'New issues need a status to start in'
  if (!others.some((s) => s.type === 'completed')) return 'Finished issues need a status to end in'
  return ''
}

function remove(state: WorkflowState) {
  if (window.confirm(`Delete the status "${state.name}"?`)) sync.deleteRecord('WorkflowState', state.id)
}

const inputClass =
  'h-8 rounded-md border border-border-strong bg-elevated px-3 text-fg outline-none placeholder:text-faint focus:border-accent'
</script>

<template>
  <section aria-labelledby="workflow-heading" class="rounded-lg border border-border p-4">
    <h2 id="workflow-heading" class="font-medium">Workflow</h2>

    <ul aria-label="Statuses" class="mt-3 divide-y divide-border border-y border-border">
      <li v-for="(state, index) in states" :key="state.id" class="flex h-10 items-center gap-2">
        <StateIcon :type="state.type" :color="state.color" />
        <input
          :value="state.name"
          :aria-label="`Name of status ${state.name}`"
          :readonly="!editable"
          maxlength="40"
          class="h-7 min-w-0 flex-1 rounded border border-transparent bg-transparent px-1.5 text-fg outline-none focus:border-accent"
          :class="{ 'hover:border-border-strong': editable }"
          @change="rename(state, $event)"
          @keydown.enter.prevent="($event.target as HTMLInputElement).blur()"
        />
        <span class="w-20 shrink-0 text-xs text-faint">{{ kindLabel(state.type) }}</span>
        <span class="w-16 shrink-0 text-right text-xs text-faint tabular-nums">
          {{ usage.get(state.id) ?? 0 }} {{ (usage.get(state.id) ?? 0) === 1 ? 'issue' : 'issues' }}
        </span>
        <template v-if="editable">
          <button
            type="button"
            class="flex size-6 items-center justify-center rounded text-faint hover:bg-hover hover:text-fg disabled:opacity-30"
            :disabled="index === 0"
            :aria-label="`Move ${state.name} up`"
            @click="move(index, -1)"
          >
            <ArrowUp class="size-3.5" aria-hidden="true" />
          </button>
          <button
            type="button"
            class="flex size-6 items-center justify-center rounded text-faint hover:bg-hover hover:text-fg disabled:opacity-30"
            :disabled="index === states.length - 1"
            :aria-label="`Move ${state.name} down`"
            @click="move(index, 1)"
          >
            <ArrowDown class="size-3.5" aria-hidden="true" />
          </button>
          <button
            type="button"
            class="flex size-6 items-center justify-center rounded text-faint hover:bg-hover hover:text-danger disabled:opacity-30 disabled:hover:text-faint"
            :disabled="blocked(state) !== ''"
            :title="blocked(state) || undefined"
            :aria-label="`Delete status ${state.name}`"
            @click="remove(state)"
          >
            <Trash2 class="size-3.5" aria-hidden="true" />
          </button>
        </template>
      </li>
    </ul>

    <form v-if="editable" class="mt-3 flex flex-wrap gap-2" @submit.prevent="add">
      <input v-model="name" aria-label="New status name" placeholder="New status, e.g. In Review" maxlength="40" required class="min-w-40 flex-1" :class="inputClass" />
      <select v-model="kind" aria-label="Kind of status" :class="inputClass" class="!px-2">
        <option v-for="option in KINDS" :key="option.value" :value="option.value">{{ option.label }} ({{ option.hint }})</option>
      </select>
      <BaseButton type="submit" :disabled="!name.trim()">Add status</BaseButton>
    </form>
    <FormError :message="error" class="mt-2" />
  </section>
</template>
