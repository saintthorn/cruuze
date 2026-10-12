import { defineStore } from 'pinia'
import { ref } from 'vue'

/** A page of the command palette; everything but `root` picks a value for the target issues. */
export type PaletteStep = 'root' | 'status' | 'priority' | 'assignee' | 'labels'

export const usePaletteStore = defineStore('palette', () => {
  const open = ref(false)
  const step = ref<PaletteStep>('root')

  function show(at: PaletteStep = 'root') {
    step.value = at
    open.value = true
  }

  return { open, step, show }
})
