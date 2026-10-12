<script setup lang="ts">
import { Trash2 } from 'lucide-vue-next'
import { computed, ref } from 'vue'

import SettingsPage from '@/components/SettingsPage.vue'
import BaseButton from '@/components/ui/BaseButton.vue'
import FormError from '@/components/ui/FormError.vue'
import { LABEL_COLORS } from '@/lib/issues'
import { useSyncStore } from '@/stores/sync'
import { useWorkspaceStore } from '@/stores/workspace'
import type { Label } from '@/sync/types'

const sync = useSyncStore()
const workspace = useWorkspaceStore()

const name = ref('')
// Start on a color no recent label used, so labels are easy to tell apart.
const color = ref<string>(LABEL_COLORS[sync.labels.length % LABEL_COLORS.length]!)
const error = ref('')

function nameTaken(candidate: string, except?: string) {
  const wanted = candidate.trim().toLowerCase()
  return sync.labels.some((l) => l.id !== except && l.name.toLowerCase() === wanted)
}

// How many issues use each label, counting only issues this user can see.
const usage = computed(() => {
  const counts = new Map<string, number>()
  for (const issue of sync.issues) {
    for (const id of issue.labelIds) counts.set(id, (counts.get(id) ?? 0) + 1)
  }
  return counts
})

function create() {
  error.value = ''
  if (!name.value.trim()) return
  if (nameTaken(name.value)) {
    error.value = 'A label with that name already exists.'
    return
  }
  sync.createLabel(name.value, color.value)
  name.value = ''
  color.value = LABEL_COLORS[(LABEL_COLORS.indexOf(color.value as (typeof LABEL_COLORS)[number]) + 1) % LABEL_COLORS.length]!
}

function rename(label: Label, event: Event) {
  const input = event.target as HTMLInputElement
  const next = input.value.trim()
  if (!next || next === label.name || nameTaken(next, label.id)) {
    input.value = label.name
    return
  }
  sync.updateLabel(label.id, { name: next })
}

function recolor(label: Label, event: Event) {
  sync.updateLabel(label.id, { color: (event.target as HTMLInputElement).value })
}

function remove(label: Label) {
  const used = usage.value.get(label.id) ?? 0
  const detail = used > 0 ? ` It will be removed from ${used === 1 ? '1 issue' : `${used} issues`}.` : ''
  if (window.confirm(`Delete the label "${label.name}"?${detail}`)) sync.deleteLabel(label.id)
}
</script>

<template>
  <SettingsPage title="Labels">
    <form v-if="workspace.canManageLabels" class="flex flex-wrap items-center gap-2" @submit.prevent="create">
      <input
        v-model="color"
        type="color"
        aria-label="Label color"
        class="size-8 shrink-0 cursor-pointer rounded-md border border-border-strong bg-elevated p-1"
      />
      <input
        v-model="name"
        aria-label="Label name"
        placeholder="Label name"
        required
        maxlength="50"
        class="h-8 min-w-48 flex-1 rounded-md border border-border-strong bg-elevated px-3 text-fg outline-none placeholder:text-faint focus:border-accent"
      />
      <BaseButton type="submit" variant="primary">Create label</BaseButton>
    </form>
    <FormError :message="error" />

    <ul v-if="sync.labels.length > 0" class="divide-y divide-border rounded-lg border border-border" aria-label="Labels">
      <li v-for="label in sync.labels" :key="label.id" class="flex h-12 items-center gap-3 px-4">
        <template v-if="workspace.canManageLabels">
          <input
            type="color"
            :value="label.color"
            :aria-label="`Color of ${label.name}`"
            class="size-6 shrink-0 cursor-pointer rounded border border-border-strong bg-elevated p-0.5"
            @change="recolor(label, $event)"
          />
          <input
            :value="label.name"
            :aria-label="`Name of ${label.name}`"
            maxlength="50"
            class="h-8 min-w-0 flex-1 rounded border border-transparent bg-transparent px-2 font-medium text-fg outline-none hover:border-border-strong focus:border-accent"
            @change="rename(label, $event)"
            @keydown.enter.prevent="($event.target as HTMLInputElement).blur()"
          />
        </template>
        <template v-else>
          <span class="size-3 shrink-0 rounded-full" :style="{ background: label.color }" aria-hidden="true" />
          <span class="min-w-0 flex-1 truncate font-medium">{{ label.name }}</span>
        </template>
        <span class="shrink-0 text-xs text-faint">
          {{ usage.get(label.id) ?? 0 }} {{ (usage.get(label.id) ?? 0) === 1 ? 'issue' : 'issues' }}
        </span>
        <button
          v-if="workspace.canManageLabels"
          type="button"
          class="flex size-7 shrink-0 items-center justify-center rounded text-faint hover:bg-hover hover:text-danger"
          :aria-label="`Delete ${label.name}`"
          @click="remove(label)"
        >
          <Trash2 class="size-3.5" aria-hidden="true" />
        </button>
      </li>
    </ul>
    <p v-else class="text-muted">No labels yet.</p>
  </SettingsPage>
</template>
