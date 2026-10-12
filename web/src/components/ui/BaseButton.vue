<script setup lang="ts">
withDefaults(
  defineProps<{
    variant?: 'primary' | 'secondary' | 'ghost' | 'danger'
    size?: 'sm' | 'md' | 'lg'
    type?: 'button' | 'submit'
    loading?: boolean
    disabled?: boolean
  }>(),
  { variant: 'secondary', size: 'md', type: 'button' },
)
</script>

<template>
  <button
    :type="type"
    :disabled="disabled || loading"
    :aria-busy="loading || undefined"
    class="inline-flex shrink-0 items-center justify-center gap-1.5 rounded-md font-medium whitespace-nowrap transition-colors disabled:opacity-50"
    :class="[
      {
        primary: 'bg-accent text-accent-fg hover:bg-accent-hover',
        secondary: 'border border-border-strong bg-elevated text-fg hover:bg-hover',
        ghost: 'text-muted hover:bg-hover hover:text-fg',
        danger: 'border border-border-strong bg-elevated text-danger hover:bg-hover',
      }[variant],
      { sm: 'h-7 px-2 text-xs', md: 'h-8 px-3 text-[13px]', lg: 'h-10 px-4 text-sm' }[size],
    ]"
  >
    <slot />
  </button>
</template>
