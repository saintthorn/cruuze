<script setup lang="ts">
import { ChevronRight, Copy, Trash2 } from 'lucide-vue-next'
import { onMounted, reactive, ref } from 'vue'

import { api, errorMessage, unwrap } from '@/api/client'
import type { components } from '@/api/schema'
import BaseButton from '@/components/ui/BaseButton.vue'
import FormError from '@/components/ui/FormError.vue'
import { useCopy } from '@/composables/useCopy'
import { fullDateTime, timeAgo } from '@/lib/time'
import { useSyncStore } from '@/stores/sync'
import { useWorkspaceStore } from '@/stores/workspace'

type Webhook = components['schemas']['Webhook']
type WebhookDelivery = components['schemas']['WebhookDelivery']
type Model = components['schemas']['SyncModel']

/** The records a webhook can be told about, as the server defines them. */
const MODELS: { value: Model; label: string }[] = [
  { value: 'Issue', label: 'Issues' },
  { value: 'Comment', label: 'Comments' },
  { value: 'Project', label: 'Projects' },
  { value: 'Milestone', label: 'Milestones' },
  { value: 'Cycle', label: 'Cycles' },
  { value: 'Label', label: 'Labels' },
  { value: 'Attachment', label: 'Attachments' },
  { value: 'PullRequest', label: 'Pull requests' },
]

const workspace = useWorkspaceStore()
const sync = useSyncStore()
const copy = useCopy()

const hooks = ref<Webhook[]>([])
const error = ref('')
const creating = ref(false)
const form = reactive({ url: '', teamId: '', models: MODELS.map((m) => m.value) })

// Deliveries are fetched when a webhook is expanded.
const expanded = ref<string | null>(null)
const deliveries = ref<WebhookDelivery[]>([])

function path() {
  return { workspaceId: workspace.current!.id }
}

async function run<T>(action: () => Promise<T>): Promise<T | undefined> {
  error.value = ''
  try {
    return await action()
  } catch (err) {
    error.value = errorMessage(err)
    return undefined
  }
}

onMounted(() =>
  run(async () => {
    hooks.value = await unwrap(api.GET('/workspaces/{workspaceId}/webhooks', { params: { path: path() } }))
  }),
)

async function create() {
  creating.value = true
  const created = await run(() =>
    unwrap(
      api.POST('/workspaces/{workspaceId}/webhooks', {
        params: { path: path() },
        body: { url: form.url.trim(), teamId: form.teamId || undefined, models: form.models },
      }),
    ),
  )
  creating.value = false
  if (!created) return
  hooks.value.push(created)
  form.url = ''
  await toggle(created)
}

async function setEnabled(hook: Webhook, enabled: boolean) {
  const updated = await run(() =>
    unwrap(api.PATCH('/webhooks/{webhookId}', { params: { path: { webhookId: hook.id } }, body: { enabled } })),
  )
  if (updated) hooks.value = hooks.value.map((h) => (h.id === hook.id ? updated : h))
}

async function remove(hook: Webhook) {
  if (!window.confirm(`Delete the webhook to ${hook.label}?`)) return
  const done = await run(async () => {
    await unwrap(api.DELETE('/webhooks/{webhookId}', { params: { path: { webhookId: hook.id } } }))
    return true
  })
  if (done) hooks.value = hooks.value.filter((h) => h.id !== hook.id)
}

async function loadDeliveries(hook: Webhook) {
  const found = await run(() =>
    unwrap(api.GET('/webhooks/{webhookId}/deliveries', { params: { path: { webhookId: hook.id } } })),
  )
  if (found && expanded.value === hook.id) deliveries.value = found
}

async function toggle(hook: Webhook) {
  if (expanded.value === hook.id) {
    expanded.value = null
    return
  }
  expanded.value = hook.id
  deliveries.value = []
  await loadDeliveries(hook)
}

function scope(hook: Webhook): string {
  if (!hook.teamId) return 'All public teams'
  return sync.team(hook.teamId)?.name ?? 'A team you can’t see'
}

function outcome(delivery: WebhookDelivery): string {
  if (delivery.status === 'delivered') return `Delivered (${delivery.responseStatus})`
  const tries = delivery.attempts === 1 ? '1 attempt' : `${delivery.attempts} attempts`
  if (delivery.status === 'failed') return `Failed after ${tries}`
  return delivery.attempts === 0 ? 'Waiting to send' : `Retrying after ${tries}`
}

const inputClass =
  'h-8 rounded-md border border-border-strong bg-elevated px-3 text-fg outline-none placeholder:text-faint focus:border-accent'
</script>

