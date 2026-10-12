<script setup lang="ts">
import SettingsPage from '@/components/SettingsPage.vue'
import { currentTheme, setTheme, THEMES } from '@/lib/themes'

// Each choice is a small picture of the app drawn in that theme: carrying
// the theme's data-theme, it takes that theme's colors, type and corners.

const SAMPLE = [
  { id: 'ENG-142', title: 'Outbox survives a reload', state: 'In Progress', active: true },
  { id: 'ENG-143', title: 'Forecast from history', state: 'Todo', active: false },
  { id: 'ENG-144', title: 'Notify requesters on ship', state: 'Todo', active: false },
]
</script>

<template>
  <SettingsPage title="Themes">
    <div role="radiogroup" aria-label="Theme" class="grid gap-4 sm:grid-cols-2">
      <button
        v-for="theme in THEMES"
        :key="theme.id"
        type="button"
        role="radio"
        :aria-checked="currentTheme === theme.id"
        class="group flex flex-col overflow-hidden rounded-lg border text-left"
        :class="currentTheme === theme.id ? 'border-accent ring-1 ring-accent' : 'border-border-strong hover:border-fg'"
        @click="setTheme(theme.id)"
      >
        <span :data-theme="theme.id" class="flex h-36 w-full bg-bg text-[11px] leading-normal" aria-hidden="true">
          <span class="flex w-24 shrink-0 flex-col gap-1.5 border-r border-border bg-sidebar p-2.5">
            <span class="font-semibold text-fg">acme</span>
            <span class="rounded-sm bg-active px-1.5 py-0.5 text-fg">Active</span>
            <span class="px-1.5 text-muted">Cycles</span>
            <span class="px-1.5 text-muted">Roadmap</span>
          </span>
          <span class="flex min-w-0 flex-1 flex-col">
            <span class="flex items-center justify-between border-b border-border px-3 py-2">
              <span class="font-(family-name:--font-display) font-semibold text-fg">Engineering</span>
              <span class="rounded-md bg-accent px-2 py-0.5 font-medium text-accent-fg">New</span>
            </span>
            <span
              v-for="issue in SAMPLE"
              :key="issue.id"
              class="flex items-center gap-2 border-b border-border px-3 py-1.5"
              :class="{ 'bg-elevated': issue.active }"
            >
              <span class="shrink-0 text-faint">{{ issue.id }}</span>
              <span class="min-w-0 flex-1 truncate text-fg">{{ issue.title }}</span>
              <span class="shrink-0" :class="issue.active ? 'text-warning' : 'text-muted'">{{ issue.state }}</span>
            </span>
          </span>
        </span>
        <span class="flex items-center justify-between border-t border-border px-3 py-2">
          <span class="font-medium">{{ theme.name }}</span>
          <span v-if="currentTheme === theme.id" class="text-accent" aria-hidden="true">✓</span>
        </span>
      </button>
    </div>
  </SettingsPage>
</template>
