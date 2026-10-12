<script setup lang="ts">
import { DialogContent, DialogDescription, DialogOverlay, DialogPortal, DialogRoot, DialogTitle } from 'reka-ui'
import { ref, watch } from 'vue'

import BaseButton from './ui/BaseButton.vue'
import BaseInput from './ui/BaseInput.vue'

defineProps<{
  title: string
  description?: string
  /** Label of the text field. */
  label: string
  placeholder?: string
  /** Text of the confirm button. */
  action: string
}>()

const open = defineModel<boolean>('open', { required: true })
const emit = defineEmits<{ submit: [value: string] }>()

const value = ref('')
watch(open, (isOpen) => {
  if (isOpen) value.value = ''
})

function submit() {
  if (!value.value.trim()) return
  emit('submit', value.value.trim())
  open.value = false
}
</script>

<template>
  <DialogRoot v-model:open="open">
    <DialogPortal>
      <DialogOverlay class="fixed inset-0 z-40 bg-black/50" />
      <DialogContent
        class="fixed top-[20%] left-1/2 z-50 w-[calc(100%-2rem)] max-w-md -translate-x-1/2 rounded-xl border border-border-strong bg-elevated p-5 shadow-2xl"
      >
        <DialogTitle class="text-base font-semibold">{{ title }}</DialogTitle>
        <DialogDescription v-if="description" class="mt-1 text-muted">{{ description }}</DialogDescription>
        <DialogDescription v-else class="sr-only">{{ title }}</DialogDescription>
        <form class="mt-5 flex flex-col gap-4" @submit.prevent="submit">
          <BaseInput v-model="value" :label="label" :placeholder="placeholder" required autofocus :maxlength="80" />
          <slot />
          <div class="flex justify-end gap-2">
            <BaseButton variant="ghost" @click="open = false">Cancel</BaseButton>
            <BaseButton type="submit" variant="primary">{{ action }}</BaseButton>
          </div>
        </form>
      </DialogContent>
    </DialogPortal>
  </DialogRoot>
</template>
