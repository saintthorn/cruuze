<script setup lang="ts">
import { useRoute } from 'vue-router'

import AppSidebar from '@/components/AppSidebar.vue'
import CommandPalette from '@/components/CommandPalette.vue'
import StatusLine from '@/components/hacker/StatusLine.vue'
import NewIssueDialog from '@/components/NewIssueDialog.vue'
import SaveViewDialog from '@/components/SaveViewDialog.vue'
import ShortcutHelp from '@/components/ShortcutHelp.vue'
import { useShortcuts } from '@/composables/useShortcuts'
import { currentTheme } from '@/lib/themes'
import { useSyncStore } from '@/stores/sync'
import { useUiStore } from '@/stores/ui'

const route = useRoute()
const sync = useSyncStore()
const ui = useUiStore()

/** The team implied by the current page, for new issues. */
function currentTeamId(): string | undefined {
  if (typeof route.params.teamKey === 'string') return sync.teamByKey(route.params.teamKey)?.id
  if (typeof route.params.issueRef === 'string') return sync.issueByRef(route.params.issueRef)?.teamId
  return undefined
}

function newIssue() {
  ui.openNewIssue(currentTeamId())
}

useShortcuts(newIssue)
</script>

<template>
  <div class="flex h-full flex-col">
    <div class="flex min-h-0 flex-1">
      <AppSidebar @new-issue="newIssue" />
      <main class="flex min-w-0 flex-1 flex-col overflow-hidden bg-bg">
        <RouterView />
      </main>
    </div>
    <!-- The editor-style status line belongs to the terminal theme. -->
    <StatusLine v-if="currentTheme === 'hacker'" />
    <NewIssueDialog />
    <CommandPalette />
    <ShortcutHelp />
    <SaveViewDialog />
  </div>
</template>
