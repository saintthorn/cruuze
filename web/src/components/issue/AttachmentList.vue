<script setup lang="ts">
import { Download, Paperclip, Trash2 } from 'lucide-vue-next'
import { computed, ref, useTemplateRef } from 'vue'

import { errorMessage } from '@/api/client'
import { useSyncStore } from '@/stores/sync'
import { useToastStore } from '@/stores/toasts'
import { useWorkspaceStore } from '@/stores/workspace'
import type { Attachment, Issue } from '@/sync/types'

const props = defineProps<{ issue: Issue }>()

const sync = useSyncStore()
const toasts = useToastStore()
const workspace = useWorkspaceStore()

const attachments = computed(() => sync.attachmentsForIssue(props.issue.id))
const fileInput = useTemplateRef('fileInput')
const uploading = ref(0)

// The server has to know the issue before it can take files for it.
const saved = computed(() => props.issue.number > 0)

function size(bytes: number): string {
  if (bytes < 1024) return `${bytes} B`
  if (bytes < 1024 * 1024) return `${Math.round(bytes / 1024)} KB`
  return `${(bytes / (1024 * 1024)).toFixed(1)} MB`
}

async function upload(files: FileList | null) {
  for (const file of files ?? []) {
    uploading.value++
    try {
      await sync.uploadAttachment(props.issue.id, file)
    } catch (err) {
      toasts.show(`Couldn't upload ${file.name}: ${errorMessage(err)}`)
    } finally {
      uploading.value--
    }
  }
  if (fileInput.value) fileInput.value.value = ''
}

async function remove(attachment: Attachment) {
  if (!window.confirm(`Delete ${attachment.filename}?`)) return
  try {
    await sync.deleteAttachment(attachment.id)
  } catch (err) {
    toasts.show(errorMessage(err))
  }
}

const canDelete = (attachment: Attachment) => attachment.uploaderId === sync.currentUserId() || workspace.isAdmin
</script>

<template>
  <section aria-labelledby="attachments-heading" class="flex flex-col gap-2">
    <div class="flex items-center gap-2">
      <h2 id="attachments-heading" class="font-medium">Attachments</h2>
      <span v-if="attachments.length > 0" class="text-faint">{{ attachments.length }}</span>
      <input ref="fileInput" type="file" multiple class="sr-only" aria-label="Attach files" @change="upload(fileInput?.files ?? null)" />
      <button
        type="button"
        class="ml-auto flex h-7 items-center gap-1.5 rounded-md px-2 text-xs text-muted hover:bg-hover hover:text-fg disabled:opacity-50"
        :disabled="!saved || uploading > 0"
        :title="saved ? 'Up to 25 MB' : 'Save the issue first'"
        @click="fileInput?.click()"
      >
        <Paperclip class="size-3.5" aria-hidden="true" /> {{ uploading > 0 ? 'Uploading…' : 'Attach' }}
      </button>
    </div>
    <ul v-if="attachments.length > 0" class="divide-y divide-border rounded-lg border border-border">
      <li v-for="attachment in attachments" :key="attachment.id" class="flex h-10 items-center gap-2 px-3">
        <Paperclip class="size-3.5 shrink-0 text-faint" aria-hidden="true" />
        <a
          :href="`/api/attachments/${attachment.id}`"
          target="_blank"
          rel="noopener"
          class="min-w-0 flex-1 truncate hover:underline"
        >
          {{ attachment.filename }}
        </a>
        <span class="shrink-0 text-xs text-faint">{{ size(attachment.size) }}</span>
        <a
          :href="`/api/attachments/${attachment.id}`"
          :download="attachment.filename"
          class="flex size-6 shrink-0 items-center justify-center rounded text-faint hover:bg-hover hover:text-fg"
          :aria-label="`Download ${attachment.filename}`"
        >
          <Download class="size-3.5" aria-hidden="true" />
        </a>
        <button
          v-if="canDelete(attachment)"
          type="button"
          class="flex size-6 shrink-0 items-center justify-center rounded text-faint hover:bg-hover hover:text-danger"
          :aria-label="`Delete ${attachment.filename}`"
          @click="remove(attachment)"
        >
          <Trash2 class="size-3.5" aria-hidden="true" />
        </button>
      </li>
    </ul>
  </section>
</template>
