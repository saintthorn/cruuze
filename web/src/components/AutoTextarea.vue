<script setup lang="ts">
import { nextTick, onBeforeUnmount, onMounted, useTemplateRef, watch } from 'vue'

defineProps<{ label: string; placeholder?: string; maxlength?: number }>()

const model = defineModel<string>({ required: true })
const el = useTemplateRef('el')

// Grows with its content instead of scrolling.
function resize() {
  if (!el.value) return
  el.value.style.height = 'auto'
  el.value.style.height = `${el.value.scrollHeight}px`
}

// How tall the text is depends on how wide the box is, so measure again
// whenever the width changes: the window is resized, or the box was mounted
// before the page had its final layout.
let observer: ResizeObserver | undefined
let measuredWidth = -1

onMounted(() => {
  resize()
  if (!el.value || typeof ResizeObserver === 'undefined') return
  observer = new ResizeObserver(([entry]) => {
    const width = entry?.contentRect.width ?? 0
    // Resizing changes the height, which fires the observer again.
    if (width === measuredWidth) return
    measuredWidth = width
    resize()
  })
  observer.observe(el.value)
})
onBeforeUnmount(() => observer?.disconnect())

watch(model, () => nextTick(resize))

defineExpose({ blur: () => el.value?.blur() })
</script>

<template>
  <textarea
    ref="el"
    v-model="model"
    :aria-label="label"
    :placeholder="placeholder"
    :maxlength="maxlength"
    rows="1"
    class="block w-full resize-none overflow-hidden bg-transparent outline-none placeholder:text-faint"
  />
</template>
