<script setup lang="ts">
import { X } from 'lucide-vue-next'
import { computed } from 'vue'
import { RouterLink } from 'vue-router'

import PickerMenu from '@/components/PickerMenu.vue'
import { isOpenRequest, requestStatus } from '@/lib/requests'
import { useSyncStore } from '@/stores/sync'
import { useWorkspaceStore } from '@/stores/workspace'
import type { Issue } from '@/sync/types'

/** The customers waiting on an issue. Completing the issue tells them. */
const props = defineProps<{ issue: Issue }>()

const sync = useSyncStore()
const workspace = useWorkspaceStore()

const requests = computed(() => sync.requestsForIssue(props.issue.id))
const votes = computed(() => requests.value.reduce((sum, r) => sum + r.voteCount, 0))
// Open requests that aren't tied to an issue yet.
const unlinked = computed(() =>
  sync.customerRequests.filter((r) => !r.issueId && isOpenRequest(r)).map((r) => ({ key: r.id, label: `${r.title} · ${r.requesterEmail}` })),
)
</script>

<template>
  <section v-if="requests.length > 0 || (workspace.canManageLabels && unlinked.length > 0)" aria-labelledby="customer-requests-heading" class="flex flex-col gap-2">
    <div class="flex items-center gap-2">
      <h2 id="customer-requests-heading" class="font-medium">Customer requests</h2>
      <span v-if="requests.length > 0" class="text-faint">
        {{ requests.length }}<template v-if="votes > 0"> · {{ votes }} {{ votes === 1 ? 'vote' : 'votes' }}</template>
      </span>
      <PickerMenu
        v-if="workspace.canManageLabels && unlinked.length > 0 && issue.number > 0"
        :options="unlinked"
        :selected="[]"
        label="Link a customer request to this issue"
        trigger-class="flex h-7 items-center gap-1.5 rounded-md px-2 text-xs text-muted hover:bg-hover hover:text-fg"
        searchable
        @select="sync.updateRecord('CustomerRequest', $event, { issueId: issue.id })"
      >
        Link request
      </PickerMenu>
    </div>
    <ul v-if="requests.length > 0" class="divide-y divide-border rounded-lg border border-border">
      <li v-for="request in requests" :key="request.id" class="flex h-10 items-center gap-2 px-3">
        <RouterLink :to="{ name: 'requests' }" class="min-w-0 flex-1 truncate hover:underline">{{ request.title }}</RouterLink>
        <span class="hidden shrink-0 truncate text-xs text-faint sm:block">{{ request.requesterEmail }}</span>
        <span class="shrink-0 text-xs" :class="requestStatus(request.status).class">{{ requestStatus(request.status).label }}</span>
        <button
          v-if="workspace.canManageLabels"
          type="button"
          class="flex size-6 shrink-0 items-center justify-center rounded text-faint hover:bg-hover hover:text-danger"
          :aria-label="`Unlink the request ${request.title}`"
          @click="sync.updateRecord('CustomerRequest', request.id, { issueId: null })"
        >
          <X class="size-3.5" aria-hidden="true" />
        </button>
      </li>
    </ul>
  </section>
</template>
