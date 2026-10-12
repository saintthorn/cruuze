<script setup lang="ts">
import { Plus } from 'lucide-vue-next'
import { computed } from 'vue'
import { RouterLink, useRoute } from 'vue-router'

import DisplayOptions from '@/components/DisplayOptions.vue'
import FavoriteButton from '@/components/FavoriteButton.vue'
import FilterMenu from '@/components/FilterMenu.vue'
import ForecastLine from '@/components/ForecastLine.vue'
import IssueView from '@/components/IssueView.vue'
import ProgressBar from '@/components/ProgressBar.vue'
import BaseButton from '@/components/ui/BaseButton.vue'
import { isClosed } from '@/lib/filter'
import { formatDateRange, progressOf } from '@/lib/issues'
import { useSyncStore } from '@/stores/sync'
import { useUiStore } from '@/stores/ui'
import { useViewOptionsStore } from '@/stores/viewOptions'

const route = useRoute()
const sync = useSyncStore()
const ui = useUiStore()
const viewOptions = useViewOptionsStore()

const team = computed(() => sync.teamByKey(String(route.params.teamKey)))
const cycle = computed(() =>
  team.value ? sync.cyclesForTeam(team.value.id).find((c) => c.number === Number(route.params.number)) : undefined,
)
const issues = computed(() => sync.issues.filter((i) => i.cycleId === cycle.value?.id))
const states = computed(() => (team.value ? sync.statesForTeam(team.value.id) : []))
const remaining = computed(() => issues.value.filter((i) => !isClosed(i, sync.state)).length)

const viewKey = computed(() => `cycle:${route.params.teamKey}`)
const options = computed(() => viewOptions.optionsFor(viewKey.value))
</script>

<template>
  <template v-if="team && cycle">
    <header class="flex h-11 shrink-0 items-center gap-2 border-b border-border px-5">
      <RouterLink :to="{ name: 'cycles', params: { teamKey: team.key } }" class="text-muted hover:text-fg">Cycles</RouterLink>
      <span class="text-faint" aria-hidden="true">›</span>
      <h1 class="font-medium">{{ sync.cycleName(cycle) }}</h1>
      <span class="hidden text-muted sm:inline">{{ formatDateRange(cycle.startsAt, cycle.endsAt) }}</span>
      <FavoriteButton kind="cycle" :target-id="cycle.id" />
      <ProgressBar :progress="progressOf(issues, sync.state)" class="hidden md:flex" />
      <div class="ml-auto flex items-center gap-2">
        <FilterMenu :filter="options.filter" :states="states" />
        <DisplayOptions :options="options" board />
        <BaseButton size="sm" @click="ui.openNewIssue(team.id, undefined, { cycleId: cycle.id })">
          <Plus class="size-3.5" aria-hidden="true" /> New issue
        </BaseButton>
      </div>
    </header>

    <div class="shrink-0 border-b border-border px-5 py-1.5">
      <ForecastLine :scope="{ cycleId: cycle.id }" :remaining="remaining" />
    </div>
    <IssueView v-if="issues.length > 0" :view-key="viewKey" :issues="issues" :states="states" :board-states="states" />
    <div v-else class="flex flex-1 flex-col items-center justify-center gap-2 p-8 text-center">
      <p class="text-muted">No issues in this cycle</p>
    </div>
  </template>
  <p v-else class="p-8 text-center text-muted">Cycle not found.</p>
</template>
