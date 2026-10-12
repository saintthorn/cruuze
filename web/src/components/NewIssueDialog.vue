<script setup lang="ts">
import {
  DialogContent,
  DialogDescription,
  DialogOverlay,
  DialogPortal,
  DialogRoot,
  DialogTitle,
} from 'reka-ui'
import { computed, ref, useTemplateRef, watch } from 'vue'
import { useRouter } from 'vue-router'

import { issueIdentifier } from '@/lib/issues'
import { similarIssues } from '@/lib/similar'
import { useSyncStore } from '@/stores/sync'
import { useUiStore } from '@/stores/ui'
import { useWorkspaceStore } from '@/stores/workspace'
import type { Issue } from '@/sync/types'

import AssigneePicker from './issue/AssigneePicker.vue'
import CyclePicker from './issue/CyclePicker.vue'
import DueDateField from './issue/DueDateField.vue'
import LabelPicker from './issue/LabelPicker.vue'
import PriorityPicker from './issue/PriorityPicker.vue'
import ProjectPicker from './issue/ProjectPicker.vue'
import StatusPicker from './issue/StatusPicker.vue'
import StateIcon from './StateIcon.vue'
import BaseButton from './ui/BaseButton.vue'

const sync = useSyncStore()
const ui = useUiStore()
const workspace = useWorkspaceStore()
const router = useRouter()

const teamId = ref('')
const title = ref('')
const description = ref('')
const stateId = ref('')
const priority = ref(0)
const assigneeId = ref<string | undefined>()
const labelIds = ref<string[]>([])
const dueDate = ref<string | undefined>()
const projectId = ref<string | undefined>()
const cycleId = ref<string | undefined>()
const parentId = ref<string | undefined>()
const createMore = ref(false)
const titleInput = useTemplateRef('titleInput')

const team = computed(() => sync.team(teamId.value))
const states = computed(() => sync.statesForTeam(teamId.value))

function defaultStateId() {
  return (states.value.find((s) => s.type === 'backlog') ?? states.value[0])?.id ?? ''
}

function resetFields() {
  title.value = ''
  description.value = ''
  labelIds.value = []
  dueDate.value = undefined
}

watch(
  () => ui.newIssueOpen,
  (open) => {
    if (!open) return
    teamId.value = ui.newIssueTeamId ?? (workspace.myTeams[0] ?? workspace.teams[0])?.id ?? ''
    stateId.value = states.value.some((s) => s.id === ui.newIssueStateId) ? ui.newIssueStateId! : defaultStateId()
    priority.value = 0
    assigneeId.value = undefined
    projectId.value = ui.newIssuePreset.projectId
    cycleId.value = ui.newIssuePreset.cycleId
    parentId.value = ui.newIssuePreset.parentId
    dueDate.value = ui.newIssuePreset.dueDate
    resetFields()
  },
)

// "Does this already exist?": issues with much the same title, found as the
// title is typed. Only looked for once typing pauses.
const similar = ref<Issue[]>([])
let similarTimer: ReturnType<typeof setTimeout> | undefined
watch(title, (value) => {
  clearTimeout(similarTimer)
  if (value.trim().length < 6) {
    similar.value = []
    return
  }
  similarTimer = setTimeout(() => (similar.value = similarIssues(value, sync.allIssues, 4)), 200)
})

function openSimilar(issue: Issue) {
  ui.newIssueOpen = false
  const identifier = issueIdentifier(issue, sync.team(issue.teamId))
  void router.push({ name: 'issue', params: { issueRef: issue.number > 0 ? identifier : issue.id } })
}

const parent = computed(() => sync.issue(parentId.value))
const templates = computed(() => sync.templatesForTeam(teamId.value))

/** Fills the form from a template; anything already typed in the title stays. */
function applyTemplate(event: Event) {
  const select = event.target as HTMLSelectElement
  const template = templates.value.find((t) => t.id === select.value)
  select.value = ''
  if (!template) return
  if (!title.value.trim()) title.value = template.title
  description.value = template.description
  priority.value = template.priority
  labelIds.value = [...template.labelIds]
  titleInput.value?.focus()
}

// Statuses and cycles belong to a team, so switching team resets them.
watch(teamId, () => {
  if (!states.value.some((s) => s.id === stateId.value)) stateId.value = defaultStateId()
  if (cycleId.value && sync.cycle(cycleId.value)?.teamId !== teamId.value) cycleId.value = undefined
})

function submit() {
  if (!title.value.trim() || !teamId.value) return
  // Shows up in the list at once; the server catches up in the background.
  sync.createIssue({
    teamId: teamId.value,
    title: title.value,
    description: description.value,
    stateId: stateId.value || undefined,
    priority: priority.value,
    assigneeId: assigneeId.value,
    labelIds: [...labelIds.value],
    dueDate: dueDate.value,
    projectId: projectId.value,
    cycleId: cycleId.value,
    parentId: parentId.value,
  })
  if (createMore.value) {
    // Keep team, status, priority and assignee for the next one.
    resetFields()
    titleInput.value?.focus()
  } else {
    ui.newIssueOpen = false
  }
}
</script>

