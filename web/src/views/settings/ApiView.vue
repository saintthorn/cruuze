<script setup lang="ts">
import ApiKeys from '@/components/settings/ApiKeys.vue'
import Webhooks from '@/components/settings/Webhooks.vue'
import SettingsPage from '@/components/SettingsPage.vue'
import { useWorkspaceStore } from '@/stores/workspace'

const workspace = useWorkspaceStore()
</script>

<template>
  <SettingsPage title="API">
    <ApiKeys />
    <Webhooks v-if="workspace.isAdmin" />
    <section v-else aria-labelledby="webhooks-heading" class="rounded-lg border border-border p-4">
      <h2 id="webhooks-heading" class="font-medium">Webhooks</h2>
      <p class="mt-0.5 text-muted">Admins only.</p>
    </section>
  </SettingsPage>
</template>
