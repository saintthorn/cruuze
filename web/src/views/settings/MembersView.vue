<script setup lang="ts">
import { onMounted, ref } from 'vue'

import { errorMessage, type WorkspaceRole } from '@/api/client'
import SettingsPage from '@/components/SettingsPage.vue'
import BaseButton from '@/components/ui/BaseButton.vue'
import FormError from '@/components/ui/FormError.vue'
import UserAvatar from '@/components/UserAvatar.vue'
import { useSessionStore } from '@/stores/session'
import { useSyncStore } from '@/stores/sync'
import { useWorkspaceStore } from '@/stores/workspace'
import type { UserRecord } from '@/sync/types'

const workspace = useWorkspaceStore()
const sync = useSyncStore()
const session = useSessionStore()

const memberError = ref('')
/** ID of the member being changed. */
const busy = ref<string | null>(null)

async function change(member: UserRecord, action: () => Promise<void>) {
  memberError.value = ''
  busy.value = member.id
  try {
    await action()
  } catch (err) {
    memberError.value = errorMessage(err)
  } finally {
    busy.value = null
  }
}

function setRole(member: UserRecord, event: Event) {
  const select = event.target as HTMLSelectElement
  const role = select.value as WorkspaceRole
  // Shows the old role again until the change comes back confirmed.
  select.value = member.role
  void change(member, () => workspace.setMemberRole(member.id, role))
}

function remove(member: UserRecord) {
  const name = member.name || member.email
  if (!window.confirm(`Remove ${name} from ${workspace.current?.name}?`)) return
  void change(member, () => workspace.removeMember(member.id))
}

const loadError = ref('')
const email = ref('')
const role = ref<WorkspaceRole>('member')
const inviting = ref(false)
const inviteError = ref('')

onMounted(async () => {
  if (!workspace.isAdmin) return
  try {
    await workspace.loadInvites()
  } catch (err) {
    loadError.value = errorMessage(err)
  }
})

async function invite() {
  inviteError.value = ''
  inviting.value = true
  try {
    await workspace.invite(email.value.trim(), role.value)
    email.value = ''
  } catch (err) {
    inviteError.value = errorMessage(err)
  } finally {
    inviting.value = false
  }
}

async function revoke(inviteId: string) {
  inviteError.value = ''
  try {
    await workspace.revokeInvite(inviteId)
  } catch (err) {
    inviteError.value = errorMessage(err)
  }
}
</script>

<template>
  <SettingsPage title="Members">
    <section v-if="workspace.isAdmin" aria-labelledby="invite-heading" class="rounded-lg border border-border p-4">
      <h2 id="invite-heading" class="font-medium">Invite people</h2>
      <form class="mt-3 flex flex-wrap gap-2" @submit.prevent="invite">
        <input
          v-model="email"
          type="email"
          required
          aria-label="Email address"
          placeholder="teammate@company.com"
          class="h-8 min-w-48 flex-1 rounded-md border border-border-strong bg-elevated px-3 text-fg outline-none placeholder:text-faint focus:border-accent"
        />
        <select
          v-model="role"
          aria-label="Role"
          class="h-8 rounded-md border border-border-strong bg-elevated px-2 text-fg outline-none focus:border-accent"
        >
          <option value="admin">Admin</option>
          <option value="member">Member</option>
          <option value="guest">Guest</option>
        </select>
        <BaseButton type="submit" variant="primary" :loading="inviting">Send invite</BaseButton>
      </form>
      <FormError :message="inviteError" class="mt-2" />

      <ul v-if="workspace.invites.length > 0" class="mt-4 divide-y divide-border border-t border-border">
        <li v-for="pending in workspace.invites" :key="pending.id" class="flex h-11 items-center gap-3">
          <span class="min-w-0 flex-1 truncate">{{ pending.email }}</span>
          <span class="text-xs text-faint capitalize">{{ pending.role }} · Pending</span>
          <BaseButton size="sm" variant="ghost" @click="revoke(pending.id)">Revoke</BaseButton>
        </li>
      </ul>
    </section>

    <FormError :message="loadError" />
    <FormError :message="memberError" />

    <ul class="divide-y divide-border rounded-lg border border-border" aria-label="Members">
      <li v-for="member in sync.users" :key="member.id" class="flex h-14 items-center gap-3 px-4">
        <UserAvatar :name="member.name" :email="member.email" :avatar-url="member.avatarUrl" :size="28" />
        <span class="min-w-0 flex-1">
          <span class="block truncate font-medium">{{ member.name || member.email }}</span>
          <span class="block truncate text-xs text-faint">{{ member.email }}</span>
        </span>
        <template v-if="workspace.isAdmin">
          <select
            :value="member.role"
            :disabled="busy === member.id"
            :aria-label="`Role of ${member.name || member.email}`"
            class="h-7 rounded-md border border-border-strong bg-elevated px-1.5 text-xs text-fg outline-none focus:border-accent disabled:opacity-50"
            @change="setRole(member, $event)"
          >
            <option value="admin">Admin</option>
            <option value="member">Member</option>
            <option value="guest">Guest</option>
          </select>
          <!-- Leaving is on the General page, where it can't be hit by accident. -->
          <BaseButton
            v-if="member.id !== session.user?.id"
            size="sm"
            variant="ghost"
            :disabled="busy === member.id"
            :aria-label="`Remove ${member.name || member.email}`"
            @click="remove(member)"
          >
            Remove
          </BaseButton>
          <span v-else class="w-[62px] text-center text-xs text-faint">You</span>
        </template>
        <span v-else class="text-xs text-muted capitalize">{{ member.role }}</span>
      </li>
    </ul>
    <p class="text-xs text-faint">Guests see only the teams they are in.</p>
  </SettingsPage>
</template>
