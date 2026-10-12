<script setup lang="ts">
import BrandMark from '@/components/BrandMark.vue'

defineProps<{ title: string; subtitle?: string }>()
</script>

<template>
  <main class="flex min-h-full items-center justify-center px-6 py-12">
    <div class="w-full max-w-[380px] border border-border-strong bg-elevated">
      <p class="border-b border-border-strong px-3 py-1 text-[11px] text-faint" aria-hidden="true">~/cruuze/auth</p>
      <div class="px-5 py-6">
      <BrandMark class="mx-auto mb-6 size-10" />
      <h1 class="text-center text-xl font-semibold tracking-tight">{{ title }}</h1>
      <p v-if="subtitle" class="mt-2 text-center text-sm text-muted">{{ subtitle }}</p>
      <div class="mt-8">
        <slot />
      </div>
      </div>
    </div>
  </main>
</template>
