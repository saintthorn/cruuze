<script setup lang="ts">
import { Copy } from 'lucide-vue-next'
import { onMounted, ref } from 'vue'

import { api, errorMessage, unwrap } from '@/api/client'
import type { components } from '@/api/schema'
import SettingsPage from '@/components/SettingsPage.vue'
import BaseButton from '@/components/ui/BaseButton.vue'
import FormError from '@/components/ui/FormError.vue'
import { useCopy } from '@/composables/useCopy'
import { useWorkspaceStore } from '@/stores/workspace'

type GithubIntegration = components['schemas']['GithubIntegration']

const workspace = useWorkspaceStore()
const copy = useCopy()

const github = ref<GithubIntegration | null>(null)
const busy = ref(false)
const error = ref('')

function params() {
  return { path: { workspaceId: workspace.current!.id } }
}

async function run(action: () => Promise<void>) {
  error.value = ''
  busy.value = true
  try {
    await action()
  } catch (err) {
    error.value = errorMessage(err)
  } finally {
    busy.value = false
  }
}

onMounted(() => {
  if (!workspace.isAdmin) return
  void run(async () => {
    github.value = await unwrap(api.GET('/workspaces/{workspaceId}/integrations/github', { params: params() }))
  })
})

const connect = () =>
  run(async () => {
    github.value = await unwrap(api.PUT('/workspaces/{workspaceId}/integrations/github', { params: params() }))
  })

function rotate() {
  if (window.confirm('Replace the secret? Update the webhook in GitHub afterwards.')) void connect()
}

function disconnect() {
  if (!window.confirm('Disconnect GitHub?')) return
  void run(async () => {
    await unwrap(api.DELETE('/workspaces/{workspaceId}/integrations/github', { params: params() }))
    if (github.value) github.value = { enabled: false, webhookUrl: github.value.webhookUrl }
  })
}

const valueClass = 'min-w-0 flex-1 truncate rounded-md border border-border bg-bg px-2 py-1 font-mono text-xs'
const copyClass = 'flex size-7 shrink-0 items-center justify-center rounded text-faint hover:bg-hover hover:text-fg'
</script>

<template>
  <SettingsPage title="Integrations">
    <section aria-labelledby="github-heading" class="rounded-lg border border-border p-4">
      <div class="flex items-center gap-2">
        <h2 id="github-heading" class="font-medium">GitHub</h2>
        <span v-if="github?.enabled" class="rounded-full bg-success/15 px-2 text-xs text-success">Connected</span>
      </div>
      <p class="mt-0.5 text-muted">
        Branches, pull requests and commits that name an issue (<code class="rounded bg-hover px-1 font-mono text-xs">ENG-12</code>)
        update its status.
      </p>
      <FormError :message="error" class="mt-2" />

      <p v-if="!workspace.isAdmin" class="mt-3 text-muted">Admins only.</p>

      <template v-else-if="github?.enabled">
        <ol class="mt-3 flex list-decimal flex-col gap-1 pl-5 text-muted">
          <li>GitHub: <span class="text-fg">Settings → Webhooks → Add webhook</span>.</li>
          <li>Paste the URL and secret below. Content type: <span class="text-fg">application/json</span>.</li>
          <li>
            Events: <span class="text-fg">Branch or tag creation</span>, <span class="text-fg">Pull requests</span>,
            <span class="text-fg">Pushes</span>.
          </li>
        </ol>
        <dl class="mt-3 flex flex-col gap-2">
          <div class="flex items-center gap-2">
            <dt class="w-24 shrink-0 text-xs text-faint">Payload URL</dt>
            <dd :class="valueClass">{{ github.webhookUrl }}</dd>
            <button type="button" :class="copyClass" aria-label="Copy payload URL" @click="copy(github.webhookUrl, 'Payload URL')">
              <Copy class="size-3.5" aria-hidden="true" />
            </button>
          </div>
          <div class="flex items-center gap-2">
            <dt class="w-24 shrink-0 text-xs text-faint">Secret</dt>
            <dd :class="valueClass">{{ github.secret }}</dd>
            <button type="button" :class="copyClass" aria-label="Copy secret" @click="copy(github.secret ?? '', 'Secret')">
              <Copy class="size-3.5" aria-hidden="true" />
            </button>
          </div>
        </dl>
        <div class="mt-4 flex gap-2">
          <BaseButton size="sm" :disabled="busy" @click="rotate">Replace secret</BaseButton>
          <BaseButton size="sm" variant="danger" :disabled="busy" @click="disconnect">Disconnect</BaseButton>
        </div>
      </template>

      <BaseButton v-else-if="github" variant="primary" class="mt-3" :loading="busy" @click="connect">Connect GitHub</BaseButton>
    </section>
  </SettingsPage>
</template>
