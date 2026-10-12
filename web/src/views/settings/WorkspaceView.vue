<script setup lang="ts">
import { ref, watch } from 'vue'

import { errorMessage } from '@/api/client'
import PortalSettings from '@/components/settings/PortalSettings.vue'
import TimeExport from '@/components/settings/TimeExport.vue'
import SettingsPage from '@/components/SettingsPage.vue'
import BaseButton from '@/components/ui/BaseButton.vue'
import FormError from '@/components/ui/FormError.vue'
import { useSessionStore } from '@/stores/session'
import { useToastStore } from '@/stores/toasts'
import { useWorkspaceStore } from '@/stores/workspace'
import { exitWorkspace } from '@/workspaceExit'

const workspace = useWorkspaceStore()
const session = useSessionStore()
const toasts = useToastStore()

const name = ref(workspace.current?.name ?? '')
watch(
  () => workspace.current?.name,
  (current) => (name.value = current ?? ''),
)
const saving = ref(false)
const leaving = ref(false)
const error = ref('')

const host = window.location.host

async function rename() {
  error.value = ''
  saving.value = true
  try {
    await workspace.rename(name.value.trim())
    toasts.show('Workspace renamed')
  } catch (err) {
    error.value = errorMessage(err)
  } finally {
    saving.value = false
  }
}

async function leave() {
  const current = workspace.current
  if (!current || !session.user) return
  if (!window.confirm(`Leave ${current.name}? You lose access until invited again.`)) return
  error.value = ''
  leaving.value = true
  try {
    await workspace.removeMember(session.user.id)
    await exitWorkspace()
    toasts.show(`You left ${current.name}`)
  } catch (err) {
    error.value = errorMessage(err)
  } finally {
    leaving.value = false
  }
}
</script>

<template>
  <SettingsPage title="General">
    <FormError :message="error" />

    <section aria-labelledby="workspace-name-heading" class="rounded-lg border border-border p-4">
      <h2 id="workspace-name-heading" class="font-medium">Workspace</h2>
      <p class="mt-0.5 text-muted">{{ host }}/{{ workspace.current?.urlKey }}</p>
      <form class="mt-3 flex flex-wrap gap-2" @submit.prevent="rename">
        <input
          v-model="name"
          aria-label="Workspace name"
          required
          maxlength="80"
          :readonly="!workspace.isAdmin"
          class="h-8 min-w-48 flex-1 rounded-md border border-border-strong bg-elevated px-3 text-fg outline-none placeholder:text-faint focus:border-accent read-only:text-muted"
        />
        <BaseButton
          v-if="workspace.isAdmin"
          type="submit"
          variant="primary"
          :loading="saving"
          :disabled="!name.trim() || name.trim() === workspace.current?.name"
        >
          Rename
        </BaseButton>
      </form>
      <p v-if="!workspace.isAdmin" class="mt-2 text-xs text-faint">Admins only.</p>
    </section>

    <PortalSettings v-if="workspace.role !== 'guest'" />

    <TimeExport />

    <section aria-labelledby="leave-heading" class="rounded-lg border border-border p-4">
      <h2 id="leave-heading" class="font-medium">Leave workspace</h2>
      <BaseButton variant="danger" class="mt-3" :loading="leaving" @click="leave">Leave {{ workspace.current?.name }}</BaseButton>
    </section>
  </SettingsPage>
</template>
