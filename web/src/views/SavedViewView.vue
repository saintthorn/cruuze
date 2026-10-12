<script setup lang="ts">
import { Trash2 } from 'lucide-vue-next'
import { computed, watch } from 'vue'
import { useRoute, useRouter } from 'vue-router'

import DisplayOptions from '@/components/DisplayOptions.vue'
import FavoriteButton from '@/components/FavoriteButton.vue'
import FilterMenu from '@/components/FilterMenu.vue'
import IssueView from '@/components/IssueView.vue'
import BaseButton from '@/components/ui/BaseButton.vue'
import { useSyncStore } from '@/stores/sync'
import { parseViewOptions, useViewOptionsStore } from '@/stores/viewOptions'

const route = useRoute()
const router = useRouter()
const sync = useSyncStore()
const viewOptions = useViewOptionsStore()

const view = computed(() => sync.view(String(route.params.viewId)))
const viewKey = computed(() => `view:${route.params.viewId}`)
const options = computed(() => viewOptions.optionsFor(viewKey.value))
const mine = computed(() => view.value?.ownerId === sync.currentUserId())

// Every time the view is opened it starts from what was saved.
watch(
  () => view.value?.id,
  () => {
    if (view.value) Object.assign(options.value, parseViewOptions(view.value.options))
  },
  { immediate: true },
)

const issues = computed(() => (view.value?.teamId ? sync.issuesForTeam(view.value.teamId) : sync.issues))
const states = computed(() =>
  (view.value?.teamId ? [view.value.teamId] : sync.teams.map((t) => t.id)).flatMap((id) => sync.statesForTeam(id)),
)

const changed = computed(
  () => view.value !== undefined && JSON.stringify(options.value) !== JSON.stringify(parseViewOptions(view.value.options)),
)

function saveChanges() {
  if (view.value) sync.updateRecord('View', view.value.id, { options: JSON.parse(JSON.stringify(options.value)) })
}

function remove() {
  if (!view.value || !window.confirm(`Delete the view "${view.value.name}"?`)) return
  sync.deleteRecord('View', view.value.id)
  void router.push({ name: 'my-issues' })
}
</script>

<template>
  <template v-if="view">
    <header class="flex h-11 shrink-0 items-center gap-2 border-b border-border px-5">
      <h1 class="truncate font-medium">{{ view.name }}</h1>
      <span class="shrink-0 rounded border border-border-strong px-1.5 text-[11px] text-muted">
        {{ view.shared ? 'Shared' : 'Private' }}
      </span>
      <FavoriteButton kind="view" :target-id="view.id" />
      <div class="ml-auto flex items-center gap-2">
        <BaseButton v-if="mine && changed" size="sm" variant="primary" @click="saveChanges">Save changes</BaseButton>
        <BaseButton
          v-if="mine"
          size="sm"
          variant="ghost"
          @click="sync.updateRecord('View', view.id, { shared: !view.shared })"
        >
          {{ view.shared ? 'Make private' : 'Share' }}
        </BaseButton>
        <FilterMenu :filter="options.filter" :states="states" />
        <DisplayOptions :options="options" />
        <button
          v-if="mine"
          type="button"
          class="flex size-7 items-center justify-center rounded text-faint hover:bg-hover hover:text-danger"
          aria-label="Delete view"
          title="Delete view"
          @click="remove"
        >
          <Trash2 class="size-3.5" aria-hidden="true" />
        </button>
      </div>
    </header>

    <IssueView v-if="issues.length > 0" :view-key="viewKey" :issues="issues" :states="states" />
    <p v-else class="p-8 text-center text-muted">There are no issues to show yet.</p>
  </template>
  <div v-else class="flex flex-1 flex-col items-center justify-center gap-1 p-8 text-center">
    <p class="text-muted">View not found</p>
  </div>
</template>
