<script setup lang="ts">
import { GitMerge, GitPullRequest, GitPullRequestClosed, GitPullRequestDraft } from 'lucide-vue-next'
import { computed } from 'vue'

import { useSyncStore } from '@/stores/sync'
import type { Issue, PullRequest } from '@/sync/types'

const props = defineProps<{ issue: Issue }>()

const sync = useSyncStore()
const pullRequests = computed(() => sync.pullRequestsForIssue(props.issue.id))

const STATUS: Record<PullRequest['status'], { label: string; icon: typeof GitMerge; class: string }> = {
  draft: { label: 'Draft', icon: GitPullRequestDraft, class: 'text-faint' },
  open: { label: 'Open', icon: GitPullRequest, class: 'text-success' },
  merged: { label: 'Merged', icon: GitMerge, class: 'text-accent-hover' },
  closed: { label: 'Closed', icon: GitPullRequestClosed, class: 'text-danger' },
}
</script>

<template>
  <section v-if="pullRequests.length > 0" aria-labelledby="pull-requests-heading" class="flex flex-col gap-2">
    <div class="flex items-center gap-2">
      <h2 id="pull-requests-heading" class="font-medium">Pull requests</h2>
      <span class="text-faint">{{ pullRequests.length }}</span>
    </div>
    <ul class="divide-y divide-border rounded-lg border border-border">
      <li v-for="pr in pullRequests" :key="pr.id" class="flex h-10 items-center gap-2 px-3">
        <component :is="STATUS[pr.status].icon" class="size-3.5 shrink-0" :class="STATUS[pr.status].class" aria-hidden="true" />
        <a :href="pr.url" target="_blank" rel="noopener noreferrer" class="min-w-0 flex-1 truncate hover:underline">
          {{ pr.title }}
        </a>
        <span class="hidden shrink-0 truncate text-xs text-faint sm:block">{{ pr.repo }}#{{ pr.number }}</span>
        <span class="shrink-0 text-xs text-muted">{{ STATUS[pr.status].label }}</span>
      </li>
    </ul>
  </section>
</template>
