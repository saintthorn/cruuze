<script setup lang="ts">
import { computed } from 'vue'

import { initials } from '@/lib/keys'

const props = defineProps<{ name: string; email?: string; avatarUrl?: string; size?: number }>()

// Everyone is a shade of the theme's accent; which one is stable per person,
// derived from their email or name. Each shade is the accent with its hue
// turned by this many degrees.
const TURNS = [0, -18, 18, -36, 36, -9, 27]

const color = computed(() => {
  const seed = props.email || props.name
  let hash = 0
  for (const ch of seed) hash = (hash * 31 + ch.charCodeAt(0)) >>> 0
  return `oklch(from var(--accent) l c calc(h + ${TURNS[hash % TURNS.length]}))`
})
const px = computed(() => `${props.size ?? 20}px`)
</script>

<template>
  <img
    v-if="avatarUrl"
    :src="avatarUrl"
    alt=""
    referrerpolicy="no-referrer"
    class="shrink-0 rounded-sm border border-border-strong object-cover"
    :style="{ width: px, height: px, filter: 'grayscale(1)' }"
  />
  <span
    v-else
    aria-hidden="true"
    class="inline-flex shrink-0 items-center justify-center rounded-sm border bg-bg font-semibold select-none"
    :style="{ width: px, height: px, color, borderColor: color, fontSize: `${(size ?? 20) * 0.42}px` }"
  >
    {{ initials(name, email) }}
  </span>
</template>
