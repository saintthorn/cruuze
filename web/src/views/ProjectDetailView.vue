<script setup lang="ts">
import { Plus, Trash2 } from 'lucide-vue-next'
import { computed, ref } from 'vue'
import { RouterLink, useRoute, useRouter } from 'vue-router'

import DisplayOptions from '@/components/DisplayOptions.vue'
import RichText from '@/components/editor/LazyRichText.vue'
import FavoriteButton from '@/components/FavoriteButton.vue'
import FilterMenu from '@/components/FilterMenu.vue'
import ForecastLine from '@/components/ForecastLine.vue'
import AssigneePicker from '@/components/issue/AssigneePicker.vue'
import DueDateField from '@/components/issue/DueDateField.vue'
import IssueView from '@/components/IssueView.vue'
import PickerMenu from '@/components/PickerMenu.vue'
import ProjectPlanning from '@/components/project/ProjectPlanning.vue'
import ProgressBar from '@/components/ProgressBar.vue'
import PromptDialog from '@/components/PromptDialog.vue'
import BaseButton from '@/components/ui/BaseButton.vue'
import { useDraft } from '@/composables/useDraft'
import { isClosed } from '@/lib/filter'
import { progressOf, PROJECT_STATUSES, projectStatusLabel } from '@/lib/issues'
import { TRIGGER_CLASS } from '@/lib/pickerVariants'
import { useSyncStore } from '@/stores/sync'
import { useUiStore } from '@/stores/ui'
import { useViewOptionsStore } from '@/stores/viewOptions'
import { useWorkspaceStore } from '@/stores/workspace'
import type { Milestone, Project } from '@/sync/types'

const route = useRoute()
const router = useRouter()
const sync = useSyncStore()
const ui = useUiStore()
const workspace = useWorkspaceStore()
const viewOptions = useViewOptionsStore()

const project = computed(() => sync.project(String(route.params.projectId)))
const issues = computed(() => sync.issues.filter((i) => i.projectId === project.value?.id))
const remaining = computed(() => issues.value.filter((i) => !isClosed(i, sync.state)).length)
const milestones = computed(() => (project.value ? sync.milestonesForProject(project.value.id) : []))
// A project can hold issues from several teams; filters cover all their states.
const states = computed(() => [...new Set(issues.value.map((i) => i.teamId))].flatMap((id) => sync.statesForTeam(id)))

const viewKey = computed(() => `project:${route.params.projectId}`)
const options = computed(() => viewOptions.optionsFor(viewKey.value))
const editable = computed(() => workspace.canManageLabels)

function update(patch: Record<string, unknown>) {
  if (project.value) sync.updateRecord('Project', project.value.id, patch)
}

const name = useDraft(
  () => project.value?.name ?? '',
  (value) => {
    if (value.trim()) update({ name: value.trim() })
  },
)
const description = useDraft(
  () => project.value?.description ?? '',
  (value) => update({ description: value }),
)

const statusOptions = PROJECT_STATUSES.map((s) => ({ key: s.value as Project['status'], label: s.label }))

const milestoneOpen = ref(false)
const milestoneProgress = (milestone: Milestone) =>
  progressOf(issues.value.filter((i) => i.milestoneId === milestone.id), sync.state)

function removeMilestone(milestone: Milestone) {
  if (window.confirm(`Delete the milestone "${milestone.name}"? Its issues stay in the project.`)) {
    sync.deleteRecord('Milestone', milestone.id)
  }
}

function remove() {
  if (!project.value) return
  if (!window.confirm(`Delete the project "${project.value.name}"? Its issues are kept, without a project.`)) return
  sync.deleteRecord('Project', project.value.id)
  void router.push({ name: 'projects' })
}
</script>

