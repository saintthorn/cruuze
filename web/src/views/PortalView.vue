<script setup lang="ts">
import { ChevronUp } from 'lucide-vue-next'
import { computed, onMounted, ref } from 'vue'
import { useRoute } from 'vue-router'

import { api, ApiError, errorMessage, unwrap } from '@/api/client'
import type { components } from '@/api/schema'
import BaseButton from '@/components/ui/BaseButton.vue'
import BaseInput from '@/components/ui/BaseInput.vue'
import FormError from '@/components/ui/FormError.vue'
import { requestStatus } from '@/lib/requests'
import { timeAgo } from '@/lib/time'

/**
 * A workspace's public request page. It stands apart from the app: a
 * customer signs in with an emailed link, and sees only what they sent and
 * what the team has chosen to show.
 */
type Portal = components['schemas']['Portal']
type PortalRequest = components['schemas']['PortalRequest']

const route = useRoute()
const urlKey = String(route.params.urlKey)
const path = { urlKey }

const portal = ref<Portal | null>(null)
const missing = ref(false)
const error = ref('')

async function load() {
  try {
    portal.value = await unwrap(api.GET('/portal/{urlKey}', { params: { path } }))
    document.title = `${portal.value.name} requests`
  } catch (err) {
    if (err instanceof ApiError && err.status === 404) missing.value = true
    else error.value = errorMessage(err)
  }
}
onMounted(load)

const mine = computed(() => portal.value?.requests.filter((r) => r.mine) ?? [])
const board = computed(() => portal.value?.requests.filter((r) => r.public) ?? [])

// --- signing in --------------------------------------------------------------
const email = ref('')
const sentTo = ref('')
const sending = ref(false)

async function signIn() {
  error.value = ''
  sending.value = true
  try {
    const address = email.value.trim()
    await unwrap(api.POST('/auth/magic-link', { body: { email: address, portal: urlKey } }))
    sentTo.value = address
  } catch (err) {
    error.value = errorMessage(err)
  } finally {
    sending.value = false
  }
}

async function signOut() {
  await unwrap(api.POST('/auth/logout')).catch(() => undefined)
  // Other tabs of the app hold the old session in memory; start clean.
  window.location.reload()
}

// --- asking ------------------------------------------------------------------
const title = ref('')
const body = ref('')
const submitting = ref(false)
// Kept across retries, so sending twice makes one request.
let draftId = crypto.randomUUID()

async function submit() {
  if (!title.value.trim() || !portal.value) return
  error.value = ''
  submitting.value = true
  try {
    const created = await unwrap(
      api.POST('/portal/{urlKey}/requests', { params: { path }, body: { id: draftId, title: title.value, body: body.value } }),
    )
    portal.value.requests = [created, ...portal.value.requests.filter((r) => r.id !== created.id)]
    title.value = ''
    body.value = ''
    draftId = crypto.randomUUID()
  } catch (err) {
    error.value = errorMessage(err)
  } finally {
    submitting.value = false
  }
}

async function toggleVote(request: PortalRequest) {
  if (!portal.value?.email) {
    error.value = 'Sign in with your email to vote.'
    return
  }
  error.value = ''
  try {
    const params = { path: { urlKey, requestId: request.id } }
    const updated = request.voted
      ? await unwrap(api.DELETE('/portal/{urlKey}/requests/{requestId}/vote', { params }))
      : await unwrap(api.PUT('/portal/{urlKey}/requests/{requestId}/vote', { params }))
    Object.assign(request, { voteCount: updated.voteCount, voted: updated.voted })
  } catch (err) {
    error.value = errorMessage(err)
  }
}
</script>

