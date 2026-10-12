import { useRouter } from 'vue-router'

import { issueIdentifier } from '@/lib/issues'
import { useIssueViewStore } from '@/stores/issueView'
import { useSyncStore } from '@/stores/sync'
import type { Issue } from '@/sync/types'

/** How long the pointer rests on an issue before its details are fetched: passing over a list fetches nothing. */
const WARM_AFTER_MS = 90
let warming: ReturnType<typeof setTimeout> | undefined

/** Pointer behaviour shared by issue rows and cards. */
export function useIssueSelect() {
  const router = useRouter()
  const view = useIssueViewStore()
  const sync = useSyncStore()

  /** Where an issue's page lives; unnumbered issues are addressed by ID. */
  function issueRoute(issue: Issue) {
    const issueRef = issue.number > 0 ? issueIdentifier(issue, sync.team(issue.teamId)) : issue.id
    return { name: 'issue', params: { issueRef } }
  }

  /**
   * A click on an issue: shift extends the selection, ⌘/Ctrl toggles it, and
   * a plain click opens the issue.
   */
  function onClick(event: MouseEvent, issue: Issue) {
    if (event.shiftKey) {
      event.preventDefault()
      // Shift-click would otherwise select the text in between.
      window.getSelection()?.removeAllRanges()
      view.selectRangeTo(issue.id)
    } else if (event.metaKey || event.ctrlKey) {
      event.preventDefault()
      view.toggle(issue.id)
    } else {
      void router.push(issueRoute(issue))
    }
  }

  /**
   * The pointer moved over an issue: it becomes the target of shortcuts, and
   * once the pointer has rested on it a moment its comments and activity are
   * fetched, so that opening it has nothing to wait for.
   */
  function onHover(issue: Issue) {
    if (view.focusedId === issue.id) return
    view.focusedId = issue.id
    clearTimeout(warming)
    // An issue the server hasn't numbered yet has nothing there to fetch.
    if (issue.number === 0) return
    warming = setTimeout(() => void sync.loadIssueDetails(issue.id).catch(() => {}), WARM_AFTER_MS)
  }

  return { issueRoute, onClick, onHover }
}
