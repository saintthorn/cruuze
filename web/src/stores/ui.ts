import { defineStore } from 'pinia'
import { ref } from 'vue'

/** App-wide UI state that more than one component needs to reach. */
export const useUiStore = defineStore('ui', () => {
  const newIssueOpen = ref(false)
  /** The team the new-issue dialog starts on. */
  const newIssueTeamId = ref<string | undefined>()
  /** The status the new-issue dialog starts on, e.g. a board column's. */
  const newIssueStateId = ref<string | undefined>()

  const shortcutsOpen = ref(false)

  /** Whether the filter menu of the current issue view is open. */
  const filterOpen = ref(false)

  /** Presets for the new-issue dialog beyond team and status. */
  const newIssuePreset = ref<{ projectId?: string; cycleId?: string; parentId?: string; dueDate?: string }>({})
  const saveViewOpen = ref(false)

  function openNewIssue(teamId?: string, stateId?: string, preset: { projectId?: string; cycleId?: string; parentId?: string; dueDate?: string } = {}) {
    newIssueTeamId.value = teamId
    newIssueStateId.value = stateId
    newIssuePreset.value = preset
    newIssueOpen.value = true
  }

  return { newIssueOpen, newIssueTeamId, newIssueStateId, newIssuePreset, saveViewOpen, shortcutsOpen, filterOpen, openNewIssue }
})