<template>
  <main class="mx-auto flex min-h-full w-full max-w-2xl flex-col gap-8 px-6 py-12">
    <div v-if="missing" class="py-24 text-center">
      <h1 class="text-xl font-semibold tracking-tight">Page not found</h1>
    </div>

    <template v-else-if="portal">
      <header class="flex flex-col gap-2">
        <p class="text-[11px] text-faint" aria-hidden="true">~/{{ portal.urlKey }}/requests</p>
        <h1 class="text-2xl font-semibold tracking-tight">{{ portal.name }}: requests</h1>
        <p v-if="portal.intro" class="whitespace-pre-wrap text-muted">{{ portal.intro }}</p>
        <p v-if="portal.email" class="text-xs text-faint">
          Signed in as {{ portal.email }} ·
          <button type="button" class="underline hover:text-fg" @click="signOut">Sign out</button>
        </p>
      </header>

      <FormError :message="error" />

      <section v-if="!portal.email" aria-labelledby="portal-signin" class="border border-border-strong bg-elevated p-5">
        <template v-if="sentTo">
          <h2 id="portal-signin" class="font-medium">Check your email</h2>
          <p class="mt-1 text-muted">Sign-in link sent to {{ sentTo }}. It expires in 15 minutes.</p>
          <BaseButton variant="ghost" class="mt-3" @click="sentTo = ''">Use a different email</BaseButton>
        </template>
        <form v-else class="flex flex-col gap-3" @submit.prevent="signIn">
          <h2 id="portal-signin" class="font-medium">Sign in to send a request</h2>
          <BaseInput v-model="email" label="Email" type="email" placeholder="you@company.com" autocomplete="email" required />
          <BaseButton type="submit" variant="primary" :loading="sending">Send link</BaseButton>
        </form>
      </section>

      <section v-else aria-labelledby="portal-ask" class="border border-border-strong bg-elevated p-5">
        <form class="flex flex-col gap-3" @submit.prevent="submit">
          <h2 id="portal-ask" class="font-medium">New request</h2>
          <BaseInput v-model="title" label="Title" placeholder="Export invoices as CSV" required />
          <label class="flex flex-col gap-1.5 text-xs font-medium text-muted">
            Details
            <textarea
              v-model="body"
              rows="4"
              maxlength="10000"
              placeholder="Optional"
              class="resize-y rounded-md border border-border-strong bg-bg px-3 py-2 text-[13px] font-normal text-fg outline-none placeholder:text-faint focus:border-accent"
            />
          </label>
          <BaseButton type="submit" variant="primary" class="self-start" :loading="submitting" :disabled="!title.trim()">Send request</BaseButton>
        </form>
      </section>

      <section v-if="mine.length > 0" aria-labelledby="portal-mine" class="flex flex-col gap-2">
        <h2 id="portal-mine" class="text-xs font-medium text-faint">// your requests</h2>
        <ul class="divide-y divide-border border border-border">
          <li v-for="request in mine" :key="request.id" class="flex flex-col gap-1 px-4 py-3">
            <p class="flex items-baseline gap-2">
              <span class="min-w-0 flex-1 font-medium">{{ request.title }}</span>
              <span class="shrink-0 text-xs" :class="requestStatus(request.status).class">{{ requestStatus(request.status).label }}</span>
            </p>
            <p v-if="request.body" class="text-[13px] whitespace-pre-wrap text-muted">{{ request.body }}</p>
            <p v-if="request.response" class="mt-1 border-l-2 border-accent pl-3 text-[13px] whitespace-pre-wrap">{{ request.response }}</p>
            <p class="text-xs text-faint">Sent {{ timeAgo(request.createdAt) }}</p>
          </li>
        </ul>
      </section>

      <section v-if="portal.publicBoard" aria-labelledby="portal-board" class="flex flex-col gap-2">
        <h2 id="portal-board" class="text-xs font-medium text-faint">// public board</h2>
        <ul v-if="board.length > 0" class="divide-y divide-border border border-border">
          <li v-for="request in board" :key="request.id" class="flex gap-3 px-4 py-3">
            <button
              type="button"
              class="flex h-12 w-11 shrink-0 flex-col items-center justify-center border text-xs tabular-nums"
              :class="request.voted ? 'border-accent text-accent' : 'border-border-strong text-muted hover:text-fg'"
              :aria-pressed="request.voted"
              :aria-label="`${request.voted ? 'Remove your vote for' : 'Vote for'} ${request.title}: ${request.voteCount} votes`"
              @click="toggleVote(request)"
            >
              <ChevronUp class="size-3.5" aria-hidden="true" />
              {{ request.voteCount }}
            </button>
            <div class="flex min-w-0 flex-1 flex-col gap-1">
              <p class="flex items-baseline gap-2">
                <span class="min-w-0 flex-1 font-medium">{{ request.title }}</span>
                <span class="shrink-0 text-xs" :class="requestStatus(request.status).class">{{ requestStatus(request.status).label }}</span>
              </p>
              <p v-if="request.body" class="line-clamp-3 text-[13px] whitespace-pre-wrap text-muted">{{ request.body }}</p>
              <p v-if="request.response" class="border-l-2 border-accent pl-3 text-[13px] whitespace-pre-wrap">{{ request.response }}</p>
            </div>
          </li>
        </ul>
        <p v-else class="text-muted">Nothing here yet.</p>
      </section>
    </template>

    <p v-else-if="error" role="alert" class="py-24 text-center text-danger">{{ error }}</p>
    <p v-else class="py-24 text-center text-muted" role="status">Loading…</p>
  </main>
</template>
