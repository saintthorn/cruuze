<script setup lang="ts">
import { ref } from 'vue'
import { useRouter } from 'vue-router'

import { errorMessage } from '@/api/client'
import AuthShell from '@/components/AuthShell.vue'
import BaseButton from '@/components/ui/BaseButton.vue'
import BaseInput from '@/components/ui/BaseInput.vue'
import FormError from '@/components/ui/FormError.vue'
import { useSessionStore } from '@/stores/session'

const router = useRouter()
const session = useSessionStore()

const name = ref(session.user?.name ?? '')
const saving = ref(false)
const error = ref('')

async function submit() {
  error.value = ''
  saving.value = true
  try {
    await session.updateName(name.value)
    await router.replace({ name: 'home' })
  } catch (err) {
    error.value = errorMessage(err)
  } finally {
    saving.value = false
  }
}
</script>

<template>
  <AuthShell title="Your name">
    <form class="flex flex-col gap-4" @submit.prevent="submit">
      <BaseInput
        v-model="name"
        label="Full name"
        placeholder="Ada Lovelace"
        autocomplete="name"
        required
        autofocus
        :maxlength="80"
      />
      <FormError :message="error" />
      <BaseButton type="submit" variant="primary" size="lg" :loading="saving">Continue</BaseButton>
    </form>
  </AuthShell>
</template>