<template>
  <DialogRoot v-model:open="ui.newIssueOpen">
    <DialogPortal>
      <DialogOverlay class="fixed inset-0 z-40 bg-black/50" />
      <DialogContent
        @open-auto-focus.prevent="titleInput?.focus()"
        class="fixed top-[12%] left-1/2 z-50 w-[calc(100%-2rem)] max-w-2xl -translate-x-1/2 rounded-xl border border-border-strong bg-elevated shadow-2xl"
      >
        <form @submit.prevent="submit" @keydown.meta.enter.prevent="submit" @keydown.ctrl.enter.prevent="submit">
          <div class="flex items-center gap-2 px-5 pt-4 text-xs text-muted">
            <select
              v-model="teamId"
              aria-label="Team"
              class="h-6 rounded border border-border-strong bg-active px-1.5 font-medium text-fg outline-none focus:border-accent"
            >
              <option v-for="option in workspace.teams" :key="option.id" :value="option.id">{{ option.key }}</option>
            </select>
            <span aria-hidden="true">›</span>
            <DialogTitle>{{ parent ? 'New sub-issue' : 'New issue' }}</DialogTitle>
            <span v-if="parent" class="truncate">of {{ issueIdentifier(parent, sync.team(parent.teamId)) }} {{ parent.title }}</span>
            <select
              v-if="templates.length > 0"
              aria-label="Start from a template"
              class="ml-auto h-6 rounded border border-border-strong bg-elevated px-1.5 text-fg outline-none focus:border-accent"
              @change="applyTemplate"
            >
              <option value="">Template…</option>
              <option v-for="template in templates" :key="template.id" :value="template.id">{{ template.name }}</option>
            </select>
          </div>
          <DialogDescription class="sr-only">
            Create an issue{{ team ? ` in ${team.name}` : '' }}. Press Command or Control plus Enter to save.
          </DialogDescription>

          <div class="px-5 py-3">
            <input
              ref="titleInput"
              v-model="title"
              aria-label="Issue title"
              placeholder="Issue title"
              required
              maxlength="500"
              class="w-full bg-transparent text-lg font-medium text-fg outline-none placeholder:text-faint"
            />
            <textarea
              v-model="description"
              aria-label="Description"
              placeholder="Add description…"
              rows="4"
              class="mt-2 w-full resize-none bg-transparent text-fg outline-none placeholder:text-faint"
            />
          </div>

          <div v-if="similar.length > 0" class="mx-5 mb-3 border border-border text-xs" role="status">
            <p class="border-b border-border px-2 py-1 text-faint">Similar issues</p>
            <ul>
              <li v-for="match in similar" :key="match.id">
                <button
                  type="button"
                  class="flex h-7 w-full items-center gap-2 px-2 text-left hover:bg-hover"
                  @click="openSimilar(match)"
                >
                  <StateIcon v-if="sync.state(match.stateId)" :type="sync.state(match.stateId)!.type" :color="sync.state(match.stateId)!.color" />
                  <span class="shrink-0 text-muted tabular-nums">{{ issueIdentifier(match, sync.team(match.teamId)) }}</span>
                  <span class="truncate">{{ match.title }}</span>
                </button>
              </li>
            </ul>
          </div>

          <div class="flex flex-wrap items-center gap-1.5 px-5 pb-3">
            <StatusPicker variant="chip" :states="states" :state-id="stateId" @update="stateId = $event" />
            <PriorityPicker variant="chip" :priority="priority" @update="priority = $event" />
            <AssigneePicker variant="chip" :assignee-id="assigneeId" @update="assigneeId = $event ?? undefined" />
            <LabelPicker
              variant="chip"
              :label-ids="labelIds"
              @add="labelIds.push($event)"
              @remove="labelIds = labelIds.filter((id) => id !== $event)"
            />
            <ProjectPicker variant="chip" :project-id="projectId" @update="projectId = $event ?? undefined" />
            <CyclePicker
              v-if="team && team.cycleWeeks > 0"
              variant="chip"
              :team-id="team.id"
              :cycle-id="cycleId"
              @update="cycleId = $event ?? undefined"
            />
            <DueDateField variant="chip" :due-date="dueDate" @update="dueDate = $event ?? undefined" />
          </div>

          <div class="flex items-center gap-3 border-t border-border px-5 py-3">
            <label class="ml-auto flex items-center gap-1.5 text-xs text-muted select-none">
              <input v-model="createMore" type="checkbox" class="accent-(--accent)" /> Create more
            </label>
            <BaseButton variant="ghost" @click="ui.newIssueOpen = false">Cancel</BaseButton>
            <BaseButton type="submit" variant="primary" :disabled="!teamId">Create issue</BaseButton>
          </div>
        </form>
      </DialogContent>
    </DialogPortal>
  </DialogRoot>
</template>
