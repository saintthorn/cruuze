<script setup lang="ts">
import { Plus, TriangleAlert, X } from 'lucide-vue-next'
import { computed, ref } from 'vue'
import { RouterLink } from 'vue-router'

import RichText from '@/components/editor/LazyRichText.vue'
import PickerMenu from '@/components/PickerMenu.vue'
import BaseButton from '@/components/ui/BaseButton.vue'
import { todayUTC } from '@/lib/issues'
import { TRIGGER_CLASS } from '@/lib/pickerVariants'
import { dependencyProblems, dependsOn, type Health, healthInfo, HEALTHS } from '@/lib/roadmap'
import { timeAgo } from '@/lib/time'
import { useSyncStore } from '@/stores/sync'
import { useWorkspaceStore } from '@/stores/workspace'
import type { Project, ProjectUpdate } from '@/sync/types'

/** Where a project sits among the others: its initiative, what it waits on, and how it is going. */
const props = defineProps<{ project: Project; editable: boolean }>()

const sync = useSyncStore()
const workspace = useWorkspaceStore()

// --- initiative ----------------------------------------------------------
const NONE = ''
const initiative = computed(() => sync.initiative(props.project.initiativeId))
const initiativeOptions = computed(() => [
  { key: NONE, label: 'No initiative' },
  ...sync.initiatives.map((i) => ({ key: i.id, label: i.name })),
])
function setInitiative(id: string) {
  sync.updateRecord('Project', props.project.id, { initiativeId: id === NONE ? null : id })
}
function createInitiative(name: string) {
  setInitiative(sync.createInitiative(name).id)
}

// --- dependencies --------------------------------------------------------
const waitsOn = computed(() => sync.projectDependencies.filter((d) => d.projectId === props.project.id))
const blocks = computed(() => sync.projectDependencies.filter((d) => d.dependsOnId === props.project.id))
// Not itself, not twice, and nothing that already waits on this project.
const candidates = computed(() =>
  sync.projects
    .filter(
      (p) =>
        p.id !== props.project.id &&
        !waitsOn.value.some((d) => d.dependsOnId === p.id) &&
        !dependsOn(sync.projectDependencies, p.id, props.project.id),
    )
    .map((p) => ({ key: p.id, label: p.name })),
)
const problems = computed(() =>
  dependencyProblems(props.project, sync.projectDependencies, sync.project, sync.projectHealth, todayUTC()),
)

// --- updates ---------------------------------------------------------------
const updates = computed(() => sync.projectUpdates(props.project.id))
const latest = computed(() => updates.value[0])
const writing = ref(false)
const health = ref<Health>('onTrack')
const body = ref('')

function startUpdate() {
  health.value = latest.value?.health ?? 'onTrack'
  body.value = ''
  writing.value = true
}
function post() {
  sync.postProjectUpdate(props.project.id, health.value, body.value)
  writing.value = false
}
const author = (update: ProjectUpdate) => {
  const user = sync.user(update.userId)
  return user ? user.name || user.email : 'Someone'
}
const canDelete = (update: ProjectUpdate) => props.editable && (update.userId === sync.currentUserId() || workspace.isAdmin)
</script>

