<script setup lang="ts">
import { Archive, Check } from 'lucide-vue-next'
import { computed } from 'vue'
import { RouterLink, useRoute } from 'vue-router'

import StateIcon from '@/components/StateIcon.vue'
import BaseButton from '@/components/ui/BaseButton.vue'
import { useIssueSelect } from '@/composables/useIssueSelect'
import { type HygieneFinding, type HygieneKind, STUCK_DAYS } from '@/lib/hygiene'
import { issueIdentifier } from '@/lib/issues'
import { useSyncStore } from '@/stores/sync'
import { useToastStore } from '@/stores/toasts'

const route = useRoute()
const sync = useSyncStore()
const toasts = useToastStore()
const { issueRoute } = useIssueSelect()

const team = computed(() => sync.teamByKey(String(route.params.teamKey)))
const findings = computed(() => (team.value ? (sync.hygiene.get(team.value.id) ?? []) : []))

// What to say about each kind, in the order they are worth looking at, and
// the one thing that settles a row of that kind.
const SECTIONS: { kind: HygieneKind; title: string; about: (days: number) => string; action: string; archive: boolean }[] = [
  { kind: 'blocked', title: 'Blocked', about: () => 'Waiting on an open issue.', action: 'Keep', archive: false },
  { kind: 'overdue', title: 'Overdue', about: () => 'Past due.', action: 'Push a week', archive: false },
  { kind: 'unowned', title: 'Unassigned', about: () => 'Started, nobody assigned.', action: 'Take it', archive: false },
  { kind: 'stuck', title: 'Stuck', about: () => `No activity for ${STUCK_DAYS}+ days.`, action: 'Keep', archive: false },
  {
    kind: 'stale',
    title: 'Stale',
    about: (days) => `Untouched for ${days}+ days.`,
    action: 'Keep',
    archive: true,
  },
]

/** How many rows of a section are shown; a neglected backlog can hold thousands. */
const SHOWN = 200

const sections = computed(() =>
  SECTIONS.map((section) => {
    const all = findings.value.filter((f) => f.kind === section.kind)
    return { ...section, findings: all, shown: all.slice(0, SHOWN) }
  }).filter((section) => section.findings.length > 0),
)

function reason(finding: HygieneFinding): string {
  const days = finding.days === 1 ? '1 day' : `${finding.days} days`
  return finding.kind === 'overdue' ? `${days} overdue` : `quiet for ${days}`
}

/**
 * Settles a row. What that takes depends on why it is here: an unowned
 * issue needs an owner, an overdue one a date it can meet, and the rest
 * only someone saying they are still wanted, which stops them being flagged
 * until they go quiet again.
 */
function resolve(finding: HygieneFinding) {
  const { issue, kind } = finding
  if (kind === 'unowned') {
    sync.updateIssue(issue.id, { assigneeId: sync.currentUserId() })
  } else if (kind === 'overdue') {
    const next = new Date()
    next.setUTCDate(next.getUTCDate() + 7)
    sync.updateIssue(issue.id, { dueDate: next.toISOString().slice(0, 10) })
  } else {
    sync.updateIssue(issue.id, { touch: true })
  }
}

function archive(finding: HygieneFinding) {
  sync.archiveIssue(finding.issue.id)
}

function archiveAll(list: HygieneFinding[]) {
  if (!window.confirm(`Archive ${list.length === 1 ? 'this issue' : `these ${list.length} issues`}?`)) return
  for (const finding of list) sync.archiveIssue(finding.issue.id)
  toasts.show(list.length === 1 ? 'Issue archived' : `${list.length} issues archived`)
}

