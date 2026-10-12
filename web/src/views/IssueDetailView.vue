<script setup lang="ts">
import { Archive, ArchiveRestore, Bell, BellOff, CornerLeftUp, GitBranch } from 'lucide-vue-next'
import { computed, onBeforeUnmount, ref, useTemplateRef, watch } from 'vue'
import { RouterLink, useRoute, useRouter } from 'vue-router'

import { errorMessage } from '@/api/client'
import AutoTextarea from '@/components/AutoTextarea.vue'
import RichText from '@/components/editor/LazyRichText.vue'
import ActivityFeed from '@/components/issue/ActivityFeed.vue'
import FavoriteButton from '@/components/FavoriteButton.vue'
import AssigneePicker from '@/components/issue/AssigneePicker.vue'
import AttachmentList from '@/components/issue/AttachmentList.vue'
import CustomerRequests from '@/components/issue/CustomerRequests.vue'
import CyclePicker from '@/components/issue/CyclePicker.vue'
import DueDateField from '@/components/issue/DueDateField.vue'
import IssuePicker from '@/components/issue/IssuePicker.vue'
import LabelPicker from '@/components/issue/LabelPicker.vue'
import MilestonePicker from '@/components/issue/MilestonePicker.vue'
import PriorityPicker from '@/components/issue/PriorityPicker.vue'
import PullRequests from '@/components/issue/PullRequests.vue'
import ProjectPicker from '@/components/issue/ProjectPicker.vue'
import Relations from '@/components/issue/Relations.vue'
import StatusPicker from '@/components/issue/StatusPicker.vue'
import SubIssues from '@/components/issue/SubIssues.vue'
import TimeTracking from '@/components/issue/TimeTracking.vue'
import LabelChip from '@/components/LabelChip.vue'
import BaseButton from '@/components/ui/BaseButton.vue'
import { useDraft } from '@/composables/useDraft'
import { useIssueActions } from '@/composables/useIssueActions'
import { useIssueSelect } from '@/composables/useIssueSelect'
import { issueIdentifier } from '@/lib/issues'
import { TRIGGER_CLASS } from '@/lib/pickerVariants'
import { worksInCode } from '@/lib/teamKinds'
import { useIssueViewStore } from '@/stores/issueView'
import { useSyncStore } from '@/stores/sync'
import { useToastStore } from '@/stores/toasts'
import type { IssuePatch } from '@/sync/mutations'

const route = useRoute()
const router = useRouter()
const sync = useSyncStore()
const view = useIssueViewStore()
const toasts = useToastStore()
const { issueRoute } = useIssueSelect()
const actions = useIssueActions()

const issue = computed(() => sync.issueByRef(String(route.params.issueRef)))
const team = computed(() => (issue.value ? sync.team(issue.value.teamId) : undefined))
// An issue in triage shows that as its status; otherwise triage isn't offered.
const states = computed(() => (issue.value ? sync.statesForTeam(issue.value.teamId, sync.isTriage(issue.value)) : []))
const parent = computed(() => sync.issue(issue.value?.parentId))
const labels = computed(() => (issue.value ? sync.labelsById(issue.value.labelIds) : []))
const creator = computed(() => sync.user(issue.value?.creatorId))
const identifier = computed(() => (issue.value ? issueIdentifier(issue.value, team.value) : ''))

// Shortcuts and the command menu act on the open issue.
watch(() => issue.value?.id ?? null, (id) => (view.detailId = id), { immediate: true })
onBeforeUnmount(() => (view.detailId = null))

// Archived issues aren't in the local copy. If the address names an issue we
// don't have, ask the server before saying it doesn't exist.
const looking = ref(false)
watch(
  [() => String(route.params.issueRef), () => issue.value?.id],
  async ([ref, found]) => {
    if (found) return
    looking.value = true
    try {
      await sync.fetchIssue(ref)
    } catch {
      // Offline: all we can say is that it isn't here.
    } finally {
      if (String(route.params.issueRef) === ref) looking.value = false
    }
  },
  { immediate: true },
)

const restoring = ref(false)
async function unarchive() {
  if (!issue.value) return
  restoring.value = true
  try {
    await sync.unarchiveIssue(issue.value.id)
  } catch (err) {
    toasts.show(errorMessage(err))
  } finally {
    restoring.value = false
  }
}

