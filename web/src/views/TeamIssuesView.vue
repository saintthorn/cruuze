<script setup lang="ts">
import { CircleDot, Plus } from 'lucide-vue-next'
import { computed } from 'vue'
import { useRoute } from 'vue-router'

import DisplayOptions from '@/components/DisplayOptions.vue'
import FilterMenu from '@/components/FilterMenu.vue'
import IssueView from '@/components/IssueView.vue'
import BaseButton from '@/components/ui/BaseButton.vue'
import { useSyncStore } from '@/stores/sync'
import { useUiStore } from '@/stores/ui'
import { useViewOptionsStore } from '@/stores/viewOptions'

const route = useRoute()
const sync = useSyncStore()
const ui = useUiStore()
const viewOptions = useViewOptionsStore()

const teamKey = computed(() => String(route.params.teamKey))
const team = computed(() => sync.teamByKey(teamKey.value))
const issues = computed(() => (team.value ? sync.issuesForTeam(team.value.id) : []))
const states = computed(() => (team.value ? sync.statesForTeam(team.value.id) : []))

const viewKey = computed(() => `team:${teamKey.value}`)
const options = computed(() => viewOptions.optionsFor(viewKey.value))
</script>

<template>
  <template v-if="team">
    <header class="flex h-11 shrink-0 items-center gap-2 border-b border-border px-5">
      <h1 class="font-medium">{{ team.name }}</h1>
      <span class="text-faint" aria-hidden="true">›</span>
      <span class="text-muted">Issues</span>
      <div class="ml-auto flex items-center gap-2">
        <FilterMenu :filter="options.filter" :states="states" />
        <DisplayOptions :options="options" board />
        <BaseButton size="sm" @click="ui.openNewIssue(team.id)">
          <Plus class="size-3.5" aria-hidden="true" /> New issue
          <kbd class="ml-1 rounded border border-border-strong px-1 text-[10px] text-faint">C</kbd>
        </BaseButton>
      </div>
    </header>

    <IssueView v-if="issues.length > 0" :view-key="viewKey" :issues="issues" :states="states" :board-states="states" />

    <div v-else class="flex flex-1 flex-col items-center justify-center gap-2 p-8 text-center">
      <CircleDot class="size-8 text-faint" aria-hidden="true" />
      <p class="font-medium">No issues in {{ team.name }} yet</p>
      <BaseButton variant="primary" class="mt-2" @click="ui.openNewIssue(team.id)">New issue</BaseButton>
    </div>
  </template>
  <div v-else class="flex flex-1 flex-col items-center justify-center gap-1 p-8 text-center">
    <p class="text-muted">Team not found</p>
  </div>
</template>
