<script setup lang="ts">
import { Check, ChevronRight } from 'lucide-vue-next'
import {
  ContextMenuContent,
  ContextMenuItem,
  ContextMenuPortal,
  ContextMenuRoot,
  ContextMenuSeparator,
  ContextMenuSub,
  ContextMenuSubContent,
  ContextMenuSubTrigger,
  ContextMenuTrigger,
} from 'reka-ui'
import { computed } from 'vue'
import { useRouter } from 'vue-router'

import { useIssueActions } from '@/composables/useIssueActions'
import { stateKey } from '@/lib/filter'
import { PRIORITIES } from '@/lib/issues'
import { useIssueViewStore } from '@/stores/issueView'
import { useSyncStore } from '@/stores/sync'
import type { Issue, WorkflowState } from '@/sync/types'

import PriorityIcon from './PriorityIcon.vue'
import StateIcon from './StateIcon.vue'

const props = defineProps<{ issue: Issue }>()

const router = useRouter()
const view = useIssueViewStore()
const sync = useSyncStore()
const actions = useIssueActions()

// Right-clicking a selected issue acts on the whole selection.
const ids = computed(() => (view.selected.has(props.issue.id) ? view.targets : [props.issue.id]))
const issues = computed(() => actions.issuesOf(ids.value))
const many = computed(() => ids.value.length > 1)

const issuePath = computed(() => {
  const ref = props.issue.number > 0 ? actions.identifier(props.issue) : props.issue.id
  return router.resolve({ name: 'issue', params: { issueRef: ref } }).href
})

function onOpen(open: boolean) {
  // A right-click outside the selection starts over with just this issue.
  if (open && !view.selected.has(props.issue.id)) view.clearSelection()
}

function archive() {
  actions.archive(ids.value)
  view.clearSelection()
}

const every = (test: (issue: Issue) => boolean) => issues.value.length > 0 && issues.value.every(test)

function inStatus(issue: Issue, state: WorkflowState): boolean {
  const current = sync.state(issue.stateId)
  return current !== undefined && stateKey(current) === stateKey(state)
}

const contentClass = 'z-50 max-h-80 min-w-48 overflow-y-auto rounded-lg border border-border-strong bg-elevated p-1 shadow-xl'
const itemClass =
  'flex h-8 items-center gap-2 rounded px-2 text-[13px] outline-none select-none data-[highlighted]:bg-active data-[state=open]:bg-active'
</script>

<template>
  <ContextMenuRoot @update:open="onOpen">
    <ContextMenuTrigger as-child>
      <slot />
    </ContextMenuTrigger>
    <ContextMenuPortal>
      <ContextMenuContent :class="contentClass">
        <p v-if="many" class="px-2 py-1 text-xs text-faint">{{ ids.length }} issues</p>

        <ContextMenuSub>
          <ContextMenuSubTrigger :class="itemClass">
            Status <ChevronRight class="ml-auto size-3.5 text-faint" aria-hidden="true" />
          </ContextMenuSubTrigger>
          <ContextMenuPortal>
            <ContextMenuSubContent :class="contentClass">
              <ContextMenuItem
                v-for="state in actions.statusOptions(ids)"
                :key="state.id"
                :class="itemClass"
                @select="actions.setStatus(ids, stateKey(state))"
              >
                <StateIcon :type="state.type" :color="state.color" />
                {{ state.name }}
                <Check v-if="every((i) => inStatus(i, state))" class="ml-auto size-4 text-muted" aria-label="Current" />
              </ContextMenuItem>
            </ContextMenuSubContent>
          </ContextMenuPortal>
        </ContextMenuSub>

        <ContextMenuSub>
          <ContextMenuSubTrigger :class="itemClass">
            Priority <ChevronRight class="ml-auto size-3.5 text-faint" aria-hidden="true" />
          </ContextMenuSubTrigger>
          <ContextMenuPortal>
            <ContextMenuSubContent :class="contentClass">
              <ContextMenuItem
                v-for="priority in PRIORITIES"
                :key="priority.value"
                :class="itemClass"
                @select="actions.setPriority(ids, priority.value)"
              >
                <span class="text-muted"><PriorityIcon :priority="priority.value" /></span>
                {{ priority.label }}
                <Check v-if="every((i) => i.priority === priority.value)" class="ml-auto size-4 text-muted" aria-label="Current" />
              </ContextMenuItem>
            </ContextMenuSubContent>
          </ContextMenuPortal>
        </ContextMenuSub>

        <ContextMenuSub>
          <ContextMenuSubTrigger :class="itemClass">
            Assignee <ChevronRight class="ml-auto size-3.5 text-faint" aria-hidden="true" />
          </ContextMenuSubTrigger>
          <ContextMenuPortal>
            <ContextMenuSubContent :class="contentClass">
              <ContextMenuItem :class="itemClass" @select="actions.setAssignee(ids, null)">
                No assignee
                <Check v-if="every((i) => !i.assigneeId)" class="ml-auto size-4 text-muted" aria-label="Current" />
              </ContextMenuItem>
              <ContextMenuItem
                v-for="user in sync.users"
                :key="user.id"
                :class="itemClass"
                @select="actions.setAssignee(ids, user.id)"
              >
                <span class="truncate">{{ user.name || user.email }}</span>
                <Check v-if="every((i) => i.assigneeId === user.id)" class="ml-auto size-4 shrink-0 text-muted" aria-label="Current" />
              </ContextMenuItem>
            </ContextMenuSubContent>
          </ContextMenuPortal>
        </ContextMenuSub>

        <ContextMenuSub v-if="sync.labels.length > 0">
          <ContextMenuSubTrigger :class="itemClass">
            Labels <ChevronRight class="ml-auto size-3.5 text-faint" aria-hidden="true" />
          </ContextMenuSubTrigger>
          <ContextMenuPortal>
            <ContextMenuSubContent :class="contentClass">
              <ContextMenuItem
                v-for="label in sync.labels"
                :key="label.id"
                :class="itemClass"
                @select.prevent="actions.toggleLabel(ids, label.id)"
              >
                <span class="size-2.5 shrink-0 rounded-full" :style="{ background: label.color }" aria-hidden="true" />
                <span class="truncate">{{ label.name }}</span>
                <Check
                  v-if="actions.labelState(ids, label.id) === 'all'"
                  class="ml-auto size-4 shrink-0 text-muted"
                  aria-label="On"
                />
              </ContextMenuItem>
            </ContextMenuSubContent>
          </ContextMenuPortal>
        </ContextMenuSub>

        <ContextMenuSeparator class="my-1 h-px bg-border" />
        <ContextMenuItem v-if="!many" :class="itemClass" @select="router.push(issuePath)">Open issue</ContextMenuItem>
        <ContextMenuItem :class="itemClass" @select="actions.copyIds(ids)">
          {{ many ? 'Copy IDs' : 'Copy ID' }}
        </ContextMenuItem>
        <ContextMenuItem v-if="!many" :class="itemClass" @select="actions.copyLink(issuePath)">Copy link</ContextMenuItem>
        <ContextMenuSeparator class="my-1 h-px bg-border" />
        <ContextMenuItem :class="itemClass" class="text-danger" @select="archive">
          {{ many ? `Archive ${ids.length} issues` : 'Archive' }}
        </ContextMenuItem>
      </ContextMenuContent>
    </ContextMenuPortal>
  </ContextMenuRoot>
</template>
