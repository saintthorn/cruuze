<script setup lang="ts">
import { computed, ref, watch } from 'vue'
import { RouterLink, useRouter } from 'vue-router'

import { errorMessage } from '@/api/client'
import AuthShell from '@/components/AuthShell.vue'
import BaseButton from '@/components/ui/BaseButton.vue'
import BaseInput from '@/components/ui/BaseInput.vue'
import FormError from '@/components/ui/FormError.vue'
import { suggestUrlKey, URL_KEY_PATTERN } from '@/lib/keys'
import { useSessionStore } from '@/stores/session'

const router = useRouter()
const session = useSessionStore()

const name = ref('')
const urlKey = ref('')
// Keep suggesting a URL from the name until the user edits the URL themselves.
const urlKeyEdited = ref(false)
const saving = ref(false)
const error = ref('')

const host = window.location.host

watch(name, (value) => {
  if (!urlKeyEdited.value) urlKey.value = suggestUrlKey(value)
})

const urlKeyValid = computed(() => URL_KEY_PATTERN.test(urlKey.value))

async function submit() {
  error.value = ''
  if (!urlKeyValid.value) {
    error.value = 'Workspace URL must be 3-32 lowercase letters, digits or dashes.'
    return
  }
  saving.value = true
  try {
    const workspace = await session.createWorkspace(name.value, urlKey.value)
    await router.replace({ name: 'workspace', params: { workspaceKey: workspace.urlKey } })
  } catch (err) {
    error.value = errorMessage(err)
  } finally {
    saving.value = false
  }
}
</script>

<template>
  <AuthShell
    v-if="session.me && !session.me.canCreateWorkspace"
    title="No workspace"
    subtitle="Ask a workspace admin for an invite."
  >
    <p v-if="session.workspaces.length > 0" class="text-center text-xs">
      <RouterLink :to="{ name: 'home' }" class="text-muted hover:text-fg">Back to Cruuze</RouterLink>
    </p>
  </AuthShell>
  <AuthShell
    v-else
    title="Create a workspace"
  >
    <form class="flex flex-col gap-4" @submit.prevent="submit">
      <BaseInput
        v-model="name"
        label="Workspace name"
        placeholder="Acme"
        autocomplete="organization"
        required
        autofocus
        :maxlength="80"
      />
      <BaseInput
        v-model="urlKey"
        label="Workspace URL"
        :prefix="`${host}/`"
        required
        :maxlength="32"
        @input="urlKeyEdited = true"
      />
      <FormError :message="error" />
      <BaseButton type="submit" variant="primary" size="lg" :loading="saving">
        Create workspace
      </BaseButton>
    </form>
    <p v-if="session.workspaces.length > 0" class="mt-6 text-center text-xs">
      <RouterLink :to="{ name: 'home' }" class="text-muted hover:text-fg">Back to Cruuze</RouterLink>
    </p>
  </AuthShell>
</template>
