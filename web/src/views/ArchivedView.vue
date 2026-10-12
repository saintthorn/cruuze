<script setup lang="ts">
import { ArchiveRestore } from 'lucide-vue-next'
import { computed, ref, watch } from 'vue'
import { RouterLink, useRoute } from 'vue-router'

import { api, errorMessage, unwrap } from '@/api/client'
import BaseButton from '@/components/ui/BaseButton.vue'
import { issueIdentifier } from '@/lib/issues'
import { useSyncStore } from '@/stores/sync'
import type { Issue } from '@/sync/types'

/** A team's archive. Archived issues aren't in the local copy, so this page asks the server. */
const route = useRoute()
const sync = useSyncStore()

const team = computed(() => sync.teamByKey(String(route.params.teamKey)))
const issues = ref<Issue[]>([])
const more = ref(false)
const loading = ref(false)
const error = ref('')
/** ID of the issue being unarchived. */
const restoring = ref<string | null>(null)

async function load(reset = false) {
  if (!team.value) return
  loading.value = true
  error.value = ''
  try {
    const before = reset ? undefined : issues.value[issues.value.length - 1]?.archivedAt
    const page = await unwrap(
      api.GET('/teams/{teamId}/archived', { params: { path: { teamId: team.value.id }, query: before ? { before } : {} } }),
    )
    issues.value = reset ? page.issues : [...issues.value, ...page.issues]
    more.value = page.more
  } catch (err) {
    error.value = errorMessage(err)
  } finally {
    loading.value = false
  }
}

watch(() => team.value?.id, () => void load(true), { immediate: true })

async function unarchive(issue: Issue) {
  restoring.value = issue.id
  error.value = ''
  try {
    await unwrap(api.POST('/issues/{issueId}/unarchive', { params: { path: { issueId: issue.id } } }))
    issues.value = issues.value.filter((i) => i.id !== issue.id)
  } catch (err) {
    error.value = errorMessage(err)
  } finally {
    restoring.value = null
  }
}

const date = (iso: string | undefined) =>
  iso ? new Date(iso).toLocaleDateString('en-US', { month: 'short', day: 'numeric', year: 'numeric' }) : ''
</script>

<template>
  <template v-if="team">
    <header class="flex h-11 shrink-0 items-center gap-2 border-b border-border px-5">
      <span class="font-medium">{{ team.name }}</span>
      <span class="text-faint" aria-hidden="true">›</span>
      <h1 class="text-muted">Archived</h1>
    </header>
    <p v-if="error" role="alert" class="border-b border-border px-5 py-2 text-danger">{{ error }}</p>

    <div v-if="issues.length > 0" class="flex-1 overflow-y-auto">
      <ul aria-label="Archived issues">
        <li v-for="issue in issues" :key="issue.id" class="flex h-10 items-center gap-3 border-b border-border px-5 hover:bg-hover">
          <span class="w-20 shrink-0 text-muted tabular-nums">{{ issueIdentifier(issue, team) }}</span>
          <RouterLink
            :to="{ name: 'issue', params: { issueRef: issueIdentifier(issue, team) } }"
            class="min-w-0 flex-1 truncate text-muted hover:text-fg hover:underline"
          >
            {{ issue.title }}
          </RouterLink>
          <span class="hidden shrink-0 text-xs text-faint sm:inline">archived {{ date(issue.archivedAt) }}</span>
          <BaseButton size="sm" variant="ghost" :loading="restoring === issue.id" @click="unarchive(issue)">
            <ArchiveRestore class="size-3.5" aria-hidden="true" /> Unarchive
          </BaseButton>
        </li>
      </ul>
      <div v-if="more" class="p-4 text-center">
        <BaseButton :loading="loading" @click="load()">Show older</BaseButton>
      </div>
    </div>
    <div v-else-if="loading" class="flex flex-1 items-center justify-center p-8 text-muted" role="status">Loading…</div>
    <div v-else class="flex flex-1 flex-col items-center justify-center gap-2 p-8 text-center">
      <p class="text-muted">Nothing archived</p>
    </div>
  </template>
  <p v-else class="p-8 text-center text-muted">Team not found.</p>
</template>
