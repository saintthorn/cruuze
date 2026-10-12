<script setup lang="ts">
import { computed, onBeforeUnmount, ref, watch } from 'vue'

import { api, unwrap } from '@/api/client'
import type { components } from '@/api/schema'
import { describeForecast } from '@/lib/forecast'

type Forecast = components['schemas']['Forecast']

/**
 * When a project, milestone or cycle is likely to be done. The forecast is
 * made on the server from completed work; it is asked for again whenever
 * the amount of work left changes.
 */
const props = defineProps<{
  /** What to forecast. */
  scope: { projectId: string; milestoneId?: string } | { cycleId: string }
  /** Open issues in scope, as this client sees them: when it changes, the forecast is stale. */
  remaining: number
}>()

const forecast = ref<Forecast | null>(null)
let timer: ReturnType<typeof setTimeout> | undefined
let latest = 0

async function load() {
  const request = ++latest
  try {
    const scope = props.scope
    const result =
      'cycleId' in scope
        ? await unwrap(api.GET('/cycles/{cycleId}/forecast', { params: { path: { cycleId: scope.cycleId } } }))
        : await unwrap(
            api.GET('/projects/{projectId}/forecast', {
              params: { path: { projectId: scope.projectId }, query: scope.milestoneId ? { milestoneId: scope.milestoneId } : {} },
            }),
          )
    if (request === latest) forecast.value = result
  } catch {
    // Offline or gone: no forecast is better than a wrong one.
    if (request === latest) forecast.value = null
  }
}

watch(
  [() => JSON.stringify(props.scope), () => props.remaining],
  () => {
    // Edits come in bursts; ask once they settle.
    clearTimeout(timer)
    timer = setTimeout(() => void load(), forecast.value ? 800 : 0)
  },
  { immediate: true },
)
onBeforeUnmount(() => clearTimeout(timer))

const summary = computed(() => (forecast.value ? describeForecast(forecast.value) : null))
</script>

<template>
  <p v-if="summary" class="flex flex-wrap items-baseline gap-x-3 gap-y-0.5 text-xs" :title="summary.detail">
    <span class="text-faint">forecast</span>
    <span v-for="part in summary.parts" :key="part.label" :class="part.tone === 'warn' ? 'text-warning' : part.tone === 'good' ? 'text-accent' : 'text-muted'">
      <span v-if="part.label" class="text-faint">{{ part.label }}</span> {{ part.value }}
    </span>
  </p>
</template>
