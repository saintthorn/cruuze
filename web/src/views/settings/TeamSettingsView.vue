<script setup lang="ts">
import { Lock, Trash2 } from 'lucide-vue-next'

import { TEAM_KINDS, type TeamKind } from '@/lib/teamKinds'
import { computed, ref, watch } from 'vue'
import { useRoute, useRouter } from 'vue-router'

import { errorMessage } from '@/api/client'
import LabelPicker from '@/components/issue/LabelPicker.vue'
import PriorityPicker from '@/components/issue/PriorityPicker.vue'
import PromptDialog from '@/components/PromptDialog.vue'
import ImportExport from '@/components/settings/ImportExport.vue'
import WorkflowStates from '@/components/settings/WorkflowStates.vue'
import SettingsPage from '@/components/SettingsPage.vue'
import BaseButton from '@/components/ui/BaseButton.vue'
import FormError from '@/components/ui/FormError.vue'
import UserAvatar from '@/components/UserAvatar.vue'
import { useSessionStore } from '@/stores/session'
import { useSyncStore } from '@/stores/sync'
import { useWorkspaceStore } from '@/stores/workspace'
import type { Template, UserRecord } from '@/sync/types'

const route = useRoute()
const router = useRouter()
const sync = useSyncStore()
const workspace = useWorkspaceStore()
const session = useSessionStore()

const team = computed(() => workspace.teams.find((t) => t.key === route.params.teamKey))
const startedStates = computed(() => (team.value ? sync.statesForTeam(team.value.id).filter((s) => s.type === 'started') : []))
const templates = computed(() => (team.value ? sync.templatesForTeam(team.value.id) : []))
// Members run their team; admins can step in.
const editable = computed(() => team.value !== undefined && (team.value.isMember || workspace.isAdmin) && workspace.canCreateTeams)

const error = ref('')
const saving = ref(false)
const templateOpen = ref(false)

const members = computed(() => (team.value ? sync.teamMembers(team.value.id) : []))
// Guests are added to teams one by one; that is the point of the role.
const addable = computed(() => sync.users.filter((u) => !members.value.some((m) => m.id === u.id)))
const adding = ref('')

const name = ref('')
watch(
  () => team.value?.name,
  (current) => (name.value = current ?? ''),
  { immediate: true },
)

const deleting = ref(false)

/** Deletes the team after a warning that names what goes with it. */
async function remove() {
  if (!team.value) return
  const count = sync.issuesForTeam(team.value.id).length
  const issues = count === 1 ? '1 issue' : `${count.toLocaleString()} issues`
  if (!window.confirm(`Delete ${team.value.name}? Its ${issues}, statuses and cycles are deleted. This cannot be undone.`)) return
  error.value = ''
  deleting.value = true
  try {
    await workspace.deleteTeam(team.value.id)
    await router.replace({ name: 'settings-teams' })
  } catch (err) {
    error.value = errorMessage(err)
  } finally {
    deleting.value = false
  }
}

function rename() {
  const next = name.value.trim()
  if (next && next !== team.value?.name) void change({ name: next })
  else name.value = team.value?.name ?? ''
}

function setPrivate(event: Event) {
  const box = event.target as HTMLInputElement
  const makePrivate = box.checked
  const question = makePrivate
    ? `Make ${team.value?.name} private? Only members will see it.`
    : `Make ${team.value?.name} public? Everyone except guests will see it.`
  if (!window.confirm(question)) {
    box.checked = !makePrivate
    return
  }
  void change({ private: makePrivate }).then(() => (box.checked = team.value?.private ?? false))
}

async function setMember(user: UserRecord, isMember: boolean) {
  if (!team.value) return
  const self = user.id === session.user?.id
  if (!isMember && self && team.value.private && !window.confirm(`Leave ${team.value.name}? It is private; you can't rejoin on your own.`)) return
  error.value = ''
  saving.value = true
  try {
    await workspace.setTeamMember(team.value.id, user.id, isMember)
  } catch (err) {
    error.value = errorMessage(err)
  } finally {
    saving.value = false
    adding.value = ''
  }
}

function add() {
  const user = sync.user(adding.value)
  if (user) void setMember(user, true)
}

