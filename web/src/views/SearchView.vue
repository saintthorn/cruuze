<script setup lang="ts">
import { MessageSquare, Search } from 'lucide-vue-next'
import { computed, onBeforeUnmount, onMounted, ref, useTemplateRef, watch } from 'vue'
import { RouterLink, useRoute, useRouter } from 'vue-router'

import { api, errorMessage, unwrap } from '@/api/client'
import type { components } from '@/api/schema'
import StateIcon from '@/components/StateIcon.vue'
import { snippetParts } from '@/lib/search'
import { useSyncStore } from '@/stores/sync'
import { useWorkspaceStore } from '@/stores/workspace'

/** How long typing has to pause before the server is asked. */
const DEBOUNCE_MS = 200

const route = useRoute()
const router = useRouter()
const sync = useSyncStore()
const workspace = useWorkspaceStore()

const input = useTemplateRef('input')
const query = ref(typeof route.query.q === 'string' ? route.query.q : '')
const matches = ref<components['schemas']['SearchResult'][]>([])
const searched = ref('')
const loading = ref(false)
const error = ref('')

// The server finds and ranks the matches, archived issues included, and
// sends enough to list each one whether or not it is in the local copy.
const results = computed(() =>
  matches.value.map((match) => ({
    ...match,
    state: sync.state(match.stateId),
    parts: snippetParts(match.snippet),
    to: { name: 'issue', params: { issueRef: match.identifier } },
  })),
)

let timer: ReturnType<typeof setTimeout> | undefined
let latest = 0

async function search(text: string) {
  const request = ++latest
  const q = text.trim()
  void router.replace({ query: q ? { q } : {} })
  if (!q || !workspace.current) {
    matches.value = []
    searched.value = ''
    loading.value = false
    error.value = ''
    return
  }
  loading.value = true
  try {
    const found = await unwrap(
      api.GET('/workspaces/{workspaceId}/search', { params: { path: { workspaceId: workspace.current.id }, query: { q } } }),
    )
    // A slower, older answer must not replace a newer one.
    if (request !== latest) return
    matches.value = found.results
    searched.value = q
    error.value = ''
  } catch (err) {
    if (request !== latest) return
    error.value = errorMessage(err)
  } finally {
    if (request === latest) loading.value = false
  }
}

watch(query, (text) => {
  clearTimeout(timer)
  timer = setTimeout(() => void search(text), DEBOUNCE_MS)
})

onMounted(() => {
  input.value?.focus()
  if (query.value) void search(query.value)
})
onBeforeUnmount(() => clearTimeout(timer))
</script>

<template>
  <header class="flex h-11 shrink-0 items-center border-b border-border px-5">
    <h1 class="font-medium">Search</h1>
  </header>
  <div class="flex-1 overflow-y-auto">
    <div class="mx-auto flex max-w-3xl flex-col gap-4 px-6 py-8">
      <div class="flex h-10 items-center gap-2 rounded-md border border-border-strong bg-elevated px-3 focus-within:border-accent">
        <Search class="size-4 shrink-0 text-faint" aria-hidden="true" />
        <input
          ref="input"
          v-model="query"
          type="search"
          aria-label="Search issues"
          placeholder="Search titles, descriptions and comments…"
          class="h-full min-w-0 flex-1 bg-transparent text-sm text-fg outline-none placeholder:text-faint"
        />
        <span v-if="loading" class="shrink-0 text-xs text-faint" role="status">Searching…</span>
      </div>

      <p v-if="error" role="alert" class="text-danger">{{ error }}</p>

      <ul v-else-if="results.length > 0" aria-label="Search results" class="divide-y divide-border rounded-lg border border-border">
        <li v-for="result in results" :key="result.issueId">
          <RouterLink :to="result.to" class="flex flex-col gap-1 px-4 py-2.5 hover:bg-hover">
            <span class="flex items-center gap-2">
              <StateIcon v-if="result.state" :type="result.state.type" :color="result.state.color" />
              <span class="shrink-0 text-muted tabular-nums">{{ result.identifier }}</span>
              <span class="truncate font-medium" :class="{ 'text-muted': result.archived }">{{ result.title }}</span>
              <span v-if="result.archived" class="shrink-0 border border-border-strong px-1 text-[10px] text-faint">archived</span>
            </span>
            <span v-if="result.parts.some((p) => p.mark)" class="flex items-start gap-1.5 pl-5.5 text-xs text-muted">
              <MessageSquare v-if="result.inComment" class="mt-0.5 size-3 shrink-0 text-faint" aria-label="In a comment" />
              <span class="line-clamp-2 break-words">
                <template v-for="(part, index) in result.parts" :key="index">
                  <mark v-if="part.mark" class="rounded-sm bg-accent/25 px-0.5 text-fg">{{ part.text }}</mark>
                  <template v-else>{{ part.text }}</template>
                </template>
              </span>
            </span>
          </RouterLink>
        </li>
      </ul>

      <p v-else-if="searched && !loading" class="py-8 text-center text-muted">
        No matches for “{{ searched }}”
      </p>
    </div>
  </div>
</template>
