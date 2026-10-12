<script setup lang="ts">
import { onMounted, ref } from 'vue'
import { RouterLink, useRoute, useRouter } from 'vue-router'

import { errorMessage } from '@/api/client'
import AuthShell from '@/components/AuthShell.vue'
import { useSessionStore } from '@/stores/session'

const router = useRouter()
const route = useRoute()
const session = useSessionStore()
const error = ref('')

onMounted(async () => {
  // The token arrives in the fragment so it is never sent to the server in a URL.
  const token = new URLSearchParams(window.location.hash.slice(1)).get('token')
  history.replaceState(history.state, '', window.location.pathname)
  if (!token) {
    error.value = 'This sign-in link is incomplete.'
    return
  }
  try {
    await session.verifyMagicLink(token)
    // A link asked for from a customer portal leads back to it, and nowhere else.
    const next = typeof route.query.next === 'string' && /^\/portal\/[a-z0-9-]+$/.test(route.query.next) ? route.query.next : null
    await router.replace(next ?? { name: 'home' })
  } catch (err) {
    error.value = errorMessage(err)
  }
})
</script>

<template>
  <AuthShell v-if="error" title="Couldn't sign you in" :subtitle="error">
    <RouterLink
      :to="{ name: 'login' }"
      class="flex h-10 items-center justify-center rounded-md bg-accent text-sm font-medium text-accent-fg hover:bg-accent-hover"
    >
      Request a new link
    </RouterLink>
  </AuthShell>
  <AuthShell v-else title="Signing you in…" />
</template>
