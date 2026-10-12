<script setup lang="ts">
import { Box, Plus } from 'lucide-vue-next'
import { ref } from 'vue'
import { RouterLink, useRouter } from 'vue-router'

import ProgressBar from '@/components/ProgressBar.vue'
import PromptDialog from '@/components/PromptDialog.vue'
import BaseButton from '@/components/ui/BaseButton.vue'
import UserAvatar from '@/components/UserAvatar.vue'
import { formatDueDate, progressOf, projectStatusLabel } from '@/lib/issues'
import { useSyncStore } from '@/stores/sync'
import { useWorkspaceStore } from '@/stores/workspace'
import type { Project } from '@/sync/types'

const router = useRouter()
const sync = useSyncStore()
const workspace = useWorkspaceStore()

const createOpen = ref(false)

function create(name: string) {
  const project = sync.createProject(name)
  void router.push({ name: 'project', params: { projectId: project.id } })
}

const progress = (project: Project) => progressOf(sync.issues.filter((i) => i.projectId === project.id), sync.state)
</script>

<template>
  <header class="flex h-11 shrink-0 items-center gap-2 border-b border-border px-5">
    <h1 class="font-medium">Projects</h1>
    <span class="text-faint">{{ sync.projects.length }}</span>
    <div class="ml-auto flex items-center gap-2">
      <RouterLink
        :to="{ name: 'roadmap' }"
        class="inline-flex h-7 items-center rounded-md border border-border-strong bg-elevated px-2 text-xs font-medium hover:bg-hover"
      >
        Roadmap
      </RouterLink>
      <BaseButton v-if="workspace.canManageLabels" size="sm" @click="createOpen = true">
        <Plus class="size-3.5" aria-hidden="true" /> New project
      </BaseButton>
    </div>
  </header>

  <ul v-if="sync.projects.length > 0" class="flex-1 overflow-y-auto" aria-label="Projects">
    <li v-for="project in sync.projects" :key="project.id" class="border-b border-border">
      <RouterLink :to="{ name: 'project', params: { projectId: project.id } }" class="flex h-12 items-center gap-3 px-5 hover:bg-hover">
        <span class="size-3 shrink-0 rounded-sm" :style="{ background: project.color }" aria-hidden="true" />
        <span class="min-w-0 flex-1 truncate font-medium">{{ project.name }}</span>
        <span class="hidden w-24 shrink-0 text-xs text-muted sm:block">{{ projectStatusLabel(project.status) }}</span>
        <span class="hidden w-20 shrink-0 text-xs text-muted md:block">
          {{ project.targetDate ? formatDueDate(project.targetDate) : '' }}
        </span>
        <ProgressBar :progress="progress(project)" class="hidden shrink-0 sm:flex" />
        <UserAvatar
          v-if="sync.user(project.leadId)"
          :name="sync.user(project.leadId)!.name"
          :email="sync.user(project.leadId)!.email"
          :avatar-url="sync.user(project.leadId)!.avatarUrl"
          :size="20"
        />
        <span v-else class="size-5 shrink-0" aria-hidden="true" />
      </RouterLink>
    </li>
  </ul>

  <div v-else class="flex flex-1 flex-col items-center justify-center gap-2 p-8 text-center">
    <Box class="size-8 text-faint" aria-hidden="true" />
    <p class="text-muted">No projects</p>
    <BaseButton v-if="workspace.canManageLabels" variant="primary" class="mt-2" @click="createOpen = true">New project</BaseButton>
  </div>

  <PromptDialog v-model:open="createOpen" title="New project" label="Project name" placeholder="Launch" action="Create project" @submit="create" />
</template>
