<script setup lang="ts">
import { computed } from 'vue'
import { RouterLink } from 'vue-router'

import { useSessionStore } from '@/stores/session'

defineProps<{ title: string; description?: string }>()

const session = useSessionStore()

const TABS = [
  { name: 'settings-workspace', label: 'General' },
  { name: 'settings-members', label: 'Members' },
  { name: 'settings-teams', label: 'Teams' },
  { name: 'settings-labels', label: 'Labels' },
  { name: 'settings-api', label: 'API' },
  { name: 'settings-integrations', label: 'Integrations' },
  { name: 'settings-import', label: 'Import' },
  { name: 'settings-account', label: 'Account' },
  { name: 'settings-themes', label: 'Themes' },
]
const tabs = computed(() => {
  const all = [...TABS]
  // The install's own settings are for whoever runs it.
  if (session.me?.instanceAdmin) all.push({ name: 'settings-instance', label: 'Instance' })
  return all
})
</script>

<template>
  <header class="flex h-11 shrink-0 items-center gap-4 border-b border-border px-5">
    <span class="font-medium">Settings</span>
    <nav aria-label="Settings" class="flex gap-1">
      <RouterLink
        v-for="tab in tabs"
        :key="tab.name"
        :to="{ name: tab.name }"
        class="flex h-7 items-center rounded px-2 text-muted hover:bg-hover hover:text-fg"
        active-class="!bg-active !text-fg"
      >
        {{ tab.label }}
      </RouterLink>
    </nav>
  </header>
  <div class="flex-1 overflow-y-auto">
    <div class="mx-auto flex max-w-2xl flex-col gap-6 px-6 py-10">
      <div>
        <h1 class="text-2xl font-semibold tracking-tight">{{ title }}</h1>
        <p v-if="description" class="mt-1 text-muted">{{ description }}</p>
      </div>
      <slot />
    </div>
  </div>
</template>
