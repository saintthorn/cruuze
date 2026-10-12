<script setup lang="ts">
import { Download, Upload } from 'lucide-vue-next'
import { ref, useTemplateRef } from 'vue'

import { api, errorMessage, unwrap } from '@/api/client'
import type { components } from '@/api/schema'
import BaseButton from '@/components/ui/BaseButton.vue'
import FormError from '@/components/ui/FormError.vue'
import { issuesToCsv } from '@/lib/csv'
import { issueIdentifier, sortIssues } from '@/lib/issues'
import { useSyncStore } from '@/stores/sync'
import type { TeamView } from '@/stores/workspace'

type ImportResult = components['schemas']['ImportResult']

/** The server's limit; checked here first to fail before uploading. */
const MAX_BYTES = 5 * 1024 * 1024

const props = defineProps<{ team: TeamView }>()

const sync = useSyncStore()
const fileInput = useTemplateRef('fileInput')
const importing = ref(false)
const error = ref('')
const result = ref<ImportResult | null>(null)

async function importFile(file: File | undefined) {
  if (!file) return
  error.value = ''
  result.value = null
  if (file.size > MAX_BYTES) {
    error.value = 'The file is too large. The limit is 5 MB.'
  } else {
    importing.value = true
    try {
      result.value = await unwrap(
        api.POST('/teams/{teamId}/import', {
          params: { path: { teamId: props.team.id } },
          body: await file.text(),
          bodySerializer: (body) => body,
          headers: { 'Content-Type': 'text/csv' },
        }),
      )
    } catch (err) {
      error.value = errorMessage(err)
    } finally {
      importing.value = false
    }
  }
  if (fileInput.value) fileInput.value.value = ''
}

function exportIssues() {
  const issues = sortIssues(sync.issuesForTeam(props.team.id), 'created')
  const csv = issuesToCsv(issues, {
    identifier: (issue) => issueIdentifier(issue, props.team),
    state: sync.state,
    user: sync.user,
    label: sync.label,
  })
  // The byte order mark makes Excel read the file as UTF-8.
  const url = URL.createObjectURL(new Blob(['﻿', csv], { type: 'text/csv;charset=utf-8' }))
  const link = document.createElement('a')
  link.href = url
  link.download = `${props.team.key.toLowerCase()}-issues.csv`
  link.click()
  URL.revokeObjectURL(url)
}
</script>

<template>
  <section aria-labelledby="import-heading" class="rounded-lg border border-border p-4">
    <h2 id="import-heading" class="font-medium">Import and export</h2>
    <p class="mt-0.5 text-muted">CSV. A <span class="text-fg">Title</span> column is required.</p>
    <div class="mt-3 flex flex-wrap gap-2">
      <input
        ref="fileInput"
        type="file"
        accept=".csv,text/csv"
        class="sr-only"
        aria-label="CSV file to import"
        @change="importFile(fileInput?.files?.[0])"
      />
      <BaseButton :loading="importing" @click="fileInput?.click()">
        <Upload class="size-3.5" aria-hidden="true" /> {{ importing ? 'Importing…' : 'Import CSV' }}
      </BaseButton>
      <BaseButton @click="exportIssues"><Download class="size-3.5" aria-hidden="true" /> Export CSV</BaseButton>
    </div>
    <FormError :message="error" class="mt-2" />
    <div v-if="result" role="status" class="mt-3 rounded-md border border-border bg-bg p-3">
      <p class="font-medium">
        Imported {{ result.created === 1 ? '1 issue' : `${result.created} issues` }}<template v-if="result.labelsCreated > 0">
          and created {{ result.labelsCreated === 1 ? '1 label' : `${result.labelsCreated} labels` }}</template>.
      </p>
      <template v-if="result.skipped.length > 0">
        <p class="mt-1 text-muted">{{ result.skipped.length === 1 ? '1 row was' : `${result.skipped.length} rows were` }} skipped:</p>
        <ul class="mt-1 max-h-40 overflow-y-auto text-xs text-muted">
          <li v-for="row in result.skipped" :key="row.row">Row {{ row.row }}: {{ row.reason }}</li>
        </ul>
      </template>
    </div>
  </section>
</template>
