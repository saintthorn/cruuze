import { ref } from 'vue'

import { sortOrdersForDrop } from '@/lib/order'
import { useIssueViewStore } from '@/stores/issueView'
import { useSyncStore } from '@/stores/sync'
import type { IssuePatch } from '@/sync/mutations'
import type { Issue } from '@/sync/types'

/** Somewhere issues can be dropped: a list group or a board column. */
export interface DropZone {
  key: string
  /** The zone's issues in display order. */
  issues: Issue[]
  /** What landing in this zone changes about an issue, besides its position. */
  patch(issue: Issue): IssuePatch
}

/**
 * Drag and drop for issue rows and cards, using the browser's native drag
 * events. Dragging a selected issue drags the whole selection.
 *
 * `manualOrder` says whether the view is ordered by hand; only then does the
 * drop position within a zone matter.
 */
export function useIssueDrag(manualOrder: () => boolean) {
  const view = useIssueViewStore()
  const sync = useSyncStore()

  /** Where the dragged issues would land: an index among the zone's other issues. */
  const dropTarget = ref<{ key: string; index: number } | null>(null)

  function onDragStart(event: DragEvent, issue: Issue) {
    const ids = view.selected.has(issue.id) ? view.visibleIds.filter((id) => view.selected.has(id)) : [issue.id]
    view.dragging = ids
    if (event.dataTransfer) {
      event.dataTransfer.effectAllowed = 'move'
      event.dataTransfer.setData('text/plain', issue.title)
    }
  }

  function onDragEnd() {
    view.dragging = []
    dropTarget.value = null
  }

  /** The zone's issues that aren't being dragged. */
  function others(zone: DropZone): Issue[] {
    return zone.issues.filter((i) => !view.dragging.includes(i.id))
  }

  function onDragOver(event: DragEvent, zone: DropZone) {
    if (view.dragging.length === 0) return
    event.preventDefault()
    if (event.dataTransfer) event.dataTransfer.dropEffect = 'move'

    let index = others(zone).length
    if (manualOrder()) {
      // The first issue whose middle is below the pointer is the one we'd land before.
      const container = event.currentTarget as HTMLElement
      const rows = [...container.querySelectorAll<HTMLElement>('[data-issue-id]')].filter(
        (el) => !view.dragging.includes(el.dataset.issueId!),
      )
      const below = rows.find((el) => {
        const rect = el.getBoundingClientRect()
        return event.clientY < rect.top + rect.height / 2
      })
      // A long list only renders the rows near the viewport, so a row's
      // place among the rendered ones isn't its place in the zone.
      const rest = others(zone)
      const indexOf = (el: HTMLElement) => rest.findIndex((i) => i.id === el.dataset.issueId)
      const last = rows[rows.length - 1]
      if (below) index = Math.max(0, indexOf(below))
      else if (last) index = indexOf(last) + 1
    }
    const current = dropTarget.value
    if (!current || current.key !== zone.key || current.index !== index) dropTarget.value = { key: zone.key, index }
  }

  function onDragLeave(event: DragEvent, zone: DropZone) {
    // Moving onto a child fires dragleave too; only clear when really leaving.
    const container = event.currentTarget as HTMLElement
    if (container.contains(event.relatedTarget as Node | null)) return
    if (dropTarget.value?.key === zone.key) dropTarget.value = null
  }

  function onDrop(event: DragEvent, zone: DropZone) {
    const target = dropTarget.value
    const ids = view.dragging
    onDragEnd()
    if (ids.length === 0) return
    event.preventDefault()

    const rest = zone.issues.filter((i) => !ids.includes(i.id))
    const index = target?.key === zone.key ? target.index : rest.length
    // Every other issue on screen, so a drop at a zone's edge doesn't land
    // on the sort order of an issue in the next zone.
    const visible = new Set(view.visibleIds)
    const onScreen = sync.issues.filter((i) => visible.has(i.id) && !ids.includes(i.id))
    const orders = manualOrder()
      ? sortOrdersForDrop(rest.map((i) => i.sortOrder), index, ids.length, onScreen.map((i) => i.sortOrder))
      : undefined

    ids.forEach((id, n) => {
      const issue = sync.issues.find((i) => i.id === id)
      if (!issue) return
      const patch = zone.patch(issue)
      if (orders && orders[n] !== issue.sortOrder) patch.sortOrder = orders[n]
      if (Object.keys(patch).length > 0) sync.updateIssue(id, patch)
    })
  }

  /** Whether the drop indicator belongs before the zone's issue `issueId`. */
  function dropsBefore(zone: DropZone, issueId: string): boolean {
    const target = dropTarget.value
    if (!target || target.key !== zone.key || !manualOrder()) return false
    return others(zone)[target.index]?.id === issueId
  }

  /** Whether the drop indicator belongs after the zone's last issue. */
  function dropsAtEnd(zone: DropZone): boolean {
    const target = dropTarget.value
    if (!target || target.key !== zone.key) return false
    return !manualOrder() || target.index >= others(zone).length
  }

  return { dropTarget, onDragStart, onDragEnd, onDragOver, onDragLeave, onDrop, dropsBefore, dropsAtEnd }
}
