<script setup lang="ts">
import { ref } from 'vue'

import { errorMessage } from '@/api/client'
import SettingsPage from '@/components/SettingsPage.vue'
import BaseButton from '@/components/ui/BaseButton.vue'
import FormError from '@/components/ui/FormError.vue'
import { useSessionStore } from '@/stores/session'
import { useToastStore } from '@/stores/toasts'

const session = useSessionStore()
const toasts = useToastStore()

const name = ref(session.user?.name ?? '')
const saving = ref(false)
const error = ref('')

async function saveName() {
  error.value = ''
  saving.value = true
  try {
    await session.updateName(name.value.trim())
    name.value = session.user?.name ?? ''
    toasts.show('Name saved')
  } catch (err) {
    error.value = errorMessage(err)
  } finally {
    saving.value = false
  }
}

const github = ref(session.me?.githubLogin ?? '')
const savingGithub = ref(false)

async function saveGithub() {
  error.value = ''
  savingGithub.value = true
  try {
    await session.setGithubLogin(github.value.trim().replace(/^@/, ''))
    github.value = session.me?.githubLogin ?? ''
    toasts.show(github.value ? 'GitHub username saved' : 'GitHub username removed')
  } catch (err) {
    error.value = errorMessage(err)
  } finally {
    savingGithub.value = false
  }
}

async function setEmails(event: Event) {
  const box = event.target as HTMLInputElement
  error.value = ''
  try {
    await session.setEmailNotifications(box.checked)
  } catch (err) {
    box.checked = !box.checked
    error.value = errorMessage(err)
  }
}
</script>

<template>
  <SettingsPage title="Account">
    <FormError :message="error" />

    <section aria-labelledby="profile-heading" class="rounded-lg border border-border p-4">
      <h2 id="profile-heading" class="font-medium">Profile</h2>
      <p class="mt-0.5 text-muted">Signed in as {{ session.user?.email }}.</p>
      <form class="mt-3 flex flex-wrap gap-2" @submit.prevent="saveName">
        <input
          v-model="name"
          aria-label="Your name"
          required
          maxlength="80"
          autocomplete="name"
          class="h-8 min-w-48 flex-1 rounded-md border border-border-strong bg-elevated px-3 text-fg outline-none placeholder:text-faint focus:border-accent"
        />
        <BaseButton type="submit" variant="primary" :loading="saving" :disabled="!name.trim() || name.trim() === session.user?.name">
          Save
        </BaseButton>
      </form>
    </section>

    <section aria-labelledby="github-user-heading" class="rounded-lg border border-border p-4">
      <h2 id="github-user-heading" class="font-medium">GitHub username</h2>
      <p class="mt-0.5 text-muted">Credits your GitHub activity to you.</p>
      <form class="mt-3 flex flex-wrap gap-2" @submit.prevent="saveGithub">
        <input
          v-model="github"
          aria-label="GitHub username"
          placeholder="octocat"
          maxlength="39"
          autocomplete="off"
          spellcheck="false"
          class="h-8 min-w-48 flex-1 rounded-md border border-border-strong bg-elevated px-3 text-fg outline-none placeholder:text-faint focus:border-accent"
        />
        <BaseButton type="submit" :loading="savingGithub" :disabled="github.trim() === (session.me?.githubLogin ?? '')">Save</BaseButton>
      </form>
    </section>

    <section aria-labelledby="emails-heading" class="rounded-lg border border-border p-4">
      <h2 id="emails-heading" class="font-medium">Email notifications</h2>
      <label class="mt-3 flex items-center gap-2 select-none">
        <input
          type="checkbox"
          :checked="session.me?.emailNotifications"
          class="size-3.5 accent-(--accent)"
          @change="setEmails"
        />
        Email unread notifications
      </label>
    </section>
  </SettingsPage>
</template>
