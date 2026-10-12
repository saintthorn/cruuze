<script setup lang="ts">
import { ExternalLink, Plus, Trash2, X } from 'lucide-vue-next'
import { computed, ref, watch } from 'vue'
import { RouterLink } from 'vue-router'

import IssuePicker from '@/components/issue/IssuePicker.vue'
import PickerMenu from '@/components/PickerMenu.vue'
import StateIcon from '@/components/StateIcon.vue'
import BaseButton from '@/components/ui/BaseButton.vue'
import { useDraft } from '@/composables/useDraft'
import { useIssueSelect } from '@/composables/useIssueSelect'
import { issueIdentifier } from '@/lib/issues'
import { TRIGGER_CLASS } from '@/lib/pickerVariants'
import { isOpenRequest, REQUEST_STATUSES, type RequestStatus, requestStatus } from '@/lib/requests'
import { timeAgo } from '@/lib/time'
import { useSyncStore } from '@/stores/sync'
import { useWorkspaceStore } from '@/stores/workspace'

/** What customers have asked for: read it, answer it, tie it to the issue that delivers it. */
const sync = useSyncStore()
const workspace = useWorkspaceStore()
const { issueRoute } = useIssueSelect()
const editable = computed(() => workspace.canManageLabels)

const showing = ref<'open' | 'all'>('open')
const requests = computed(() => (showing.value === 'open' ? sync.customerRequests.filter(isOpenRequest) : sync.customerRequests))

const selectedId = ref<string | null>(null)
const selected = computed(() => sync.customerRequest(selectedId.value ?? undefined))
// Keep something selected while there is anything to select.
watch(
  [requests, selected],
  () => {
    if (!selected.value) selectedId.value = requests.value[0]?.id ?? null
  },
  { immediate: true },
)

const issue = computed(() => sync.issue(selected.value?.issueId))
const statusOptions = REQUEST_STATUSES.map((s) => ({ key: s.value, label: s.label }))

function update(patch: Record<string, unknown>) {
  if (selected.value) sync.updateRecord('CustomerRequest', selected.value.id, patch)
}

const response = useDraft(
  () => selected.value?.response ?? '',
  (value) => update({ response: value.trim() }),
)

/** Makes an issue out of the request, in the given team, and ties them together. */
function createIssue(teamId: string) {
  const request = selected.value
  if (!request) return
  const created = sync.createIssue({
    teamId,
    title: request.title,
    description: `${request.body}\n\n_Asked for by ${request.requesterEmail}._`.trim(),
  })
  if (created) update({ issueId: created.id, status: request.status === 'new' ? 'planned' : request.status })
}
const teamOptions = computed(() => workspace.myTeams.map((t) => ({ key: t.id, label: t.name })))

function remove() {
  const request = selected.value
  if (!request || !window.confirm(`Delete the request "${request.title}"? ${request.requesterEmail} won't be told.`)) return
  sync.deleteRecord('CustomerRequest', request.id)
  selectedId.value = null
}

// --- noting one down ---------------------------------------------------------
const adding = ref(false)
const newTitle = ref('')
const newEmail = ref('')
const newBody = ref('')

function add() {
  if (!newTitle.value.trim() || !/^\S+@\S+\.\S+$/.test(newEmail.value.trim())) return
  selectedId.value = sync.createCustomerRequest(newTitle.value, newEmail.value, newBody.value).id
  adding.value = false
  newTitle.value = newEmail.value = newBody.value = ''
}

const portalUrl = computed(() => `${window.location.origin}/portal/${workspace.current?.urlKey}`)
</script>

