<script setup lang="ts">
import { CircleUser } from 'lucide-vue-next'
import { computed } from 'vue'

import DisplayOptions from '@/components/DisplayOptions.vue'
import FilterMenu from '@/components/FilterMenu.vue'
import IssueView from '@/components/IssueView.vue'
import { useSyncStore } from '@/stores/sync'
import { useViewOptionsStore } from '@/stores/viewOptions'

const VIEW_KEY = 'my-issues'

const sync = useSyncStore()
const viewOptions = useViewOptionsStore()

const options = computed(() => viewOptions.optionsFor(VIEW_KEY))
// The states of every team the user's issues are in.
const states = computed(() => [...new Set(sync.myIssues.map((i) => i.teamId))].flatMap((id) => sync.statesForTeam(id)))
</script>

<template>
  <header class="flex h-11 shrink-0 items-center gap-2 border-b border-border px-5">
    <h1 class="font-medium">My issues</h1>
    <span class="text-faint">{{ sync.myIssues.length }}</span>
    <div class="ml-auto flex items-center gap-2">
      <FilterMenu :filter="options.filter" :states="states" />
      <DisplayOptions :options="options" />
    </div>
  </header>

  <IssueView v-if="sync.myIssues.length > 0" :view-key="VIEW_KEY" :issues="sync.myIssues" :states="states" />

  <div v-else class="flex flex-1 flex-col items-center justify-center gap-2 p-8 text-center">
    <CircleUser class="size-8 text-faint" aria-hidden="true" />
    <p class="text-muted">Nothing assigned to you</p>
  </div>
</template>
