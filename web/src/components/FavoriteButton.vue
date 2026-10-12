<script setup lang="ts">
import { Star } from 'lucide-vue-next'
import { computed } from 'vue'

import { useSyncStore } from '@/stores/sync'
import type { Favorite } from '@/sync/types'

const props = defineProps<{ kind: Favorite['kind']; targetId: string }>()

const sync = useSyncStore()
const on = computed(() => sync.favoriteOf(props.kind, props.targetId) !== undefined)
</script>

<template>
  <button
    type="button"
    class="flex size-7 items-center justify-center rounded-md hover:bg-hover"
    :class="on ? 'text-[#f2c94c]' : 'text-faint hover:text-fg'"
    :aria-pressed="on"
    :aria-label="on ? 'Remove from favorites' : 'Add to favorites'"
    :title="on ? 'Remove from favorites' : 'Add to favorites'"
    @click="sync.toggleFavorite(kind, targetId)"
  >
    <Star class="size-3.5" :fill="on ? 'currentColor' : 'none'" aria-hidden="true" />
  </button>
</template>
