<script setup lang="ts">
import { Plus, X } from 'lucide-vue-next'
import { computed } from 'vue'
import { RouterLink } from 'vue-router'

import StateIcon from '@/components/StateIcon.vue'
import { useIssueSelect } from '@/composables/useIssueSelect'
import { issueIdentifier } from '@/lib/issues'
import { useSyncStore } from '@/stores/sync'
import type { Issue, Relation } from '@/sync/types'

import IssuePicker from './IssuePicker.vue'

const props = defineProps<{ issue: Issue }>()

const sync = useSyncStore()
const { issueRoute } = useIssueSelect()

/** How a relation reads from this issue's side. */
function phrase(relation: Relation): string {
  const outgoing = relation.issueId === props.issue.id
  switch (relation.type) {
    case 'blocks':
      return outgoing ? 'Blocks' : 'Blocked by'
    case 'duplicate':
      return outgoing ? 'Duplicate of' : 'Duplicated by'
    case 'related':
      return 'Related to'
  }
}

// Relations to issues this user can't see are left out.
const relations = computed(() =>
  sync
    .relationsForIssue(props.issue.id)
    .map((relation) => ({
      relation,
      phrase: phrase(relation),
      other: sync.issue(relation.issueId === props.issue.id ? relation.relatedIssueId : relation.issueId),
    }))
    .filter((entry): entry is typeof entry & { other: Issue } => entry.other !== undefined)
    .sort((a, b) => a.phrase.localeCompare(b.phrase)),
)

const ADD: { type: Relation['type']; reverse?: boolean; label: string }[] = [
  { type: 'blocks', label: 'Blocks…' },
  { type: 'blocks', reverse: true, label: 'Blocked by…' },
  { type: 'related', label: 'Related to…' },
  { type: 'duplicate', label: 'Duplicate of…' },
]

function add(option: (typeof ADD)[number], otherId: string) {
  if (option.reverse) sync.createRelation(otherId, props.issue.id, option.type)
  else sync.createRelation(props.issue.id, otherId, option.type)
}
</script>

<template>
  <section aria-labelledby="relations-heading" class="flex flex-col gap-2">
    <div class="flex flex-wrap items-center gap-1">
      <h2 id="relations-heading" class="mr-auto font-medium">Relations</h2>
      <IssuePicker
        v-for="option in ADD"
        :key="option.label"
        :label="`Add relation: ${option.label}`"
        :exclude="[issue.id]"
        trigger-class="flex h-7 items-center gap-1 rounded-md px-2 text-xs text-muted hover:bg-hover hover:text-fg"
        @select="add(option, $event)"
      >
        <Plus class="size-3" aria-hidden="true" /> {{ option.label }}
      </IssuePicker>
    </div>
    <ul v-if="relations.length > 0" class="divide-y divide-border rounded-lg border border-border">
      <li v-for="entry in relations" :key="entry.relation.id" class="flex h-9 items-center gap-2.5 px-3">
        <span class="w-24 shrink-0 text-xs text-muted">{{ entry.phrase }}</span>
        <RouterLink :to="issueRoute(entry.other)" class="flex min-w-0 flex-1 items-center gap-2.5 hover:underline">
          <StateIcon
            v-if="sync.state(entry.other.stateId)"
            :type="sync.state(entry.other.stateId)!.type"
            :color="sync.state(entry.other.stateId)!.color"
          />
          <span class="shrink-0 text-muted tabular-nums">{{ issueIdentifier(entry.other, sync.team(entry.other.teamId)) }}</span>
          <span class="truncate">{{ entry.other.title }}</span>
        </RouterLink>
        <button
          type="button"
          class="flex size-6 shrink-0 items-center justify-center rounded text-faint hover:bg-hover hover:text-fg"
          :aria-label="`Remove relation to ${issueIdentifier(entry.other, sync.team(entry.other.teamId))}`"
          @click="sync.deleteRecord('Relation', entry.relation.id)"
        >
          <X class="size-3.5" aria-hidden="true" />
        </button>
      </li>
    </ul>
  </section>
</template>
