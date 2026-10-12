<script setup lang="ts">
import { Plus } from 'lucide-vue-next'
import { computed } from 'vue'
import { RouterLink } from 'vue-router'

import ProgressBar from '@/components/ProgressBar.vue'
import StateIcon from '@/components/StateIcon.vue'
import UserAvatar from '@/components/UserAvatar.vue'
import { useIssueSelect } from '@/composables/useIssueSelect'
import { issueIdentifier, progressOf } from '@/lib/issues'
import { useSyncStore } from '@/stores/sync'
import { useUiStore } from '@/stores/ui'
import type { Issue } from '@/sync/types'

const props = defineProps<{ issue: Issue }>()

const sync = useSyncStore()
const ui = useUiStore()
const { issueRoute } = useIssueSelect()

const children = computed(() => sync.subIssues(props.issue.id))
const progress = computed(() => progressOf(children.value, sync.state))
</script>

<template>
  <section aria-labelledby="sub-issues-heading" class="flex flex-col gap-2">
    <div class="flex items-center gap-3">
      <h2 id="sub-issues-heading" class="font-medium">Sub-issues</h2>
      <ProgressBar v-if="children.length > 0" :progress="progress" />
      <button
        type="button"
        class="ml-auto flex h-7 items-center gap-1.5 rounded-md px-2 text-xs text-muted hover:bg-hover hover:text-fg"
        @click="ui.openNewIssue(issue.teamId, undefined, { parentId: issue.id, projectId: issue.projectId })"
      >
        <Plus class="size-3.5" aria-hidden="true" /> Add sub-issue
      </button>
    </div>
    <ul v-if="children.length > 0" class="divide-y divide-border rounded-lg border border-border">
      <li v-for="child in children" :key="child.id">
        <RouterLink :to="issueRoute(child)" class="flex h-9 items-center gap-2.5 px-3 hover:bg-hover">
          <StateIcon v-if="sync.state(child.stateId)" :type="sync.state(child.stateId)!.type" :color="sync.state(child.stateId)!.color" />
          <span class="shrink-0 text-muted tabular-nums">{{ issueIdentifier(child, sync.team(child.teamId)) }}</span>
          <span class="min-w-0 flex-1 truncate">{{ child.title }}</span>
          <UserAvatar
            v-if="sync.user(child.assigneeId)"
            :name="sync.user(child.assigneeId)!.name"
            :email="sync.user(child.assigneeId)!.email"
            :avatar-url="sync.user(child.assigneeId)!.avatarUrl"
            :size="18"
          />
        </RouterLink>
      </li>
    </ul>
  </section>
</template>