// The discussion isn't part of the initial load; fetch it for the issue on
// screen. An issue the server hasn't seen yet has none to fetch.
const saved = computed(() => (issue.value?.number ?? 0) > 0)
const detailsLoaded = computed(() => !saved.value || (issue.value !== undefined && sync.hasIssueDetails(issue.value.id)))
const detailsFailed = ref(false)
watch(
  // Also when the connection returns, to retry what failed while offline.
  [() => issue.value?.id, saved, () => sync.status],
  ([id, isSaved]) => {
    if (!id || !isSaved || sync.hasIssueDetails(id)) return
    detailsFailed.value = false
    sync.loadIssueDetails(id).catch(() => {
      if (issue.value?.id === id) detailsFailed.value = true
    })
  },
  { immediate: true },
)

function update(patch: IssuePatch) {
  if (issue.value) sync.updateIssue(issue.value.id, patch)
}

const titleField = useTemplateRef('titleField')
const title = useDraft(
  () => issue.value?.title ?? '',
  (value) => {
    // An empty title isn't allowed; leaving it blank keeps the old one.
    if (value.trim()) update({ title: value.trim() })
  },
)
const description = useDraft(
  () => issue.value?.description ?? '',
  (value) => update({ description: value }),
)

const subscribed = computed(() => issue.value?.subscriberIds.includes(sync.currentUserId()) ?? false)

// Images the browser can show go inline; everything else becomes a link.
const INLINE_IMAGES = ['image/png', 'image/jpeg', 'image/gif', 'image/webp']

async function upload(file: File) {
  const attachment = await sync.uploadAttachment(issue.value!.id, file)
  return {
    url: `/api/attachments/${attachment.id}`,
    name: attachment.filename,
    image: INLINE_IMAGES.includes(attachment.contentType),
  }
}

function archive() {
  if (!issue.value || !team.value) return
  sync.archiveIssue(issue.value.id)
  void router.push({ name: 'team-issues', params: { teamKey: team.value.key } })
}

const created = computed(() =>
  issue.value
    ? new Date(issue.value.createdAt).toLocaleDateString('en-US', { month: 'short', day: 'numeric', year: 'numeric' })
    : '',
)
</script>

