<script setup lang="ts">
import { Check, X } from 'lucide-vue-next'
import { computed } from 'vue'
import { RouterLink, useRoute } from 'vue-router'

import BaseButton from '@/components/ui/BaseButton.vue'
import UserAvatar from '@/components/UserAvatar.vue'
import { useIssueSelect } from '@/composables/useIssueSelect'
import { issueIdentifier } from '@/lib/issues'
import { timeAgo } from '@/lib/time'
import { useSyncStore } from '@/stores/sync'
import type { Issue } from '@/sync/types'

const route = useRoute()
const sync = useSyncStore()
const { issueRoute } = useIssueSelect()

const team = computed(() => sync.teamByKey(String(route.params.teamKey)))
const issues = computed(() => (team.value ? sync.triageIssues(team.value.id) : []))

/** Accepting moves an issue into the team's backlog; declining cancels it. */
function resolve(issue: Issue, accept: boolean) {
  const states = sync.statesForTeam(issue.teamId)
  const target = accept
    ? (states.find((s) => s.type === 'backlog') ?? states.find((s) => s.type === 'unstarted'))
    : states.find((s) => s.type === 'canceled')
  if (target) sync.updateIssue(issue.id, { stateId: target.id })
}
</script>

<template>
  <template v-if="team">
    <header class="flex h-11 shrink-0 items-center gap-2 border-b border-border px-5">
      <span class="font-medium">{{ team.name }}</span>
      <span class="text-faint" aria-hidden="true">›</span>
      <h1 class="text-muted">Triage</h1>
      <span class="text-faint">{{ issues.length }}</span>
    </header>

    <ul v-if="issues.length > 0" class="flex-1 overflow-y-auto" aria-label="Issues to triage">
      <li v-for="issue in issues" :key="issue.id" class="flex h-12 items-center gap-3 border-b border-border px-5 hover:bg-hover">
        <span class="w-16 shrink-0 text-muted tabular-nums">{{ issueIdentifier(issue, team) }}</span>
        <RouterLink :to="issueRoute(issue)" class="min-w-0 flex-1 truncate font-medium hover:underline">{{ issue.title }}</RouterLink>
        <span class="hidden shrink-0 items-center gap-1.5 text-xs text-muted sm:flex">
          <UserAvatar
            v-if="sync.user(issue.creatorId)"
            :name="sync.user(issue.creatorId)!.name"
            :email="sync.user(issue.creatorId)!.email"
            :avatar-url="sync.user(issue.creatorId)!.avatarUrl"
            :size="16"
          />
          {{ sync.user(issue.creatorId)?.name || 'Someone' }} · {{ timeAgo(issue.createdAt) }}
        </span>
        <BaseButton size="sm" @click="resolve(issue, true)"><Check class="size-3.5" aria-hidden="true" /> Accept</BaseButton>
        <BaseButton size="sm" variant="ghost" @click="resolve(issue, false)"><X class="size-3.5" aria-hidden="true" /> Decline</BaseButton>
      </li>
    </ul>
    <div v-else class="flex flex-1 flex-col items-center justify-center gap-2 p-8 text-center">
      <p class="text-muted">{{ team.triageEnabled ? 'Nothing to triage' : `Triage is off for ${team.name}` }}</p>
    </div>
  </template>
  <p v-else class="p-8 text-center text-muted">Team not found.</p>
</template>
