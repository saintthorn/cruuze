<script setup lang="ts">
import { computed } from 'vue'
import { RouterLink, useRoute } from 'vue-router'

import ProgressBar from '@/components/ProgressBar.vue'
import { formatDateRange, progressOf, todayUTC } from '@/lib/issues'
import { useSyncStore } from '@/stores/sync'
import type { Cycle } from '@/sync/types'

const route = useRoute()
const sync = useSyncStore()

const team = computed(() => sync.teamByKey(String(route.params.teamKey)))
const cycles = computed(() => (team.value ? sync.cyclesForTeam(team.value.id) : []))

const sections = computed(() => {
  const today = todayUTC()
  return [
    { title: 'Current', cycles: cycles.value.filter((c) => c.startsAt <= today && today <= c.endsAt) },
    { title: 'Upcoming', cycles: cycles.value.filter((c) => c.startsAt > today) },
    { title: 'Past', cycles: cycles.value.filter((c) => c.endsAt < today).reverse() },
  ].filter((section) => section.cycles.length > 0)
})

const issuesIn = (cycle: Cycle) => sync.issues.filter((i) => i.cycleId === cycle.id)
</script>

<template>
  <template v-if="team">
    <header class="flex h-11 shrink-0 items-center gap-2 border-b border-border px-5">
      <span class="font-medium">{{ team.name }}</span>
      <span class="text-faint" aria-hidden="true">›</span>
      <h1 class="text-muted">Cycles</h1>
    </header>

    <div v-if="team.cycleWeeks > 0" class="flex-1 overflow-y-auto">
      <section v-for="section in sections" :key="section.title" :aria-label="`${section.title} cycles`">
        <h2 class="flex h-9 items-center border-b border-border bg-sidebar px-5 font-medium">{{ section.title }}</h2>
        <ul>
          <li v-for="cycle in section.cycles" :key="cycle.id" class="border-b border-border">
            <RouterLink
              :to="{ name: 'cycle', params: { teamKey: team.key, number: cycle.number } }"
              class="flex h-12 items-center gap-3 px-5 hover:bg-hover"
            >
              <span class="w-32 shrink-0 truncate font-medium">{{ sync.cycleName(cycle) }}</span>
              <span class="min-w-0 flex-1 text-muted">{{ formatDateRange(cycle.startsAt, cycle.endsAt) }}</span>
              <ProgressBar :progress="progressOf(issuesIn(cycle), sync.state)" />
            </RouterLink>
          </li>
        </ul>
      </section>
    </div>
    <div v-else class="flex flex-1 flex-col items-center justify-center gap-2 p-8 text-center">
      <p class="font-medium">Cycles are off for {{ team.name }}</p>
      <RouterLink :to="{ name: 'settings-team', params: { teamKey: team.key } }" class="text-accent-hover hover:underline">
        Team settings
      </RouterLink>
    </div>
  </template>
  <p v-else class="p-8 text-center text-muted">Team not found.</p>
</template>