<template>
  <template v-if="issue && team">
    <header class="flex h-11 shrink-0 items-center gap-2 border-b border-border px-5">
      <RouterLink :to="{ name: 'team-issues', params: { teamKey: team.key } }" class="font-medium hover:underline">
        {{ team.name }}
      </RouterLink>
      <span class="text-faint" aria-hidden="true">›</span>
      <span class="text-muted tabular-nums" :title="issue.number === 0 ? 'Not saved to the server yet' : undefined">
        {{ identifier }}
      </span>
      <FavoriteButton kind="issue" :target-id="issue.id" />
      <BaseButton v-if="issue.archivedAt" size="sm" class="ml-auto" :loading="restoring" @click="unarchive">
        <ArchiveRestore class="size-3.5" aria-hidden="true" /> Unarchive
      </BaseButton>
      <BaseButton v-else size="sm" variant="ghost" class="ml-auto" @click="archive">
        <Archive class="size-3.5" aria-hidden="true" /> Archive
      </BaseButton>
    </header>
    <p v-if="issue.archivedAt" role="status" class="shrink-0 border-b border-border bg-elevated px-5 py-1.5 text-xs text-muted">
      Archived {{ new Date(issue.archivedAt).toLocaleDateString('en-US', { month: 'short', day: 'numeric', year: 'numeric' }) }}
    </p>

    <div class="flex min-h-0 flex-1 flex-col md:flex-row">
      <div class="min-w-0 flex-1 overflow-y-auto">
        <div class="mx-auto flex max-w-3xl flex-col gap-4 px-8 py-10">
          <p v-if="parent" class="-mb-2 flex items-center gap-1.5 text-xs text-muted">
            Sub-issue of
            <RouterLink :to="issueRoute(parent)" class="truncate font-medium text-fg hover:underline">
              {{ issueIdentifier(parent, sync.team(parent.teamId)) }} {{ parent.title }}
            </RouterLink>
          </p>
          <h1>
            <AutoTextarea
              ref="titleField"
              v-model="title.draft.value"
              label="Issue title"
              placeholder="Issue title"
              :maxlength="500"
              class="text-2xl font-semibold tracking-tight"
              @focus="title.onFocus"
              @blur="title.onBlur"
              @keydown.enter.prevent="titleField?.blur()"
            />
          </h1>
          <div v-if="labels.length > 0" class="flex flex-wrap gap-1.5">
            <LabelChip v-for="label in labels" :key="label.id" :label="label" />
          </div>
          <RichText
            v-model="description.draft.value"
            label="Description"
            placeholder="Add description…"
            class="min-h-24 text-[15px] leading-relaxed"
            :upload="issue.number > 0 ? upload : undefined"
            @focus="description.onFocus"
            @blur="description.onBlur"
          />
          <SubIssues :issue="issue" class="mt-4" />
          <Relations :issue="issue" />
          <PullRequests :issue="issue" />
          <CustomerRequests :issue="issue" />
          <AttachmentList :issue="issue" />
          <ActivityFeed
            :issue="issue"
            class="mt-4 border-t border-border pt-6"
            :upload="issue.number > 0 ? upload : undefined"
            :loading="!detailsLoaded"
            :failed="detailsFailed"
          />
        </div>
      </div>

      <aside
        aria-label="Properties"
        class="flex w-full shrink-0 flex-col gap-0.5 overflow-y-auto border-t border-border p-3 md:w-64 md:border-t-0 md:border-l"
      >
        <h2 class="px-2 pb-1 text-xs font-medium text-faint">Properties</h2>
        <StatusPicker variant="field" :states="states" :state-id="issue.stateId" @update="update({ stateId: $event })" />
        <PriorityPicker variant="field" :priority="issue.priority" @update="update({ priority: $event })" />
        <AssigneePicker variant="field" :assignee-id="issue.assigneeId" @update="update({ assigneeId: $event })" />
        <LabelPicker
          variant="field"
          :label-ids="issue.labelIds"
          @add="update({ addLabelIds: [$event] })"
          @remove="update({ removeLabelIds: [$event] })"
        />
        <ProjectPicker variant="field" :project-id="issue.projectId" @update="update({ projectId: $event })" />
        <MilestonePicker
          v-if="issue.projectId && sync.milestonesForProject(issue.projectId).length > 0"
          variant="field"
          :project-id="issue.projectId"
          :milestone-id="issue.milestoneId"
          @update="update({ milestoneId: $event })"
        />
        <CyclePicker
          v-if="team.cycleWeeks > 0 || issue.cycleId"
          variant="field"
          :team-id="team.id"
          :cycle-id="issue.cycleId"
          @update="update({ cycleId: $event })"
        />
        <IssuePicker
          :label="parent ? 'Change parent issue' : 'Set parent issue'"
          :exclude="[issue.id, ...sync.subIssues(issue.id).map((i) => i.id)]"
          :trigger-class="TRIGGER_CLASS.field"
          @select="update({ parentId: $event })"
        >
          <CornerLeftUp class="size-3.5 shrink-0 text-faint" aria-hidden="true" />
          <span class="truncate" :class="{ 'text-muted': !parent }">
            {{ parent ? issueIdentifier(parent, sync.team(parent.teamId)) : 'Parent issue' }}
          </span>
        </IssuePicker>
        <button
          v-if="parent"
          type="button"
          class="-mt-0.5 ml-7 self-start rounded px-1 text-xs text-faint hover:text-fg"
          @click="update({ parentId: null })"
        >
          Remove parent
        </button>
        <DueDateField variant="field" :due-date="issue.dueDate" @update="update({ dueDate: $event })" />
        <button
          type="button"
          class="mt-2 flex h-8 w-full items-center gap-2 rounded px-2 text-left text-[13px] hover:bg-hover"
          :aria-pressed="subscribed"
          @click="sync.setSubscribed(issue.id, !subscribed)"
        >
          <component :is="subscribed ? Bell : BellOff" class="size-3.5 shrink-0 text-faint" aria-hidden="true" />
          <span :class="{ 'text-muted': !subscribed }">{{ subscribed ? 'Subscribed' : 'Subscribe' }}</span>
        </button>
        <button
          v-if="issue.number > 0 && worksInCode(team.kind)"
          type="button"
          class="flex h-8 w-full items-center gap-2 rounded px-2 text-left text-[13px] hover:bg-hover"
          @click="actions.copyBranch(issue)"
        >
          <GitBranch class="size-3.5 shrink-0 text-faint" aria-hidden="true" />
          <span class="text-muted">Copy branch name</span>
        </button>
        <TimeTracking :issue="issue" :details-loaded="detailsLoaded" />
        <p class="mt-3 border-t border-border px-2 pt-3 text-xs text-faint">
          Created {{ created }}<template v-if="creator"> by {{ creator.name || creator.email }}</template>
        </p>
      </aside>
    </div>
  </template>
  <div v-else-if="looking" class="flex flex-1 items-center justify-center p-8 text-muted" role="status">Loading…</div>
  <div v-else class="flex flex-1 flex-col items-center justify-center gap-1 p-8 text-center">
    <p class="text-muted">Issue not found</p>
  </div>
</template>
