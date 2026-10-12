import { onBeforeUnmount, onMounted } from 'vue'
import { useRouter } from 'vue-router'

import { useIssueViewStore } from '@/stores/issueView'
import { type PaletteStep, usePaletteStore } from '@/stores/palette'
import { useUiStore } from '@/stores/ui'
import { useViewOptionsStore } from '@/stores/viewOptions'

import { useIssueActions } from './useIssueActions'
import { useIssueSelect } from './useIssueSelect'

/** How long after `G` its second key is still accepted. */
const SEQUENCE_MS = 1500

const PROPERTY_KEYS: Record<string, PaletteStep> = { s: 'status', p: 'priority', a: 'assignee', l: 'labels' }

/**
 * The app's keyboard shortcuts. `newIssue` opens the new-issue dialog for
 * whatever team the current page implies.
 */
export function useShortcuts(newIssue: () => void) {
  const router = useRouter()
  const view = useIssueViewStore()
  const palette = usePaletteStore()
  const ui = useUiStore()
  const viewOptions = useViewOptionsStore()
  const actions = useIssueActions()
  const { issueRoute } = useIssueSelect()

  let goPending = false
  let goTimer: ReturnType<typeof setTimeout> | undefined

  function moveFocus(delta: number) {
    const id = view.moveFocus(delta)
    if (!id) return
    // Lists only render the rows near the viewport; ask the list to bring
    // this one in. Boards render everything, so the element is there.
    view.reveal(id)
    document.querySelector(`[data-issue-id="${id}"]`)?.scrollIntoView({ block: 'nearest' })
  }

  function onKeydown(event: KeyboardEvent) {
    const mod = event.metaKey || event.ctrlKey
    let key = event.key.length === 1 ? event.key.toLowerCase() : event.key
    // Some keyboard layouts and tools report Shift+/ without translating it.
    if (key === '/' && event.shiftKey) key = '?'

    // The command menu opens from anywhere, even while typing.
    if (mod && key === 'k') {
      event.preventDefault()
      if (palette.open) palette.open = false
      else palette.show()
      return
    }

    // Everything else stays out of the way of text fields, menus and dialogs.
    const target = event.target instanceof Element ? event.target : null
    if (target?.closest('input, textarea, select, [contenteditable]')) return
    if (document.querySelector('[role="dialog"], [role="menu"]')) return

    if (mod) {
      if (key === 'b' && viewOptions.current) {
        event.preventDefault()
        viewOptions.current.layout = viewOptions.current.layout === 'list' ? 'board' : 'list'
      } else if (key === 'a' && view.visibleIds.length > 0) {
        event.preventDefault()
        view.selectAll()
      } else if (key === 'Backspace' && view.targets.length > 0) {
        event.preventDefault()
        actions.archive(view.targets)
        view.clearSelection()
      }
      return
    }
    if (event.altKey) return

    if (goPending) {
      goPending = false
      clearTimeout(goTimer)
      if (key === 'i') void router.push({ name: 'inbox' })
      else if (key === 'm') void router.push({ name: 'my-issues' })
      else if (key === 's') void router.push({ name: 'settings-members' })
      else return
      event.preventDefault()
      return
    }

    const step = PROPERTY_KEYS[key]
    if (step) {
      if (view.targets.length === 0) return
      event.preventDefault()
      palette.show(step)
      return
    }

    switch (key) {
      case 'g':
        goPending = true
        goTimer = setTimeout(() => (goPending = false), SEQUENCE_MS)
        break
      case 'c':
        event.preventDefault()
        newIssue()
        break
      case '/':
        event.preventDefault()
        void router.push({ name: 'search' })
        break
      case '?':
        event.preventDefault()
        ui.shortcutsOpen = true
        break
      case 'f':
        if (!viewOptions.current) return
        event.preventDefault()
        ui.filterOpen = true
        break
      case 'j':
      case 'ArrowDown':
        if (view.visibleIds.length === 0) return
        event.preventDefault()
        moveFocus(1)
        break
      case 'k':
      case 'ArrowUp':
        if (view.visibleIds.length === 0) return
        event.preventDefault()
        moveFocus(-1)
        break
      case 'x':
        if (!view.focusedId) return
        event.preventDefault()
        view.toggle(view.focusedId)
        break
      case 'Enter': {
        // Leave Enter alone when it would activate a focused control.
        if (target && target !== document.body) return
        const issue = view.focusedId ? actions.issuesOf([view.focusedId])[0] : undefined
        if (!issue) return
        event.preventDefault()
        void router.push(issueRoute(issue))
        break
      }
      case 'Escape':
        view.clearSelection()
        break
    }
  }

  onMounted(() => window.addEventListener('keydown', onKeydown))
  onBeforeUnmount(() => {
    window.removeEventListener('keydown', onKeydown)
    clearTimeout(goTimer)
  })
}
