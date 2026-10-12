<script setup lang="ts">
import { Lock } from 'lucide-vue-next'
import { ref } from 'vue'
import { RouterLink } from 'vue-router'

import { errorMessage } from '@/api/client'
import CreateTeamDialog from '@/components/CreateTeamDialog.vue'
import SettingsPage from '@/components/SettingsPage.vue'
import BaseButton from '@/components/ui/BaseButton.vue'
import FormError from '@/components/ui/FormError.vue'
import { type TeamView, useWorkspaceStore } from '@/stores/workspace'

const workspace = useWorkspaceStore()

const createOpen = ref(false)
const error = ref('')
/** ID of the team whose membership is being changed. */
const busy = ref<string | null>(null)

async function toggle(team: TeamView) {
  error.value = ''
  busy.value = team.id
  try {
    await workspace.setTeamMembership(team, !team.isMember)
  } catch (err) {
    error.value = errorMessage(err)
  } finally {
    busy.value = null
  }
}
</script>

<template>
  <SettingsPage title="Teams">
    <div v-if="workspace.canCreateTeams">
      <BaseButton variant="primary" @click="createOpen = true">Create team</BaseButton>
    </div>
    <FormError :message="error" />

    <ul v-if="workspace.teams.length > 0" class="divide-y divide-border rounded-lg border border-border" aria-label="Teams">
      <li v-for="team in workspace.teams" :key="team.id" class="flex h-14 items-center gap-3 px-4">
        <span
          class="flex size-7 shrink-0 items-center justify-center rounded bg-active text-xs font-semibold text-muted"
          aria-hidden="true"
        >
          {{ team.key[0] }}
        </span>
        <span class="min-w-0 flex-1">
          <span class="flex items-center gap-1.5 font-medium">
            <span class="truncate">{{ team.name }}</span>
            <Lock v-if="team.private" class="size-3 shrink-0 text-faint" aria-label="Private" />
          </span>
          <span class="block text-xs text-faint">{{ team.key }}</span>
        </span>
        <RouterLink
          :to="{ name: 'settings-team', params: { teamKey: team.key } }"
          class="rounded px-2 py-1 text-xs text-muted hover:bg-hover hover:text-fg"
        >
          Settings
        </RouterLink>
        <BaseButton size="sm" :loading="busy === team.id" @click="toggle(team)">
          {{ team.isMember ? 'Leave' : 'Join' }}
        </BaseButton>
      </li>
    </ul>
    <p v-else class="text-muted">No teams</p>

    <CreateTeamDialog v-model:open="createOpen" />
  </SettingsPage>
</template>
