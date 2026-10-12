<script setup lang="ts">
import {
  DialogContent,
  DialogDescription,
  DialogOverlay,
  DialogPortal,
  DialogRoot,
  DialogTitle,
} from 'reka-ui'
import { ref, watch } from 'vue'

import { errorMessage } from '@/api/client'
import { suggestTeamKey, TEAM_KEY_PATTERN } from '@/lib/keys'
import { TEAM_KINDS, type TeamKind } from '@/lib/teamKinds'
import { type TeamView, useWorkspaceStore } from '@/stores/workspace'

import BaseButton from './ui/BaseButton.vue'
import BaseInput from './ui/BaseInput.vue'
import FormError from './ui/FormError.vue'

const open = defineModel<boolean>('open', { required: true })
const emit = defineEmits<{ created: [team: TeamView] }>()

const workspace = useWorkspaceStore()

const name = ref('')
const key = ref('')
// Keep suggesting an identifier from the name until the user edits it.
const keyEdited = ref(false)
const isPrivate = ref(false)
const kind = ref<TeamKind>('engineering')
const saving = ref(false)
const error = ref('')

watch(open, (isOpen) => {
  if (!isOpen) return
  name.value = ''
  key.value = ''
  keyEdited.value = false
  isPrivate.value = false
  kind.value = 'engineering'
  error.value = ''
})

watch(name, (value) => {
  if (!keyEdited.value) key.value = suggestTeamKey(value)
})

async function submit() {
  error.value = ''
  const normalizedKey = key.value.trim().toUpperCase()
  if (!TEAM_KEY_PATTERN.test(normalizedKey)) {
    error.value = 'Identifier must be 1-7 letters or digits, starting with a letter.'
    return
  }
  saving.value = true
  try {
    const team = await workspace.createTeam(name.value, normalizedKey, isPrivate.value, kind.value)
    open.value = false
    emit('created', team)
  } catch (err) {
    error.value = errorMessage(err)
  } finally {
    saving.value = false
  }
}
</script>

<template>
  <DialogRoot v-model:open="open">
    <DialogPortal>
      <DialogOverlay class="fixed inset-0 z-40 bg-black/50" />
      <DialogContent
        class="fixed top-[8%] left-1/2 z-50 max-h-[88vh] w-[calc(100%-2rem)] max-w-md overflow-y-auto -translate-x-1/2 rounded-xl border border-border-strong bg-elevated p-5 shadow-2xl"
      >
        <DialogTitle class="text-base font-semibold">Create a team</DialogTitle>
        <DialogDescription class="sr-only">Name the team and choose its kind.</DialogDescription>
        <form class="mt-5 flex flex-col gap-4" @submit.prevent="submit">
          <BaseInput v-model="name" label="Team name" placeholder="Engineering" required autofocus :maxlength="80" />
          <BaseInput
            v-model="key"
            label="Identifier"
            placeholder="ENG"
            hint="Prefixes issue IDs: ENG-123."
            required
            :maxlength="7"
            @input="keyEdited = true"
          />
          <fieldset class="flex flex-col gap-1.5">
            <legend class="mb-1.5 text-xs font-medium text-muted">Kind of team</legend>
            <label
              v-for="option in TEAM_KINDS"
              :key="option.value"
              class="flex cursor-pointer items-start gap-2 rounded-md border px-2.5 py-1.5 select-none"
              :class="kind === option.value ? 'border-accent' : 'border-border-strong hover:bg-hover'"
            >
              <input v-model="kind" type="radio" name="team-kind" :value="option.value" class="mt-0.5 size-3.5 accent-(--accent)" />
              <span>
                {{ option.label }}
                <span class="block text-xs text-faint">{{ option.description }}</span>
              </span>
            </label>
          </fieldset>
          <label class="flex items-start gap-2 select-none">
            <input v-model="isPrivate" type="checkbox" class="mt-0.5 size-3.5 accent-(--accent)" />
            <span>
              Private
              <span class="block text-xs text-faint">Visible to members only.</span>
            </span>
          </label>
          <FormError :message="error" />
          <div class="flex justify-end gap-2">
            <BaseButton variant="ghost" @click="open = false">Cancel</BaseButton>
            <BaseButton type="submit" variant="primary" :loading="saving">Create team</BaseButton>
          </div>
        </form>
      </DialogContent>
    </DialogPortal>
  </DialogRoot>
</template>
