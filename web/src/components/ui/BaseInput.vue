<script setup lang="ts">
import { onMounted, useId, useTemplateRef } from 'vue'

const props = defineProps<{
  label: string
  type?: string
  placeholder?: string
  hint?: string
  /** Shown before the input, e.g. a URL prefix. */
  prefix?: string
  autocomplete?: string
  required?: boolean
  maxlength?: number
  autofocus?: boolean
}>()

const model = defineModel<string>({ required: true })
const id = useId()
const input = useTemplateRef('input')

// The autofocus attribute only fires on page load, not on client-side navigation.
onMounted(() => {
  if (props.autofocus) input.value?.focus()
})
</script>

<template>
  <div class="flex flex-col gap-1.5">
    <label :for="id" class="text-xs font-medium text-muted">{{ label }}</label>
    <div
      class="flex h-10 items-center rounded-md border border-border-strong bg-elevated focus-within:border-accent"
    >
      <span v-if="prefix" class="pl-3 text-faint select-none">{{ prefix }}</span>
      <input
        :id="id"
        ref="input"
        v-model="model"
        :type="type ?? 'text'"
        :placeholder="placeholder"
        :autocomplete="autocomplete ?? 'off'"
        :required="required"
        :maxlength="maxlength"
        :aria-describedby="hint ? `${id}-hint` : undefined"
        class="h-full min-w-0 flex-1 bg-transparent text-sm text-fg outline-none placeholder:text-faint"
        :class="prefix ? 'pr-3 pl-0.5' : 'px-3'"
      />
    </div>
    <p v-if="hint" :id="`${id}-hint`" class="text-xs text-faint">{{ hint }}</p>
  </div>
</template>
