<script setup lang="ts">
import { ref, watch } from 'vue'
import { useRoute, useRouter } from 'vue-router'

import { useSyncStore } from '@/stores/sync'
import { useUiStore } from '@/stores/ui'
import { useViewOptionsStore } from '@/stores/viewOptions'
import { useWorkspaceStore } from '@/stores/workspace'

import PromptDialog from './PromptDialog.vue'

const route = useRoute()
const router = useRouter()
const sync = useSyncStore()
const ui = useUiStore()
const viewOptions = useViewOptionsStore()
const workspace = useWorkspaceStore()

const shared = ref(false)
watch(
  () => ui.saveViewOpen,
  (open) => {
    if (open) shared.value = false
  },
)

/** Saves the filters and display options on screen as a named view. */
function save(name: string) {
  const options = viewOptions.current
  if (!options) return
  // Saved from a team's list, the view stays limited to that team.
  const teamId = typeof route.params.teamKey === 'string' ? sync.teamByKey(route.params.teamKey)?.id : undefined
  const view = sync.createView(name, JSON.parse(JSON.stringify(options)) as Record<string, unknown>, teamId)
  if (shared.value) sync.updateRecord('View', view.id, { shared: true })
  void router.push({ name: 'view', params: { viewId: view.id } })
}
</script>

<template>
  <PromptDialog
    v-model:open="ui.saveViewOpen"
    title="Save as view"
    label="View name"
    placeholder="Urgent bugs"
    action="Save view"
    @submit="save"
  >
    <label v-if="workspace.canManageLabels" class="flex items-center gap-2 text-muted select-none">
      <input v-model="shared" type="checkbox" class="size-3.5 accent-(--accent)" /> Share with everyone in the workspace
    </label>
  </PromptDialog>
</template>
