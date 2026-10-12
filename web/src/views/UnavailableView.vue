<script setup lang="ts">
import { useRoute } from 'vue-router'

import AuthShell from '@/components/AuthShell.vue'
import BaseButton from '@/components/ui/BaseButton.vue'

const route = useRoute()

function retry() {
  // Only follow same-origin paths, never an attacker-supplied absolute URL.
  const next = typeof route.query.next === 'string' ? route.query.next : '/'
  window.location.assign(next.startsWith('/') && !next.startsWith('//') ? next : '/')
}
</script>

<template>
  <AuthShell title="Can't reach Cruuze" subtitle="The server isn't responding.">
    <BaseButton variant="primary" size="lg" class="w-full" @click="retry">Retry</BaseButton>
  </AuthShell>
</template>