<template>
  <section aria-labelledby="webhooks-heading" class="rounded-lg border border-border p-4">
    <h2 id="webhooks-heading" class="font-medium">Webhooks</h2>
    <p class="mt-0.5 text-muted">
      POST on every change, signed in <code class="rounded bg-hover px-1 font-mono text-xs">Cruuze-Signature</code> (HMAC-SHA256).
    </p>

    <form class="mt-3 flex flex-col gap-2" @submit.prevent="create">
      <div class="flex flex-wrap gap-2">
        <input
          v-model="form.url"
          type="url"
          required
          aria-label="Webhook URL"
          placeholder="https://example.com/hooks/cruuze"
          class="min-w-48 flex-1"
          :class="inputClass"
        />
        <select v-model="form.teamId" aria-label="Teams to send" :class="inputClass" class="!px-2">
          <option value="">All public teams</option>
          <option v-for="team in workspace.teams" :key="team.id" :value="team.id">Only {{ team.name }}</option>
        </select>
        <BaseButton type="submit" variant="primary" :loading="creating" :disabled="form.models.length === 0">Add webhook</BaseButton>
      </div>
      <fieldset class="flex flex-wrap gap-x-4 gap-y-1">
        <legend class="sr-only">Records to send</legend>
        <label v-for="model in MODELS" :key="model.value" class="flex items-center gap-1.5 text-muted select-none">
          <input v-model="form.models" type="checkbox" :value="model.value" class="size-3.5 accent-(--accent)" />
          {{ model.label }}
        </label>
      </fieldset>
    </form>
    <FormError :message="error" class="mt-2" />

    <ul v-if="hooks.length > 0" aria-label="Webhooks" class="mt-4 divide-y divide-border border-t border-border">
      <li v-for="hook in hooks" :key="hook.id" class="py-2">
        <div class="flex items-center gap-2">
          <button
            type="button"
            class="flex min-w-0 flex-1 items-center gap-1.5 rounded py-1 text-left hover:text-fg"
            :aria-expanded="expanded === hook.id"
            @click="toggle(hook)"
          >
            <ChevronRight class="size-3.5 shrink-0 text-faint transition-transform" :class="{ 'rotate-90': expanded === hook.id }" aria-hidden="true" />
            <span class="min-w-0">
              <span class="block truncate font-medium" :class="{ 'text-muted': !hook.enabled }">{{ hook.url }}</span>
              <span class="block truncate text-xs text-faint">{{ scope(hook) }} · {{ hook.models.length === MODELS.length ? 'Everything' : hook.models.join(', ') }}</span>
            </span>
          </button>
          <label class="flex shrink-0 items-center gap-1.5 text-xs text-muted select-none">
            <input
              type="checkbox"
              :checked="hook.enabled"
              class="size-3.5 accent-(--accent)"
              @change="setEnabled(hook, ($event.target as HTMLInputElement).checked)"
            />
            Enabled
          </label>
          <button
            type="button"
            class="flex size-7 shrink-0 items-center justify-center rounded text-faint hover:bg-hover hover:text-danger"
            :aria-label="`Delete webhook to ${hook.label}`"
            @click="remove(hook)"
          >
            <Trash2 class="size-3.5" aria-hidden="true" />
          </button>
        </div>

        <div v-if="expanded === hook.id" class="mt-2 flex flex-col gap-3 pl-5">
          <div class="flex items-center gap-2">
            <span class="shrink-0 text-xs text-faint">Signing secret</span>
            <code class="min-w-0 flex-1 truncate rounded border border-border bg-bg px-2 py-1 font-mono text-xs">{{ hook.secret }}</code>
            <button
              type="button"
              class="flex size-7 shrink-0 items-center justify-center rounded text-faint hover:bg-hover hover:text-fg"
              aria-label="Copy signing secret"
              @click="copy(hook.secret, 'Signing secret')"
            >
              <Copy class="size-3.5" aria-hidden="true" />
            </button>
          </div>
          <div>
            <div class="flex items-center gap-2">
              <h3 class="text-xs font-medium text-faint">Recent deliveries</h3>
              <button type="button" class="rounded px-1 text-xs text-muted hover:text-fg" @click="loadDeliveries(hook)">Refresh</button>
            </div>
            <ul v-if="deliveries.length > 0" class="mt-1 flex flex-col">
              <li v-for="delivery in deliveries" :key="delivery.id" class="flex h-7 items-center gap-2 text-xs">
                <span
                  class="size-1.5 shrink-0 rounded-full"
                  :class="{ delivered: 'bg-success', failed: 'bg-danger', pending: 'bg-faint' }[delivery.status]"
                  aria-hidden="true"
                />
                <span class="w-36 shrink-0 truncate">{{ delivery.model }} {{ delivery.action }}</span>
                <span class="min-w-0 flex-1 truncate text-muted" :title="delivery.error || undefined">
                  {{ outcome(delivery) }}<template v-if="delivery.error"> — {{ delivery.error }}</template>
                </span>
                <time class="shrink-0 text-faint" :datetime="delivery.createdAt" :title="fullDateTime(delivery.createdAt)">
                  {{ timeAgo(delivery.createdAt) }}
                </time>
              </li>
            </ul>
            <p v-else class="mt-1 text-xs text-muted">Nothing sent yet.</p>
          </div>
        </div>
      </li>
    </ul>
  </section>
</template>