async function change(settings: Parameters<typeof workspace.updateTeam>[1]) {
  if (!team.value) return
  error.value = ''
  saving.value = true
  try {
    await workspace.updateTeam(team.value.id, settings)
  } catch (err) {
    error.value = errorMessage(err)
  } finally {
    saving.value = false
  }
}

function patch(template: Template, field: 'name' | 'title' | 'description', event: Event) {
  const value = (event.target as HTMLInputElement | HTMLTextAreaElement).value
  if (field === 'name' && !value.trim()) return
  if (value !== template[field]) sync.updateRecord('Template', template.id, { [field]: value })
}

function toggleLabel(template: Template, labelId: string, on: boolean) {
  const labelIds = on ? [...template.labelIds, labelId] : template.labelIds.filter((id) => id !== labelId)
  sync.updateRecord('Template', template.id, { labelIds })
}

const fieldClass =
  'w-full rounded-md border border-border-strong bg-elevated px-3 py-1.5 text-fg outline-none placeholder:text-faint focus:border-accent'
</script>

<template>
  <SettingsPage
    v-if="team"
    :title="team.name"
  >
    <FormError :message="error" />

    <section aria-labelledby="team-general-heading" class="rounded-lg border border-border p-4">
      <h2 id="team-general-heading" class="font-medium">Team</h2>
      <p class="mt-0.5 text-muted">Identifier: {{ team.key }}</p>
      <form class="mt-3 flex flex-wrap gap-2" @submit.prevent="rename">
        <input
          v-model="name"
          aria-label="Team name"
          required
          maxlength="80"
          :readonly="!editable"
          class="h-8 min-w-48 flex-1 rounded-md border border-border-strong bg-elevated px-3 text-fg outline-none focus:border-accent read-only:text-muted"
        />
        <BaseButton v-if="editable" type="submit" :loading="saving" :disabled="!name.trim() || name.trim() === team.name">Rename</BaseButton>
      </form>
      <label class="mt-3 flex flex-wrap items-center gap-2">
        Kind
        <select
          :value="team.kind"
          :disabled="!editable || saving"
          class="h-8 rounded-md border border-border-strong bg-elevated px-2 text-fg outline-none focus:border-accent"
          @change="change({ kind: ($event.target as HTMLSelectElement).value as TeamKind })"
        >
          <option v-for="option in TEAM_KINDS" :key="option.value" :value="option.value">{{ option.label }}</option>
        </select>
      </label>
      <label class="mt-3 flex items-start gap-2 select-none">
        <input
          type="checkbox"
          :checked="team.private"
          :disabled="!editable || !team.isMember || saving"
          class="mt-0.5 size-3.5 accent-(--accent)"
          @change="setPrivate"
        />
        <span>
          <span class="flex items-center gap-1.5">Private <Lock v-if="team.private" class="size-3 text-faint" aria-hidden="true" /></span>
          <span class="block text-xs text-faint">
            Visible to members only.
            <template v-if="editable && !team.isMember">Join the team to change this.</template>
          </span>
        </span>
      </label>
    </section>

    <section aria-labelledby="team-members-heading" class="rounded-lg border border-border p-4">
      <div class="flex items-center gap-2">
        <h2 id="team-members-heading" class="font-medium">Members</h2>
        <span class="text-faint">{{ members.length }}</span>
      </div>
      <div v-if="team.isMember || workspace.isAdmin" class="mt-3 flex flex-wrap gap-2">
        <select
          v-model="adding"
          aria-label="Person to add"
          :disabled="addable.length === 0 || saving"
          class="h-8 min-w-48 flex-1 rounded-md border border-border-strong bg-elevated px-2 text-fg outline-none focus:border-accent disabled:opacity-50"
        >
          <option value="">{{ addable.length === 0 ? 'Everyone is a member' : 'Add…' }}</option>
          <option v-for="user in addable" :key="user.id" :value="user.id">
            {{ user.name || user.email }}{{ user.role === 'guest' ? ' (guest)' : '' }}
          </option>
        </select>
        <BaseButton :disabled="!adding" :loading="saving && adding !== ''" @click="add">Add to team</BaseButton>
      </div>
      <ul v-if="members.length > 0" aria-label="Team members" class="mt-3 divide-y divide-border border-t border-border">
        <li v-for="member in members" :key="member.id" class="flex h-11 items-center gap-3">
          <UserAvatar :name="member.name" :email="member.email" :avatar-url="member.avatarUrl" :size="22" />
          <span class="min-w-0 flex-1 truncate">{{ member.name || member.email }}</span>
          <span v-if="member.role !== 'member'" class="text-xs text-faint capitalize">{{ member.role }}</span>
          <BaseButton
            v-if="member.id === session.user?.id || workspace.isAdmin"
            size="sm"
            variant="ghost"
            :disabled="saving"
            :aria-label="member.id === session.user?.id ? 'Leave the team' : `Remove ${member.name || member.email} from the team`"
            @click="setMember(member, false)"
          >
            {{ member.id === session.user?.id ? 'Leave' : 'Remove' }}
          </BaseButton>
        </li>
      </ul>
    </section>

    <WorkflowStates :team="team" :editable="editable" />

    <section aria-labelledby="cycles-heading" class="rounded-lg border border-border p-4">
      <h2 id="cycles-heading" class="font-medium">Cycles</h2>
      <label class="mt-3 flex items-center gap-2 text-muted">
        Length
        <select
          :value="team.cycleWeeks"
          :disabled="!editable || saving"
          class="h-8 rounded-md border border-border-strong bg-elevated px-2 text-fg outline-none focus:border-accent disabled:opacity-50"
          @change="change({ cycleWeeks: Number(($event.target as HTMLSelectElement).value) })"
        >
          <option :value="0">Off</option>
          <option v-for="weeks in [1, 2, 3, 4, 6, 8]" :key="weeks" :value="weeks">{{ weeks === 1 ? '1 week' : `${weeks} weeks` }}</option>
        </select>
      </label>
    </section>

    <section aria-labelledby="triage-heading" class="rounded-lg border border-border p-4">
      <h2 id="triage-heading" class="font-medium">Triage</h2>
      <label class="mt-3 flex items-center gap-2 select-none">
        <input
          type="checkbox"
          :checked="team.triageEnabled"
          :disabled="!editable || saving"
          class="size-3.5 accent-(--accent)"
          @change="change({ triageEnabled: ($event.target as HTMLInputElement).checked })"
        />
        Send outside issues to triage
      </label>
    </section>

    <section aria-labelledby="templates-heading" class="rounded-lg border border-border p-4">
      <div class="flex items-center gap-2">
        <h2 id="templates-heading" class="font-medium">Issue templates</h2>
        <BaseButton v-if="editable" size="sm" class="ml-auto" @click="templateOpen = true">New template</BaseButton>
      </div>
      <p class="mt-0.5 text-muted">Presets you can start a new issue from.</p>
      <ul v-if="templates.length > 0" class="mt-3 flex flex-col gap-3">
        <li v-for="template in templates" :key="template.id" class="flex flex-col gap-2 rounded-md border border-border p-3">
          <div class="flex items-center gap-2">
            <input
              :value="template.name"
              :aria-label="`Name of template ${template.name}`"
              :readonly="!editable"
              maxlength="80"
              class="min-w-0 flex-1 bg-transparent font-medium text-fg outline-none"
              @change="patch(template, 'name', $event)"
            />
            <button
              v-if="editable"
              type="button"
              class="flex size-6 items-center justify-center rounded text-faint hover:bg-hover hover:text-danger"
              :aria-label="`Delete template ${template.name}`"
              @click="sync.deleteRecord('Template', template.id)"
            >
              <Trash2 class="size-3.5" aria-hidden="true" />
            </button>
          </div>
          <input
            :value="template.title"
            :aria-label="`Issue title for template ${template.name}`"
            :readonly="!editable"
            placeholder="Issue title"
            maxlength="500"
            :class="fieldClass"
            @change="patch(template, 'title', $event)"
          />
          <textarea
            :value="template.description"
            :aria-label="`Issue description for template ${template.name}`"
            :readonly="!editable"
            placeholder="Issue description (Markdown)"
            rows="3"
            :class="fieldClass"
            @change="patch(template, 'description', $event)"
          />
          <div v-if="editable" class="flex flex-wrap gap-1.5">
            <PriorityPicker
              variant="chip"
              :priority="template.priority"
              @update="sync.updateRecord('Template', template.id, { priority: $event })"
            />
            <LabelPicker
              variant="chip"
              :label-ids="template.labelIds"
              @add="toggleLabel(template, $event, true)"
              @remove="toggleLabel(template, $event, false)"
            />
          </div>
        </li>
      </ul>
    </section>

    <section aria-labelledby="automation-heading" class="rounded-lg border border-border p-4">
      <h2 id="automation-heading" class="font-medium">Automation</h2>
      <div class="mt-3 flex flex-col gap-2">
        <label
          v-for="option in [
            { key: 'autoAssign', label: 'Assign to whoever starts it' },
            { key: 'autoCycle', label: 'Add to the current cycle when started' },
            { key: 'autoCloseParent', label: 'Complete the parent when all sub-issues close' },
          ] as const"
          :key="option.key"
          class="flex items-center gap-2 select-none"
        >
          <input
            type="checkbox"
            :checked="team[option.key]"
            :disabled="!editable || saving"
            class="size-3.5 accent-(--accent)"
            @change="change({ [option.key]: ($event.target as HTMLInputElement).checked })"
          />
          {{ option.label }}
        </label>
        <label class="mt-1 flex flex-wrap items-center gap-2 text-muted">
          On review request, move to
          <select
            :value="team.reviewStateId ?? ''"
            :disabled="!editable || saving"
            aria-label="Review status"
            class="h-8 rounded-md border border-border-strong bg-elevated px-2 text-fg outline-none focus:border-accent disabled:opacity-50"
            @change="change({ reviewStateId: ($event.target as HTMLSelectElement).value || null })"
          >
            <option value="">(no change)</option>
            <option v-for="state in startedStates" :key="state.id" :value="state.id">{{ state.name }}</option>
          </select>
        </label>
      </div>
    </section>

    <section aria-labelledby="archive-heading" class="rounded-lg border border-border p-4">
      <h2 id="archive-heading" class="font-medium">Cleanup</h2>
      <label class="mt-3 flex items-center gap-2 text-muted">
        Flag as stale after
        <select
          :value="team.staleDays"
          :disabled="!editable || saving"
          class="h-8 rounded-md border border-border-strong bg-elevated px-2 text-fg outline-none focus:border-accent disabled:opacity-50"
          @change="change({ staleDays: Number(($event.target as HTMLSelectElement).value) })"
        >
          <option v-for="days in [14, 30, 60, 90, 180]" :key="days" :value="days">{{ days }} days</option>
          <option :value="0">Never</option>
        </select>
      </label>
      <label class="mt-3 flex flex-wrap items-center gap-2 text-muted">
        Cancel stale issues after
        <select
          :value="team.autoCloseDays"
          :disabled="!editable || saving"
          class="h-8 rounded-md border border-border-strong bg-elevated px-2 text-fg outline-none focus:border-accent disabled:opacity-50"
          @change="change({ autoCloseDays: Number(($event.target as HTMLSelectElement).value) })"
        >
          <option :value="0">Never</option>
          <option v-for="days in [90, 180, 365]" :key="days" :value="days">{{ days }} days</option>
        </select>
        <span class="basis-full text-xs text-faint">Unstarted issues only.</span>
      </label>
      <label class="mt-3 flex items-center gap-2 text-muted">
        Archive closed after
        <select
          :value="team.autoArchiveDays"
          :disabled="!editable || saving"
          class="h-8 rounded-md border border-border-strong bg-elevated px-2 text-fg outline-none focus:border-accent disabled:opacity-50"
          @change="change({ autoArchiveDays: Number(($event.target as HTMLSelectElement).value) })"
        >
          <option v-for="days in [30, 90, 180, 365]" :key="days" :value="days">{{ days }} days</option>
          <option :value="0">Never</option>
        </select>
      </label>
    </section>

    <ImportExport :team="team" />

    <section v-if="workspace.isAdmin" aria-labelledby="delete-team-heading" class="rounded-lg border border-border p-4">
      <h2 id="delete-team-heading" class="font-medium">Delete team</h2>
      <p class="mt-0.5 text-muted">Everything in it goes too.</p>
      <BaseButton variant="danger" class="mt-3" :loading="deleting" @click="remove"><Trash2 class="size-3.5" aria-hidden="true" /> Delete {{ team.name }}</BaseButton>
    </section>

    <PromptDialog
      v-model:open="templateOpen"
      title="New template"
      label="Template name"
      placeholder="Bug report"
      action="Create template"
      @submit="sync.createTemplate(team.id, $event)"
    />
  </SettingsPage>
  <SettingsPage v-else title="Team not found" />
</template>
