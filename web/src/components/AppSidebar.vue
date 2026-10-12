<script setup lang="ts">
import {
  Box,
  Brush,
  CalendarDays,
  ChartNoAxesColumn,
  Check,
  ChevronDown,
  CircleDot,
  CircleUser,
  Inbox,
  Layers,
  ListChecks,
  LogOut,
  MessageSquareQuote,
  Plus,
  RefreshCw,
  Search,
  Settings,
  SquarePen,
  Target,
  Star,
  Users,
} from 'lucide-vue-next'
import {
  DropdownMenuContent,
  DropdownMenuItem,
  DropdownMenuLabel,
  DropdownMenuPortal,
  DropdownMenuRoot,
  DropdownMenuSeparator,
  DropdownMenuTrigger,
} from 'reka-ui'
import { computed, ref } from 'vue'

import { kindInfo, plansByDate } from '@/lib/teamKinds'
import { type RouteLocationRaw, RouterLink, useRouter } from 'vue-router'

import { usePaletteStore } from '@/stores/palette'
import { useSessionStore } from '@/stores/session'
import { useSyncStore } from '@/stores/sync'
import { type TeamView, useWorkspaceStore } from '@/stores/workspace'
import { issueIdentifier } from '@/lib/issues'
import { currentTheme } from '@/lib/themes'

import BrandMark from './BrandMark.vue'
import CreateTeamDialog from './CreateTeamDialog.vue'
import SyncLog from './hacker/SyncLog.vue'
import SyncStatus from './SyncStatus.vue'
import UserAvatar from './UserAvatar.vue'

const router = useRouter()
const session = useSessionStore()
const workspace = useWorkspaceStore()
const palette = usePaletteStore()
const sync = useSyncStore()

defineEmits<{ newIssue: [] }>()

const createTeamOpen = ref(false)

// Favorites whose target is gone (deleted, or no longer visible) are left out.
const favorites = computed(() =>
  sync.favorites.flatMap((favorite): { id: string; label: string; to: RouteLocationRaw }[] => {
    const { id, kind, targetId } = favorite
    if (kind === 'issue') {
      const issue = sync.issue(targetId)
      if (!issue) return []
      const identifier = issueIdentifier(issue, sync.team(issue.teamId))
      return [{ id, label: `${identifier} ${issue.title}`, to: { name: 'issue', params: { issueRef: issue.number > 0 ? identifier : issue.id } } }]
    }
    if (kind === 'project') {
      const project = sync.project(targetId)
      return project ? [{ id, label: project.name, to: { name: 'project', params: { projectId: project.id } } }] : []
    }
    if (kind === 'view') {
      const view = sync.view(targetId)
      return view ? [{ id, label: view.name, to: { name: 'view', params: { viewId: view.id } } }] : []
    }
    const cycle = sync.cycle(targetId)
    const team = cycle && sync.team(cycle.teamId)
    return cycle && team
      ? [{ id, label: `${team.key} ${sync.cycleName(cycle)}`, to: { name: 'cycle', params: { teamKey: team.key, number: cycle.number } } }]
      : []
  }),
)

async function logout() {
  await session.logout()
  // Signing out removes this workspace's data from the browser.
  await workspace.close(true)
  await router.push({ name: 'home' })
}

function onTeamCreated(team: TeamView) {
  router.push({
    name: 'team-issues',
    params: { workspaceKey: workspace.current!.urlKey, teamKey: team.key },
  })
}

const itemClass =
  'flex h-8 items-center gap-2 rounded px-2 text-[13px] outline-none select-none data-[highlighted]:bg-active'
const navClass =
  'flex h-8 items-center gap-2.5 rounded-md px-2.5 text-muted transition-colors duration-150 hover:bg-hover hover:text-fg'
const headingClass = 'text-[11px] font-semibold tracking-[0.08em] text-faint uppercase'
</script>

