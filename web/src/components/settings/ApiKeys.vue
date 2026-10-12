<script setup lang="ts">
import { Copy, Trash2 } from 'lucide-vue-next'
import { onMounted, ref } from 'vue'

import { api, errorMessage, unwrap } from '@/api/client'
import type { components } from '@/api/schema'
import BaseButton from '@/components/ui/BaseButton.vue'
import FormError from '@/components/ui/FormError.vue'
import { useCopy } from '@/composables/useCopy'
import { timeAgo } from '@/lib/time'
import { useWorkspaceStore } from '@/stores/workspace'

type ApiKey = components['schemas']['ApiKey']

const workspace = useWorkspaceStore()
const copy = useCopy()

const keys = ref<ApiKey[]>([])
const name = ref('')
const creating = ref(false)
const error = ref('')
// The one time a key's token is known: right after creating it.
const fresh = ref<{ name: string; token: string } | null>(null)

const origin = window.location.origin

/** The command that connects Claude Code to this workspace with a key. */
const connectCommand = (token: string) =>
  `claude mcp add --transport http cruuze ${origin}/api/mcp --header "Authorization: Bearer ${token}"`

function path() {
  return { workspaceId: workspace.current!.id }
}

onMounted(async () => {
  try {
    keys.value = await unwrap(api.GET('/workspaces/{workspaceId}/api-keys', { params: { path: path() } }))
  } catch (err) {
    error.value = errorMessage(err)
  }
})

async function create() {
  error.value = ''
  creating.value = true
  try {
    const created = await unwrap(
      api.POST('/workspaces/{workspaceId}/api-keys', { params: { path: path() }, body: { name: name.value.trim() } }),
    )
    keys.value.push(created.apiKey)
    fresh.value = { name: created.apiKey.name, token: created.token }
    name.value = ''
  } catch (err) {
    error.value = errorMessage(err)
  } finally {
    creating.value = false
  }
}

async function remove(key: ApiKey) {
  if (!window.confirm(`Delete the key "${key.name}"? Anything using it stops working.`)) return
  error.value = ''
  try {
    await unwrap(api.DELETE('/api-keys/{apiKeyId}', { params: { path: { apiKeyId: key.id } } }))
    keys.value = keys.value.filter((k) => k.id !== key.id)
  } catch (err) {
    error.value = errorMessage(err)
  }
}
</script>

<template>
  <section aria-labelledby="api-keys-heading" class="rounded-lg border border-border p-4">
    <h2 id="api-keys-heading" class="font-medium">Personal API keys</h2>
    <p class="mt-0.5 text-muted">A key acts as you, in this workspace.</p>
    <pre class="mt-2 overflow-x-auto rounded-md border border-border bg-bg p-2 font-mono text-xs text-muted">curl -H "Authorization: Bearer cruuze_…" {{ origin }}/api/me</pre>

    <form class="mt-3 flex flex-wrap gap-2" @submit.prevent="create">
      <input
        v-model="name"
        aria-label="Key name"
        placeholder="Name, e.g. CI"
        required
        maxlength="80"
        class="h-8 min-w-48 flex-1 rounded-md border border-border-strong bg-elevated px-3 text-fg outline-none placeholder:text-faint focus:border-accent"
      />
      <BaseButton type="submit" variant="primary" :loading="creating" :disabled="!name.trim()">Create key</BaseButton>
    </form>
    <FormError :message="error" class="mt-2" />

    <div v-if="fresh" role="status" class="mt-3 rounded-md border border-accent/50 bg-accent/10 p-3">
      <p class="font-medium">Copy “{{ fresh.name }}” now. It isn't shown again.</p>
      <div class="mt-2 flex items-center gap-2">
        <code class="min-w-0 flex-1 truncate rounded border border-border bg-bg px-2 py-1 font-mono text-xs">{{ fresh.token }}</code>
        <BaseButton size="sm" @click="copy(fresh.token, 'API key')"><Copy class="size-3.5" aria-hidden="true" /> Copy</BaseButton>
        <BaseButton size="sm" variant="ghost" @click="fresh = null">Done</BaseButton>
      </div>
      <p class="mt-3 text-xs text-muted">For Claude Code:</p>
      <div class="mt-1 flex items-center gap-2">
        <code class="min-w-0 flex-1 truncate rounded border border-border bg-bg px-2 py-1 font-mono text-xs">{{ connectCommand('cruuze_…') }}</code>
        <BaseButton size="sm" @click="copy(connectCommand(fresh.token), 'Command')"><Copy class="size-3.5" aria-hidden="true" /> Copy command</BaseButton>
      </div>
    </div>

    <h3 class="mt-4 font-medium">MCP</h3>
    <p class="mt-0.5 text-muted">
      <code class="font-mono text-xs text-fg">{{ origin }}/api/mcp</code>, with a key as the bearer token.
    </p>

    <ul v-if="keys.length > 0" aria-label="API keys" class="mt-4 divide-y divide-border border-t border-border">
      <li v-for="key in keys" :key="key.id" class="flex h-11 items-center gap-3">
        <span class="min-w-0 flex-1 truncate font-medium">{{ key.name }}</span>
        <code class="shrink-0 font-mono text-xs text-faint">…{{ key.hint }}</code>
        <span class="hidden w-36 shrink-0 text-right text-xs text-faint sm:block">
          {{ key.lastUsedAt ? `Used ${timeAgo(key.lastUsedAt)}` : 'Never used' }}
        </span>
        <button
          type="button"
          class="flex size-7 shrink-0 items-center justify-center rounded text-faint hover:bg-hover hover:text-danger"
          :aria-label="`Delete key ${key.name}`"
          @click="remove(key)"
        >
          <Trash2 class="size-3.5" aria-hidden="true" />
        </button>
      </li>
    </ul>
  </section>
</template>
