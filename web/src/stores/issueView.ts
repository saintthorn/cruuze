import { defineStore } from 'pinia'
import { computed, ref, shallowRef } from 'vue'

/**
 * Which issues the user is acting on in the current list or board: the
 * selection, the row under the pointer or keyboard cursor, and what is being
 * dragged. Commands apply to {@link targets}.
 */
export const useIssueViewStore = defineStore('issueView', () => {
  const selected = ref(new Set<string>())
  /** The row the pointer or keyboard cursor is on. */
  const focusedId = ref<string | null>(null)
  /** The issue open on the detail page, if any. */
  const detailId = ref<string | null>(null)
  /** Issue IDs in the order they are displayed. */
  const visibleIds = shallowRef<string[]>([])
  /** Issues being dragged. */
  const dragging = shallowRef<string[]>([])
  /** Where a shift-click range starts. */
  let anchorId: string | null = null
  /** Scrolls a row into view; set by the list on screen, which may not have rendered it. */
  let revealer: ((id: string) => void) | null = null

  function setRevealer(fn: ((id: string) => void) | null) {
    revealer = fn
  }

  /** Scrolls the row for an issue into view, if a list is showing it. */
  function reveal(id: string) {
    revealer?.(id)
  }

  /** What a command acts on: the selection, else the focused row, else the open issue. */
  const targets = computed<string[]>(() => {
    if (selected.value.size > 0) return visibleIds.value.filter((id) => selected.value.has(id))
    if (focusedId.value) return [focusedId.value]
    return detailId.value ? [detailId.value] : []
  })

  /** Called by the list or board whenever what it shows changes. */
  function setVisible(ids: string[]) {
    visibleIds.value = ids
    if (selected.value.size === 0 && !focusedId.value) return
    // Never keep acting on issues that have scrolled out of the filter.
    const visible = new Set(ids)
    for (const id of selected.value) {
      if (!visible.has(id)) selected.value.delete(id)
    }
    if (focusedId.value && !visible.has(focusedId.value)) focusedId.value = null
  }

  function toggle(id: string) {
    if (selected.value.has(id)) selected.value.delete(id)
    else selected.value.add(id)
    anchorId = id
  }

  /** Selects everything between the last toggled issue and `id`. */
  function selectRangeTo(id: string) {
    const ids = visibleIds.value
    const from = anchorId ? ids.indexOf(anchorId) : -1
    const to = ids.indexOf(id)
    if (from === -1 || to === -1) return toggle(id)
    for (const rangeId of ids.slice(Math.min(from, to), Math.max(from, to) + 1)) selected.value.add(rangeId)
  }

  function selectAll() {
    selected.value = new Set(visibleIds.value)
  }

  function clearSelection() {
    selected.value.clear()
    anchorId = null
  }

  /** Moves the keyboard cursor by `delta` rows and returns the new row's ID. */
  function moveFocus(delta: number): string | null {
    const ids = visibleIds.value
    if (ids.length === 0) return null
    const current = focusedId.value ? ids.indexOf(focusedId.value) : -1
    const next = current === -1 ? (delta > 0 ? 0 : ids.length - 1) : Math.min(ids.length - 1, Math.max(0, current + delta))
    focusedId.value = ids[next]!
    return focusedId.value
  }

  /** Forgets the selection and cursor, e.g. when moving to another view. */
  function reset() {
    clearSelection()
    focusedId.value = null
    dragging.value = []
  }

  return {
    selected,
    focusedId,
    detailId,
    visibleIds,
    dragging,
    targets,
    setVisible,
    toggle,
    selectRangeTo,
    selectAll,
    clearSelection,
    moveFocus,
    setRevealer,
    reveal,
    reset,
  }
})