<template>
  <nav v-if="workspace.current" aria-label="Workspace" class="flex w-[232px] shrink-0 flex-col gap-4 overflow-y-auto border-r border-border bg-sidebar p-3 *:shrink-0">
    <div class="flex items-center gap-1">
    <DropdownMenuRoot>
      <DropdownMenuTrigger
        class="flex h-9 min-w-0 items-center gap-2.5 rounded-md px-1.5 text-[15px] font-semibold tracking-tight hover:bg-hover"
      >
        <BrandMark class="size-6 shrink-0" />
        <span class="truncate">{{ workspace.current.name }}</span>
        <ChevronDown class="size-4 shrink-0 text-faint" aria-hidden="true" />
      </DropdownMenuTrigger>
      <DropdownMenuPortal>
        <DropdownMenuContent
          align="start"
          :side-offset="4"
          class="z-50 w-64 rounded-lg border border-border-strong bg-elevated p-1 shadow-xl"
        >
          <DropdownMenuLabel class="flex items-center gap-2 px-2 py-1.5">
            <UserAvatar
              :name="session.user?.name ?? ''"
              :email="session.user?.email"
              :avatar-url="session.user?.avatarUrl"
              :size="24"
            />
            <span class="min-w-0">
              <span class="block truncate font-medium">{{ session.user?.name }}</span>
              <span class="block truncate text-xs text-faint">{{ session.user?.email }}</span>
            </span>
          </DropdownMenuLabel>
          <DropdownMenuSeparator class="my-1 h-px bg-border" />
          <DropdownMenuItem as-child>
            <RouterLink :to="{ name: 'settings-members' }" :class="itemClass">
              <Settings class="size-4 text-muted" aria-hidden="true" /> Settings
            </RouterLink>
          </DropdownMenuItem>
          <DropdownMenuSeparator class="my-1 h-px bg-border" />
          <DropdownMenuLabel class="px-2 py-1 text-xs text-faint">Workspaces</DropdownMenuLabel>
          <DropdownMenuItem v-for="w in session.workspaces" :key="w.id" as-child>
            <RouterLink :to="{ name: 'workspace', params: { workspaceKey: w.urlKey } }" :class="itemClass">
              <span class="truncate">{{ w.name }}</span>
              <Check v-if="w.id === workspace.current.id" class="ml-auto size-4 text-muted" aria-label="Current" />
            </RouterLink>
          </DropdownMenuItem>
          <DropdownMenuItem as-child>
            <RouterLink :to="{ name: 'join' }" :class="itemClass">
              <Plus class="size-4 text-muted" aria-hidden="true" /> Create workspace
            </RouterLink>
          </DropdownMenuItem>
          <DropdownMenuSeparator class="my-1 h-px bg-border" />
          <DropdownMenuItem :class="itemClass" @select="logout">
            <LogOut class="size-4 text-muted" aria-hidden="true" /> Log out
          </DropdownMenuItem>
        </DropdownMenuContent>
      </DropdownMenuPortal>
    </DropdownMenuRoot>
      <button
        v-if="workspace.teams.length > 0"
        type="button"
        class="ml-auto flex size-7 shrink-0 items-center justify-center rounded-md border border-border-strong bg-elevated text-muted hover:bg-hover hover:text-fg"
        aria-label="New issue"
        title="New issue (C)"
        @click="$emit('newIssue')"
      >
        <SquarePen class="size-4" aria-hidden="true" />
      </button>
    </div>

    <p v-if="currentTheme === 'hacker'" class="-mt-3 -mb-2 truncate px-2 text-[11px] text-faint" aria-hidden="true">
      {{ (session.user?.email ?? 'anon').split('@')[0] }}@{{ workspace.current.urlKey }}:~$
    </p>

    <div class="flex flex-col gap-0.5">
      <button type="button" :class="navClass" class="w-full" @click="palette.show()">
        <Search class="size-4" aria-hidden="true" /> Search
        <kbd class="ml-auto text-[11px] text-faint">⌘K</kbd>
      </button>
      <RouterLink :to="{ name: 'focus' }" :class="navClass" active-class="!bg-active !text-fg">
        <Target class="size-4" aria-hidden="true" /> Focus
      </RouterLink>
      <RouterLink :to="{ name: 'inbox' }" :class="navClass" active-class="!bg-active !text-fg">
        <Inbox class="size-4" aria-hidden="true" /> Inbox
        <span
          v-if="sync.unreadCount > 0"
          class="ml-auto rounded-full bg-accent px-1.5 text-[10px] font-medium text-accent-fg"
          :aria-label="`${sync.unreadCount} unread`"
        >
          {{ sync.unreadCount }}
        </span>
      </RouterLink>
      <RouterLink :to="{ name: 'my-issues' }" :class="navClass" active-class="!bg-active !text-fg">
        <CircleUser class="size-4" aria-hidden="true" /> My issues
      </RouterLink>
      <RouterLink :to="{ name: 'projects' }" :class="navClass" active-class="!bg-active !text-fg">
        <Box class="size-4" aria-hidden="true" /> Projects
      </RouterLink>
      <RouterLink
        v-if="workspace.canManageLabels && (workspace.current?.portalEnabled || sync.customerRequests.length > 0)"
        :to="{ name: 'requests' }"
        :class="navClass"
        active-class="!bg-active !text-fg"
      >
        <MessageSquareQuote class="size-4" aria-hidden="true" /> Requests
        <span
          v-if="sync.newRequestCount > 0"
          class="ml-auto rounded-full bg-accent px-1.5 text-[10px] font-medium text-accent-fg"
          :aria-label="`${sync.newRequestCount} new`"
        >
          {{ sync.newRequestCount }}
        </span>
      </RouterLink>
    </div>

    <section v-if="favorites.length > 0" aria-labelledby="sidebar-favorites">
      <h2 id="sidebar-favorites" class="flex h-7 items-center px-2.5" :class="headingClass">Favorites</h2>
      <ul class="flex flex-col gap-0.5">
        <li v-for="favorite in favorites" :key="favorite.id">
          <RouterLink :to="favorite.to" :class="navClass" active-class="!bg-active !text-fg">
            <Star class="size-4 shrink-0" aria-hidden="true" /> <span class="truncate">{{ favorite.label }}</span>
          </RouterLink>
        </li>
      </ul>
    </section>

    <section v-if="sync.views.length > 0" aria-labelledby="sidebar-views">
      <h2 id="sidebar-views" class="flex h-7 items-center px-2.5" :class="headingClass">Views</h2>
      <ul class="flex flex-col gap-0.5">
        <li v-for="view in sync.views" :key="view.id">
          <RouterLink :to="{ name: 'view', params: { viewId: view.id } }" :class="navClass" active-class="!bg-active !text-fg">
            <Layers class="size-4 shrink-0" aria-hidden="true" /> <span class="truncate">{{ view.name }}</span>
          </RouterLink>
        </li>
      </ul>
    </section>

    <section aria-labelledby="sidebar-teams">
      <div class="flex h-7 items-center justify-between px-2.5">
        <h2 id="sidebar-teams" :class="headingClass">Your teams</h2>
        <button
          v-if="workspace.canCreateTeams"
          type="button"
          class="flex size-6 items-center justify-center rounded text-faint hover:bg-hover hover:text-fg"
          aria-label="Create team"
          @click="createTeamOpen = true"
        >
          <Plus class="size-4" aria-hidden="true" />
        </button>
      </div>

      <ul class="flex flex-col gap-0.5">
        <li v-for="team in workspace.myTeams" :key="team.id">
          <div class="mt-1 flex h-8 items-center gap-2.5 px-2.5 font-medium">
            <span
              class="flex size-[18px] shrink-0 items-center justify-center rounded-sm bg-active text-[9px] font-semibold text-muted"
              aria-hidden="true"
            >
              {{ team.key[0] }}
            </span>
            <span class="truncate">{{ team.name }}</span>
          </div>
          <RouterLink
            :to="{ name: 'team-issues', params: { teamKey: team.key } }"
            :class="navClass"
            class="ml-4"
            active-class="!bg-active !text-fg"
          >
            <CircleDot class="size-4" aria-hidden="true" /> {{ kindInfo(team.kind).noun }}
          </RouterLink>
          <RouterLink
            v-if="plansByDate(team.kind)"
            :to="{ name: 'calendar', params: { teamKey: team.key } }"
            :class="navClass"
            class="ml-4"
            active-class="!bg-active !text-fg"
          >
            <CalendarDays class="size-4" aria-hidden="true" /> Calendar
          </RouterLink>
          <RouterLink
            v-if="team.cycleWeeks > 0"
            :to="{ name: 'cycles', params: { teamKey: team.key } }"
            :class="navClass"
            class="ml-4"
            active-class="!bg-active !text-fg"
          >
            <RefreshCw class="size-4" aria-hidden="true" /> Cycles
          </RouterLink>
          <RouterLink
            v-if="team.triageEnabled"
            :to="{ name: 'triage', params: { teamKey: team.key } }"
            :class="navClass"
            class="ml-4"
            active-class="!bg-active !text-fg"
          >
            <ListChecks class="size-4" aria-hidden="true" /> Triage
            <span v-if="sync.triageIssues(team.id).length > 0" class="ml-auto text-[11px] text-faint">
              {{ sync.triageIssues(team.id).length }}
            </span>
          </RouterLink>
          <RouterLink
            :to="{ name: 'insights', params: { teamKey: team.key } }"
            :class="navClass"
            class="ml-4"
            active-class="!bg-active !text-fg"
          >
            <ChartNoAxesColumn class="size-4" aria-hidden="true" /> Insights
          </RouterLink>
          <RouterLink
            v-if="sync.hygiene.get(team.id)?.length"
            :to="{ name: 'cleanup', params: { teamKey: team.key } }"
            :class="navClass"
            class="ml-4"
            active-class="!bg-active !text-fg"
          >
            <Brush class="size-4" aria-hidden="true" /> Cleanup
            <span class="ml-auto text-[11px] text-faint">{{ sync.hygiene.get(team.id)?.length }}</span>
          </RouterLink>
        </li>
      </ul>
      <p v-if="workspace.myTeams.length === 0" class="px-2 py-1 text-xs text-faint">
        You're not in any teams yet.
      </p>

      <RouterLink
        v-if="workspace.teams.length > workspace.myTeams.length"
        :to="{ name: 'settings-teams' }"
        :class="navClass"
        class="mt-1"
      >
        <Users class="size-4" aria-hidden="true" /> Browse teams
      </RouterLink>
    </section>

    <div class="mt-auto flex flex-col gap-2">
      <SyncLog v-if="currentTheme === 'hacker'" />
      <RouterLink
        :to="{ name: 'settings-account' }"
        class="flex items-center gap-2.5 rounded-lg border border-border p-2.5 transition-colors hover:border-border-strong hover:bg-hover"
      >
        <UserAvatar :name="session.user?.name ?? ''" :email="session.user?.email" :avatar-url="session.user?.avatarUrl" :size="26" />
        <span class="min-w-0">
          <span class="block truncate font-medium">{{ session.user?.name || session.user?.email }}</span>
          <span v-if="sync.status === 'live' && sync.pendingCount === 0" class="flex items-center gap-1.5 text-xs text-success">
            <span class="size-1.5 rounded-full bg-success" aria-hidden="true" /> Live
          </span>
        </span>
      </RouterLink>
      <SyncStatus />
    </div>

    <CreateTeamDialog v-model:open="createTeamOpen" @created="onTeamCreated" />
  </nav>
</template>
