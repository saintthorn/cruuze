import { defineStore } from 'pinia'
import { computed, effectScope, reactive, ref, watch } from 'vue'

import { emptyFilter, type IssueFilter, parseFilter } from '@/lib/filter'
import { type Grouping, GROUPINGS, type Ordering, ORDERINGS } from '@/lib/issues'

export type Layout = 'list' | 'board'

/** How one issue view is laid out and what it shows. */
export interface ViewOptions {
  layout: Layout
  grouping: Grouping
  ordering: Ordering
  /** Whether completed and canceled issues are shown. */
  showClosed: boolean
  filter: IssueFilter
}

function defaults(): ViewOptions {
  return { layout: 'list', grouping: 'status', ordering: 'manual', showClosed: true, filter: emptyFilter() }
}

const storageKey = (view: string) => `cruuze:view:${view}`

/** Coerces untrusted JSON (local storage, a saved view) into valid options. */
export function parseViewOptions(raw: unknown): ViewOptions {
  const options = defaults()
  const stored = (typeof raw === 'object' && raw !== null ? raw : {}) as Record<string, unknown>
  if (stored.layout === 'list' || stored.layout === 'board') options.layout = stored.layout
  if (GROUPINGS.some((g) => g.value === stored.grouping)) options.grouping = stored.grouping as Grouping
  if (ORDERINGS.some((o) => o.value === stored.ordering)) options.ordering = stored.ordering as Ordering
  if (typeof stored.showClosed === 'boolean') options.showClosed = stored.showClosed
  options.filter = parseFilter(stored.filter)
  return options
}

function load(view: string): ViewOptions {
  try {
    return parseViewOptions(JSON.parse(localStorage.getItem(storageKey(view)) ?? '{}'))
  } catch {
    // Unreadable or unavailable storage: use the defaults.
    return defaults()
  }
}

/**
 * Display options for each issue view (e.g. `team:ENG`, `my-issues`),
 * remembered per view in this browser.
 */
export const useViewOptionsStore = defineStore('viewOptions', () => {
  const views = reactive(new Map<string, ViewOptions>())
  const scope = effectScope(true)
  /** The view on screen, so shortcuts and commands can change its options. */
  const currentView = ref<string | null>(null)

  function optionsFor(view: string): ViewOptions {
    let options = views.get(view)
    if (!options) {
      views.set(view, load(view))
      options = views.get(view)!
      const tracked = options
      // Detached, so the watcher outlives whichever component asked first.
      scope.run(() =>
        watch(
          tracked,
          (value) => {
            try {
              localStorage.setItem(storageKey(view), JSON.stringify(value))
            } catch {
              // Storage can be unavailable; the choice then lasts for this visit only.
            }
          },
          { deep: true },
        ),
      )
    }
    return options
  }

  const current = computed(() => (currentView.value ? optionsFor(currentView.value) : null))

  return { currentView, current, optionsFor }
})
