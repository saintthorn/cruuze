<script setup lang="ts">
import { onMounted, ref } from 'vue'
import { useRoute } from 'vue-router'

import { api, unwrap } from '@/api/client'
import AuthShell from '@/components/AuthShell.vue'
import BaseButton from '@/components/ui/BaseButton.vue'
import BaseInput from '@/components/ui/BaseInput.vue'
import FormError from '@/components/ui/FormError.vue'
import { useMagicLink } from '@/composables/useMagicLink'

const route = useRoute()

const QUERY_ERRORS: Record<string, string> = {
  google: "Google sign-in didn't complete. Try again.",
  signup_closed: 'This server only takes people who were invited. Ask someone in your workspace for an invite.',
}
const { email, sentTo, sending, error, submit } = useMagicLink(QUERY_ERRORS[String(route.query.error)] ?? '')
const googleEnabled = ref(false)
// Whether anyone can make an account here, or only people who were invited.
const open = ref(true)

onMounted(async () => {
  try {
    const config = await unwrap(api.GET('/auth/config'))
    googleEnabled.value = config.google
    open.value = config.signup === 'open'
  } catch {
    // Email sign-in still works without knowing the provider list.
  }
})
</script>

<template>
  <AuthShell
    v-if="sentTo"
    title="Check your email"
    :subtitle="`We sent a sign-in link to ${sentTo}. It expires in 15 minutes.`"
  >
    <BaseButton variant="ghost" size="lg" class="w-full" @click="sentTo = ''">
      Use a different email
    </BaseButton>
  </AuthShell>

  <AuthShell v-else title="Log in to Cruuze">
    <form class="flex flex-col gap-4" @submit.prevent="submit">
      <BaseInput
        v-model="email"
        label="Email"
        type="email"
        placeholder="you@company.com"
        autocomplete="email"
        required
        autofocus
      />
      <FormError :message="error" />
      <BaseButton type="submit" variant="primary" size="lg" :loading="sending">
        Continue with email
      </BaseButton>
      <!-- A full navigation: the server redirects to Google's consent screen. -->
      <a
        v-if="googleEnabled"
        href="/api/auth/google/start"
        class="inline-flex h-10 items-center justify-center rounded-md border border-border-strong bg-elevated px-4 text-sm font-medium hover:bg-hover"
      >
        Continue with Google
      </a>
    </form>
    <p class="mt-6 text-center text-xs text-faint">
      {{ open ? 'No account needed: your email creates one.' : 'Invite only.' }}
    </p>
  </AuthShell>
</template>
