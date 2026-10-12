import { ref, watch } from 'vue'

/**
 * A local, editable copy of a synced text value. While the user is editing,
 * changes arriving from elsewhere don't overwrite what they are typing; the
 * draft is saved when they leave the field.
 */
export function useDraft(source: () => string, save: (value: string) => void) {
  const draft = ref(source())
  const editing = ref(false)

  watch(source, (value) => {
    if (!editing.value) draft.value = value
  })

  function onFocus() {
    editing.value = true
  }

  function onBlur() {
    editing.value = false
    if (draft.value !== source()) save(draft.value)
    // If the save was refused or normalized, show what is actually stored.
    draft.value = source()
  }

  return { draft, onFocus, onBlur }
}