<template>
  <div class="flex max-w-3xl flex-col gap-4">
    <div class="flex flex-wrap items-center gap-1.5">
      <PickerMenu
        v-if="editable"
        :options="initiativeOptions"
        :selected="[project.initiativeId ?? NONE]"
        :label="`Initiative: ${initiative?.name ?? 'none'}. Change initiative`"
        :trigger-class="TRIGGER_CLASS.chip"
        searchable
        creatable
        @select="setInitiative"
        @create="createInitiative"
      >
        <span v-if="initiative" class="size-2 rounded-sm" :style="{ background: initiative.color }" aria-hidden="true" />
        <span :class="{ 'text-muted': !initiative }">{{ initiative?.name ?? 'Initiative' }}</span>
      </PickerMenu>
      <span v-else-if="initiative" class="text-xs text-muted">Part of {{ initiative.name }}</span>

      <span v-if="latest" class="flex items-center gap-1.5 px-1 text-xs" :class="healthInfo(latest.health).class">
        <span class="size-2 rounded-full bg-current" aria-hidden="true" />
        {{ healthInfo(latest.health).label }}
        <span class="text-faint">· {{ timeAgo(latest.createdAt) }}</span>
      </span>
      <button
        v-if="editable && !writing"
        type="button"
        class="flex h-7 items-center gap-1.5 rounded-md px-2 text-xs text-muted hover:bg-hover hover:text-fg"
        @click="startUpdate"
      >
        <Plus class="size-3.5" aria-hidden="true" /> Post update
      </button>
    </div>

    <form v-if="writing" class="flex flex-col gap-2 rounded-lg border border-border p-3" aria-label="Project update" @submit.prevent="post">
      <fieldset class="flex flex-wrap gap-1.5">
        <legend class="sr-only">How is it going?</legend>
        <label
          v-for="option in HEALTHS"
          :key="option.value"
          class="flex h-7 cursor-pointer items-center gap-1.5 rounded-md border px-2 text-xs select-none"
          :class="health === option.value ? ['border-current', option.class] : 'border-border-strong text-muted hover:text-fg'"
        >
          <input v-model="health" type="radio" name="health" :value="option.value" class="sr-only" />
          <span class="size-2 rounded-full bg-current" aria-hidden="true" />
          {{ option.label }}
        </label>
      </fieldset>
      <RichText
        v-model="body"
        label="What changed"
        placeholder="What changed since the last update? (optional)"
        class="min-h-16 rounded-md border border-border-strong bg-elevated px-3 py-2 text-[13px]"
      />
      <div class="flex gap-2">
        <BaseButton type="submit" size="sm" variant="primary">Post update</BaseButton>
        <BaseButton size="sm" variant="ghost" @click="writing = false">Cancel</BaseButton>
      </div>
    </form>

    <ul v-if="problems.length > 0" aria-label="Problems with the plan" class="flex flex-col gap-1 text-xs text-warning">
      <li v-for="problem in problems" :key="problem" class="flex items-center gap-1.5">
        <TriangleAlert class="size-3.5 shrink-0" aria-hidden="true" /> {{ problem }}
      </li>
    </ul>

    <div class="flex flex-col gap-2">
      <div class="flex items-center gap-2">
        <h2 class="font-medium">Dependencies</h2>
        <PickerMenu
          v-if="editable && candidates.length > 0"
          :options="candidates"
          :selected="[]"
          label="Add a project this one waits on"
          trigger-class="flex h-7 items-center gap-1.5 rounded-md px-2 text-xs text-muted hover:bg-hover hover:text-fg"
          searchable
          @select="sync.addProjectDependency(project.id, $event)"
        >
          <Plus class="size-3.5" aria-hidden="true" /> Add project
        </PickerMenu>
      </div>
      <ul v-if="waitsOn.length > 0 || blocks.length > 0" class="divide-y divide-border rounded-lg border border-border">
        <li v-for="dependency in waitsOn" :key="dependency.id" class="flex h-10 items-center gap-2 px-3">
          <span class="w-20 shrink-0 text-xs text-muted">Waits on</span>
          <span class="size-2.5 shrink-0 rounded-sm" :style="{ background: sync.project(dependency.dependsOnId)?.color }" aria-hidden="true" />
          <RouterLink :to="{ name: 'project', params: { projectId: dependency.dependsOnId } }" class="min-w-0 flex-1 truncate hover:underline">
            {{ sync.project(dependency.dependsOnId)?.name }}
          </RouterLink>
          <span class="shrink-0 text-xs text-faint">{{ sync.project(dependency.dependsOnId)?.targetDate }}</span>
          <button
            v-if="editable"
            type="button"
            class="flex size-6 shrink-0 items-center justify-center rounded text-faint hover:bg-hover hover:text-danger"
            :aria-label="`Stop waiting on ${sync.project(dependency.dependsOnId)?.name}`"
            @click="sync.deleteRecord('ProjectDependency', dependency.id)"
          >
            <X class="size-3.5" aria-hidden="true" />
          </button>
        </li>
        <li v-for="dependency in blocks" :key="dependency.id" class="flex h-10 items-center gap-2 px-3">
          <span class="w-20 shrink-0 text-xs text-muted">Holds up</span>
          <span class="size-2.5 shrink-0 rounded-sm" :style="{ background: sync.project(dependency.projectId)?.color }" aria-hidden="true" />
          <RouterLink :to="{ name: 'project', params: { projectId: dependency.projectId } }" class="min-w-0 flex-1 truncate hover:underline">
            {{ sync.project(dependency.projectId)?.name }}
          </RouterLink>
          <span class="shrink-0 text-xs text-faint">{{ sync.project(dependency.projectId)?.targetDate }}</span>
        </li>
      </ul>
    </div>

    <div v-if="updates.length > 0" class="flex flex-col gap-2">
      <h2 class="font-medium">Updates</h2>
      <ol class="flex flex-col gap-2">
        <li v-for="update in updates" :key="update.id" class="group rounded-lg border border-border px-3 py-2">
          <p class="flex items-center gap-1.5 text-xs">
            <span class="flex items-center gap-1.5" :class="healthInfo(update.health).class">
              <span class="size-2 rounded-full bg-current" aria-hidden="true" />
              {{ healthInfo(update.health).label }}
            </span>
            <span class="text-faint">· {{ author(update) }} · {{ timeAgo(update.createdAt) }}</span>
            <button
              v-if="canDelete(update)"
              type="button"
              class="ml-auto rounded p-0.5 text-faint opacity-0 group-hover:opacity-100 hover:text-danger focus-visible:opacity-100"
              aria-label="Delete update"
              @click="sync.deleteRecord('ProjectUpdate', update.id)"
            >
              <X class="size-3.5" aria-hidden="true" />
            </button>
          </p>
          <RichText v-if="update.body" :model-value="update.body" :editable="false" label="Update" class="mt-1 text-[13px]" />
        </li>
      </ol>
    </div>
  </div>
</template>
