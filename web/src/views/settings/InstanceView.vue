<script setup lang="ts">
import { onMounted, ref } from 'vue'

import { api, errorMessage, unwrap } from '@/api/client'
import type { components } from '@/api/schema'
import SettingsPage from '@/components/SettingsPage.vue'
import BaseButton from '@/components/ui/BaseButton.vue'
import FormError from '@/components/ui/FormError.vue'
import { useToastStore } from '@/stores/toasts'

/** Settings for the whole install, for whoever runs the server. */
type Instance = components['schemas']['InstanceSettings']

const toasts = useToastStore()
const instance = ref<Instance | null>(null)
const domains = ref('')
const error = ref('')
const saving = ref(false)

function show(value: Instance) {
  instance.value = value
  domains.value = value.signupDomains.join(', ')
}

onMounted(async () => {
  try {
    show(await unwrap(api.GET('/admin/instance')))
  } catch (err) {
    error.value = errorMessage(err)
  }
})

async function save(patch: components['schemas']['UpdateInstanceRequest'], message?: string) {
  error.value = ''
  saving.value = true
  try {
    show(await unwrap(api.PATCH('/admin/instance', { body: patch })))
    if (message) toasts.show(message)
  } catch (err) {
    error.value = errorMessage(err)
  } finally {
    saving.value = false
  }
}

const SIGNUP = [
  { value: 'open', label: 'Anyone' },
  { value: 'invited', label: 'Invited people' },
  { value: 'domains', label: 'Invited people and these domains' },
] as const
</script>

<template>
  <SettingsPage title="Instance">
    <FormError :message="error" />

    <template v-if="instance">
      <section aria-labelledby="instance-about" class="rounded-lg border border-border p-4">
        <h2 id="instance-about" class="font-medium">About</h2>
        <dl class="mt-2 grid grid-cols-[max-content_1fr] gap-x-6 gap-y-1 tabular-nums">
          <dt class="text-muted">Version</dt>
          <dd>{{ instance.version }}</dd>
          <dt class="text-muted">People</dt>
          <dd>{{ instance.users.toLocaleString() }}</dd>
          <dt class="text-muted">Workspaces</dt>
          <dd>{{ instance.workspaces.toLocaleString() }}</dd>
          <dt class="text-muted">Issues</dt>
          <dd>{{ instance.issues.toLocaleString() }}</dd>
          <dt class="text-muted">Admins</dt>
          <dd>{{ instance.admins.join(', ') }}</dd>
        </dl>
        <p class="mt-2 text-xs text-faint">Add admins with INSTANCE_ADMINS.</p>
      </section>

      <section aria-labelledby="instance-signup" class="rounded-lg border border-border p-4">
        <h2 id="instance-signup" class="font-medium">Sign-ups</h2>
        <fieldset class="mt-3 flex flex-col gap-2" :disabled="saving">
          <legend class="sr-only">Who can sign up</legend>
          <label v-for="option in SIGNUP" :key="option.value" class="flex items-start gap-2 select-none">
            <input
              type="radio"
              name="signup"
              :value="option.value"
              :checked="instance.signup === option.value"
              class="mt-0.5 size-3.5 accent-(--accent)"
              @change="save({ signup: option.value })"
            />
            <span>{{ option.label }}</span>
          </label>
        </fieldset>
        <form v-if="instance.signup === 'domains'" class="mt-3 flex flex-wrap gap-2" @submit.prevent="save({ signupDomains: domains.split(/[\s,]+/) }, 'Domains saved')">
          <input
            v-model="domains"
            aria-label="Allowed domains"
            placeholder="example.com, example.org"
            spellcheck="false"
            class="h-8 min-w-48 flex-1 rounded-md border border-border-strong bg-elevated px-3 text-fg outline-none placeholder:text-faint focus:border-accent"
          />
          <BaseButton type="submit" :loading="saving">Save domains</BaseButton>
        </form>
      </section>

      <section aria-labelledby="instance-workspaces" class="rounded-lg border border-border p-4">
        <h2 id="instance-workspaces" class="font-medium">Workspace creation</h2>
        <label class="mt-3 flex items-start gap-2 select-none">
          <input
            type="checkbox"
            :checked="instance.workspaceCreation === 'admins'"
            :disabled="saving"
            class="mt-0.5 size-3.5 accent-(--accent)"
            @change="save({ workspaceCreation: ($event.target as HTMLInputElement).checked ? 'admins' : 'anyone' })"
          />
          <span>Instance admins only</span>
        </label>
      </section>
    </template>
  </SettingsPage>
</template>