<template>
  <header class="flex h-11 shrink-0 items-center gap-2 border-b border-border px-5">
    <h1 class="font-medium">Requests</h1>
    <span class="text-faint">{{ requests.length }}</span>
    <div class="ml-2 flex items-center gap-0.5 text-xs" role="group" aria-label="Which requests to show">
      <button
        v-for="option in (['open', 'all'] as const)"
        :key="option"
        type="button"
        class="h-7 rounded-md px-2"
        :class="showing === option ? 'bg-active text-fg' : 'text-muted hover:text-fg'"
        :aria-pressed="showing === option"
        @click="showing = option"
      >
        {{ option === 'open' ? 'Open' : 'All' }}
      </button>
    </div>
    <div class="ml-auto flex items-center gap-2">
      <a
        v-if="workspace.current?.portalEnabled"
        :href="portalUrl"
        target="_blank"
        rel="noopener"
        class="flex h-7 items-center gap-1.5 rounded-md px-2 text-xs text-muted hover:bg-hover hover:text-fg"
      >
        <ExternalLink class="size-3.5" aria-hidden="true" /> Portal
      </a>
      <RouterLink v-else-if="workspace.isAdmin" :to="{ name: 'settings-workspace' }" class="text-xs text-muted hover:text-fg">
        Portal is off
      </RouterLink>
      <BaseButton v-if="editable" size="sm" @click="adding = !adding"><Plus class="size-3.5" aria-hidden="true" /> Note a request</BaseButton>
    </div>
  </header>

  <form v-if="adding" class="flex shrink-0 flex-wrap items-start gap-2 border-b border-border bg-elevated px-5 py-3" aria-label="Note a request" @submit.prevent="add">
    <input v-model="newTitle" aria-label="What they asked for" placeholder="What they asked for" maxlength="200" required class="h-8 min-w-56 flex-1 rounded-md border border-border-strong bg-bg px-3 outline-none placeholder:text-faint focus:border-accent" />
    <input v-model="newEmail" type="email" aria-label="Their email" placeholder="customer@company.com" required class="h-8 w-64 rounded-md border border-border-strong bg-bg px-3 outline-none placeholder:text-faint focus:border-accent" />
    <input v-model="newBody" aria-label="Details" placeholder="Details (optional)" maxlength="10000" class="h-8 min-w-56 flex-1 rounded-md border border-border-strong bg-bg px-3 outline-none placeholder:text-faint focus:border-accent" />
    <BaseButton type="submit" variant="primary">Add</BaseButton>
  </form>

  <div v-if="sync.customerRequests.length > 0" class="flex min-h-0 flex-1 flex-col md:flex-row">
    <ul class="max-h-64 shrink-0 overflow-y-auto border-b border-border md:max-h-none md:w-96 md:border-r md:border-b-0" aria-label="Requests">
      <li v-for="request in requests" :key="request.id" class="border-b border-border">
        <button
          type="button"
          class="flex w-full flex-col gap-0.5 px-5 py-2.5 text-left hover:bg-hover"
          :class="{ 'bg-active': request.id === selectedId }"
          :aria-current="request.id === selectedId"
          @click="selectedId = request.id"
        >
          <span class="flex items-baseline gap-2">
            <span class="min-w-0 flex-1 truncate font-medium">{{ request.title }}</span>
            <span v-if="request.voteCount > 0" class="shrink-0 text-xs text-muted tabular-nums" :title="`${request.voteCount} votes`">▲ {{ request.voteCount }}</span>
          </span>
          <span class="flex items-baseline gap-2 text-xs">
            <span :class="requestStatus(request.status).class">{{ requestStatus(request.status).label }}</span>
            <span class="min-w-0 flex-1 truncate text-faint">{{ request.requesterEmail }}</span>
            <span class="shrink-0 text-faint">{{ timeAgo(request.createdAt) }}</span>
          </span>
        </button>
      </li>
      <li v-if="requests.length === 0" class="px-5 py-6 text-muted">Nothing open</li>
    </ul>

    <article v-if="selected" class="min-w-0 flex-1 overflow-y-auto px-8 py-8" aria-label="Request">
      <div class="mx-auto flex max-w-2xl flex-col gap-4">
        <div class="flex items-start gap-3">
          <h2 class="min-w-0 flex-1 text-xl font-semibold tracking-tight">{{ selected.title }}</h2>
          <button
            v-if="editable"
            type="button"
            class="flex size-7 shrink-0 items-center justify-center rounded text-faint hover:bg-hover hover:text-danger"
            aria-label="Delete request"
            title="Delete request"
            @click="remove"
          >
            <Trash2 class="size-3.5" aria-hidden="true" />
          </button>
        </div>
        <p class="-mt-2 text-xs text-muted">
          <a :href="`mailto:${selected.requesterEmail}`" class="hover:underline">{{ selected.requesterEmail }}</a>
          · {{ timeAgo(selected.createdAt) }}
          <template v-if="selected.voteCount > 0"> · {{ selected.voteCount }} {{ selected.voteCount === 1 ? 'vote' : 'votes' }}</template>
        </p>
        <p v-if="selected.body" class="text-[14px] leading-relaxed whitespace-pre-wrap">{{ selected.body }}</p>

        <div class="flex flex-wrap items-center gap-1.5 border-t border-border pt-4">
          <PickerMenu
            v-if="editable"
            :options="statusOptions"
            :selected="[selected.status]"
            :label="`Status: ${requestStatus(selected.status).label}. Change status`"
            :trigger-class="TRIGGER_CLASS.chip"
            @select="update({ status: $event as RequestStatus })"
          >
            <span :class="requestStatus(selected.status).class">{{ requestStatus(selected.status).label }}</span>
          </PickerMenu>
          <label v-if="editable" class="flex h-7 items-center gap-1.5 rounded-md border border-border-strong px-2 text-xs select-none" :title="workspace.current?.portalPublicBoard ? undefined : 'The public board is off in settings'">
            <input type="checkbox" :checked="selected.public" class="size-3.5 accent-(--accent)" @change="update({ public: ($event.target as HTMLInputElement).checked })" />
            Public
          </label>
        </div>

        <section aria-labelledby="request-issue" class="flex flex-col gap-2">
          <h3 id="request-issue" class="font-medium">Issue</h3>
          <div v-if="issue" class="flex h-10 items-center gap-2 rounded-lg border border-border px-3">
            <StateIcon v-if="sync.state(issue.stateId)" :type="sync.state(issue.stateId)!.type" :color="sync.state(issue.stateId)!.color" />
            <span class="shrink-0 text-xs text-muted tabular-nums">{{ issueIdentifier(issue, sync.team(issue.teamId)) }}</span>
            <RouterLink :to="issueRoute(issue)" class="min-w-0 flex-1 truncate hover:underline">{{ issue.title }}</RouterLink>
            <button
              v-if="editable"
              type="button"
              class="flex size-6 shrink-0 items-center justify-center rounded text-faint hover:bg-hover hover:text-danger"
              aria-label="Unlink issue"
              @click="update({ issueId: null })"
            >
              <X class="size-3.5" aria-hidden="true" />
            </button>
          </div>
          <p v-else-if="selected.issueId" class="text-xs text-muted">Linked issue not visible</p>
          <div v-else-if="editable" class="flex flex-wrap items-center gap-2">
            <IssuePicker label="Link an existing issue" :trigger-class="TRIGGER_CLASS.chip" @select="update({ issueId: $event })">
              Link an issue
            </IssuePicker>
            <PickerMenu
              v-if="teamOptions.length > 0"
              :options="teamOptions"
              :selected="[]"
              label="Create an issue from this request"
              :trigger-class="TRIGGER_CLASS.chip"
              @select="createIssue"
            >
              <Plus class="size-3.5" aria-hidden="true" /> Create issue
            </PickerMenu>
          </div>
          <p class="text-xs text-faint">Completing the issue ships the request and emails the requester.</p>
        </section>

        <section aria-labelledby="request-response" class="flex flex-col gap-2">
          <h3 id="request-response" class="font-medium">Response</h3>
          <textarea
            v-model="response.draft.value"
            aria-labelledby="request-response"
            rows="4"
            maxlength="10000"
            :readonly="!editable"
            placeholder="Shown to the requester"
            class="resize-y rounded-md border border-border-strong bg-elevated px-3 py-2 text-[13px] outline-none placeholder:text-faint focus:border-accent"
            @focus="response.onFocus"
            @blur="response.onBlur"
          />
        </section>
      </div>
    </article>
  </div>

  <div v-else class="flex flex-1 flex-col items-center justify-center gap-1 p-8 text-center">
    <p class="text-muted">No requests</p>
  </div>
</template>