<template>
  <template v-if="project">
    <header class="flex h-11 shrink-0 items-center gap-2 border-b border-border px-5">
      <RouterLink :to="{ name: 'projects' }" class="text-muted hover:text-fg">Projects</RouterLink>
      <span class="text-faint" aria-hidden="true">›</span>
      <span class="truncate font-medium">{{ project.name }}</span>
      <FavoriteButton kind="project" :target-id="project.id" />
      <div class="ml-auto flex items-center gap-2">
        <FilterMenu :filter="options.filter" :states="states" />
        <DisplayOptions :options="options" />
        <BaseButton size="sm" @click="ui.openNewIssue(undefined, undefined, { projectId: project.id })">
          <Plus class="size-3.5" aria-hidden="true" /> New issue
        </BaseButton>
      </div>
    </header>

    <div class="flex min-h-0 flex-1 flex-col">
      <section class="flex max-h-[55%] shrink-0 flex-col gap-3 overflow-y-auto border-b border-border px-5 py-5" aria-label="Project details">
        <div class="flex items-center gap-3">
          <span class="size-4 shrink-0 rounded" :style="{ background: project.color }" aria-hidden="true" />
          <input
            v-model="name.draft.value"
            aria-label="Project name"
            maxlength="80"
            :readonly="!editable"
            class="min-w-0 flex-1 bg-transparent text-xl font-semibold tracking-tight text-fg outline-none"
            @focus="name.onFocus"
            @blur="name.onBlur"
            @keydown.enter.prevent="($event.target as HTMLInputElement).blur()"
          />
          <button
            v-if="editable"
            type="button"
            class="flex size-7 shrink-0 items-center justify-center rounded text-faint hover:bg-hover hover:text-danger"
            aria-label="Delete project"
            title="Delete project"
            @click="remove"
          >
            <Trash2 class="size-3.5" aria-hidden="true" />
          </button>
        </div>

        <div class="flex flex-wrap items-center gap-1.5">
          <PickerMenu
            :options="statusOptions"
            :selected="[project.status]"
            :label="`Status: ${projectStatusLabel(project.status)}. Change status`"
            :trigger-class="TRIGGER_CLASS.chip"
            @select="update({ status: $event })"
          >
            {{ projectStatusLabel(project.status) }}
          </PickerMenu>
          <AssigneePicker variant="chip" :assignee-id="project.leadId" @update="update({ leadId: $event })" />
          <span class="flex items-center gap-1 text-xs text-muted">
            Start <DueDateField variant="chip" :due-date="project.startDate" :warn-overdue="false" @update="update({ startDate: $event })" />
          </span>
          <span class="flex items-center gap-1 text-xs text-muted">
            Target <DueDateField variant="chip" :due-date="project.targetDate" @update="update({ targetDate: $event })" />
          </span>
          <ProgressBar :progress="sync.progressOfProject(project.id)" class="ml-2" />
        </div>
        <ForecastLine :scope="{ projectId: project.id }" :remaining="remaining" />
        <ProjectPlanning :project="project" :editable="editable" />

        <RichText
          v-model="description.draft.value"
          label="Project description"
          placeholder="Describe the project…"
          :editable="editable"
          class="max-w-3xl text-[14px] leading-relaxed"
          @focus="description.onFocus"
          @blur="description.onBlur"
        />

        <div class="flex flex-col gap-2">
          <div class="flex items-center gap-2">
            <h2 class="font-medium">Milestones</h2>
            <button
              v-if="editable"
              type="button"
              class="flex h-7 items-center gap-1.5 rounded-md px-2 text-xs text-muted hover:bg-hover hover:text-fg"
              @click="milestoneOpen = true"
            >
              <Plus class="size-3.5" aria-hidden="true" /> Add milestone
            </button>
          </div>
          <ul v-if="milestones.length > 0" class="max-w-3xl divide-y divide-border rounded-lg border border-border">
            <li v-for="milestone in milestones" :key="milestone.id" class="flex h-10 items-center gap-3 px-3">
              <span class="min-w-0 flex-1 truncate">{{ milestone.name }}</span>
              <DueDateField
                variant="chip"
                :due-date="milestone.targetDate"
                @update="sync.updateRecord('Milestone', milestone.id, { targetDate: $event })"
              />
              <ProgressBar :progress="milestoneProgress(milestone)" />
              <button
                v-if="editable"
                type="button"
                class="flex size-6 shrink-0 items-center justify-center rounded text-faint hover:bg-hover hover:text-danger"
                :aria-label="`Delete milestone ${milestone.name}`"
                @click="removeMilestone(milestone)"
              >
                <Trash2 class="size-3.5" aria-hidden="true" />
              </button>
            </li>
          </ul>
        </div>
      </section>

      <IssueView v-if="issues.length > 0" :view-key="viewKey" :issues="issues" :states="states" />
      <p v-else class="p-8 text-center text-muted">No issues in this project yet.</p>
    </div>

    <PromptDialog
      v-model:open="milestoneOpen"
      title="New milestone"
      label="Milestone name"
      placeholder="Beta"
      action="Add milestone"
      @submit="sync.createMilestone(project.id, $event)"
    />
  </template>
  <div v-else class="flex flex-1 flex-col items-center justify-center gap-1 p-8 text-center">
    <p class="font-medium">Project not found</p>
    <p class="text-muted">It may have been deleted.</p>
  </div>
</template>
