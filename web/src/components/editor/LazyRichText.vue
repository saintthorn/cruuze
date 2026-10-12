<script setup lang="ts">
import { type Component, shallowRef } from 'vue'

/**
 * The rich-text editor, fetched when first needed. The editor is by far the
 * largest piece of the app, and pages that show text shouldn't wait for it:
 * until it arrives the Markdown is shown as plain text.
 */
defineOptions({ inheritAttrs: false })
defineProps<{ modelValue: string }>()

// One request for the whole app, shared by every instance.
const editor = shallowRef<Component | null>(loaded)
if (!loaded) void load().then((component) => (editor.value = component))
</script>

<script lang="ts">
let loaded: Component | null = null
let loading: Promise<Component> | undefined

function load(): Promise<Component> {
  loading ??= import('./RichText.vue').then((module) => (loaded = module.default))
  return loading
}
</script>

<template>
  <component :is="editor" v-if="editor" v-bind="$attrs" :model-value="modelValue" />
  <div v-else :class="[$attrs.class, 'whitespace-pre-wrap text-muted']">{{ modelValue }}</div>
</template>
