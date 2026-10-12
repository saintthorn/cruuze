<script setup lang="ts">
import { computed, ref } from 'vue'

import { errorMessage } from '@/api/client'
import BaseButton from '@/components/ui/BaseButton.vue'
import FormError from '@/components/ui/FormError.vue'
import { useToastStore } from '@/stores/toasts'
import { useWorkspaceStore } from '@/stores/workspace'

/** The customer portal: whether it is open, what it says, and whether it has a public board. */
const workspace = useWorkspaceStore()
const toasts = useToastStore()

const url = computed(() => `${window.location.origin}/portal/${workspace.current?.urlKey}`)
const intro = ref(workspace.current?.portalIntro ?? '')
const error = ref('')
const saving = ref(false)

async function set(patch: Parameters<typeof workspace.updatePortal>[0], event?: Event) {
  error.value = ''
  try {
    await workspace.updatePortal(patch)
  } catch (err) {
    if (event) (event.target as HTMLInputElement).checked = !(event.target as HTMLInputElement).checked
    error.value = errorMessage(err)
  }
}

async function saveIntro() {
  saving.value = true
  await set({ intro: intro.value })
  saving.value = false
  intro.value = workspace.current?.portalIntro ?? ''
  if (!error.value) toasts.show('Introduction saved')
}
</script>

<template>
  <section aria-labelledby="portal-heading" class="rounded-lg border border-border p-4">
    <h2 id="portal-heading" class="font-medium">Customer portal</h2>
    <p class="mt-0.5 text-muted">A public page where customers send requests.</p>
    <FormError :message="error" class="mt-2" />
    <label class="mt-3 flex items-center gap-2 select-none">
      <input
        type="checkbox"
        :checked="workspace.current?.portalEnabled"
        :disabled="!workspace.isAdmin"
        class="size-3.5 accent-(--accent)"
        @change="set({ enabled: ($event.target as HTMLInputElement).checked }, $event)"
      />
      Enabled
    </label>
    <p v-if="workspace.current?.portalEnabled" class="mt-1 ml-5.5 text-xs">
      <a :href="url" target="_blank" rel="noopener" class="text-accent hover:underline">{{ url }}</a>
    </p>
    <label class="mt-3 flex items-center gap-2 select-none">
      <input
        type="checkbox"
        :checked="workspace.current?.portalPublicBoard"
        :disabled="!workspace.isAdmin"
        class="size-3.5 accent-(--accent)"
        @change="set({ publicBoard: ($event.target as HTMLInputElement).checked }, $event)"
      />
      Public board with voting
    </label>
    <form v-if="workspace.isAdmin" class="mt-3 flex flex-col gap-2" @submit.prevent="saveIntro">
      <textarea
        v-model="intro"
        aria-label="Portal introduction"
        rows="2"
        maxlength="2000"
        placeholder="Introduction (optional)"
        class="resize-y rounded-md border border-border-strong bg-elevated px-3 py-2 outline-none placeholder:text-faint focus:border-accent"
      />
      <BaseButton type="submit" class="self-start" :loading="saving" :disabled="intro.trim() === (workspace.current?.portalIntro ?? '')">Save</BaseButton>
    </form>
    <p v-else class="mt-2 text-xs text-faint">Admins only.</p>
  </section>
</template>
