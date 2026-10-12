import { defineStore } from 'pinia'
import { ref } from 'vue'

export interface Toast {
  id: number
  message: string
}

const DISMISS_AFTER_MS = 6000

/** Short-lived messages shown in the corner of the app. */
export const useToastStore = defineStore('toasts', () => {
  const toasts = ref<Toast[]>([])
  let nextId = 1

  function show(message: string) {
    const id = nextId++
    toasts.value.push({ id, message })
    setTimeout(() => dismiss(id), DISMISS_AFTER_MS)
  }

  function dismiss(id: number) {
    toasts.value = toasts.value.filter((t) => t.id !== id)
  }

  return { toasts, show, dismiss }
})