// One key per decision, on the row that has focus.
function onKey(event: KeyboardEvent, finding: HygieneFinding, canArchive: boolean) {
  if (event.metaKey || event.ctrlKey || event.altKey) return
  const row = event.currentTarget as HTMLElement
  const move = (to: Element | null) => {
    event.preventDefault()
    ;(to as HTMLElement | null)?.focus()
  }
  if (event.key === 'ArrowDown' || event.key === 'j') move(row.nextElementSibling)
  else if (event.key === 'ArrowUp' || event.key === 'k') move(row.previousElementSibling)
  else if (event.key === 'e') {
    move(row.nextElementSibling ?? row.previousElementSibling)
    resolve(finding)
  } else if (event.key === '#' && canArchive) {
    move(row.nextElementSibling ?? row.previousElementSibling)
    archive(finding)
  }
}
</script>

<template>
  <template v-if="team">
    <header class="flex h-11 shrink-0 items-center gap-2 border-b border-border px-5">
      <span class="font-medium">{{ team.name }}</span>
      <span class="text-faint" aria-hidden="true">›</span>
      <h1 class="text-muted">Cleanup</h1>
      <span class="text-faint">{{ findings.length }}</span>
      <RouterLink :to="{ name: 'archived', params: { teamKey: team.key } }" class="ml-auto text-xs text-muted hover:text-fg">
        Archived issues
      </RouterLink>
    </header>

    <div v-if="sections.length > 0" class="flex-1 overflow-y-auto">
      <p class="border-b border-border px-5 py-2 text-xs text-faint">
        On a row: <kbd class="border border-border-strong px-1">E</kbd> settle it,
        <kbd class="border border-border-strong px-1">#</kbd> archive, <kbd class="border border-border-strong px-1">J</kbd>/<kbd
          class="border border-border-strong px-1"
          >K</kbd
        >
        move.
      </p>
      <section v-for="section in sections" :key="section.kind" :aria-label="section.title">
        <div class="sticky top-0 z-10 flex min-h-9 items-center gap-2 border-b border-border bg-sidebar px-5 py-1">
          <h2 class="font-medium">{{ section.title }}</h2>
          <span class="text-faint">{{ section.findings.length }}</span>
          <span class="hidden truncate text-xs text-muted md:inline">{{ section.about(team.staleDays) }}</span>
          <BaseButton v-if="section.archive" size="sm" variant="ghost" class="ml-auto" @click="archiveAll(section.findings)">
            <Archive class="size-3.5" aria-hidden="true" /> Archive all
          </BaseButton>
        </div>
        <ul>
          <li
            v-for="finding in section.shown"
            :key="finding.issue.id"
            tabindex="0"
            class="flex h-10 items-center gap-3 border-b border-border px-5 outline-none hover:bg-hover focus:bg-active"
            @keydown="onKey($event, finding, section.archive)"
          >
            <StateIcon
              v-if="sync.state(finding.issue.stateId)"
              :type="sync.state(finding.issue.stateId)!.type"
              :color="sync.state(finding.issue.stateId)!.color"
            />
            <span class="w-20 shrink-0 text-muted tabular-nums">{{ issueIdentifier(finding.issue, team) }}</span>
            <RouterLink :to="issueRoute(finding.issue)" tabindex="-1" class="min-w-0 flex-1 truncate font-medium hover:underline">
              {{ finding.issue.title }}
            </RouterLink>
            <span class="hidden shrink-0 text-xs text-faint sm:inline">{{ reason(finding) }}</span>
            <BaseButton size="sm" tabindex="-1" @click="resolve(finding)">
              <Check class="size-3.5" aria-hidden="true" /> {{ section.action }}
            </BaseButton>
            <BaseButton v-if="section.archive" size="sm" variant="ghost" tabindex="-1" @click="archive(finding)">
              <Archive class="size-3.5" aria-hidden="true" /> Archive
            </BaseButton>
          </li>
          <li v-if="section.findings.length > section.shown.length" class="border-b border-border px-5 py-2 text-xs text-faint">
            +{{ section.findings.length - section.shown.length }} more
          </li>
        </ul>
      </section>
    </div>
    <div v-else class="flex flex-1 flex-col items-center justify-center gap-2 p-8 text-center">
      <p class="text-muted">Nothing to clean up</p>
    </div>
  </template>
  <p v-else class="p-8 text-center text-muted">Team not found.</p>
</template>
