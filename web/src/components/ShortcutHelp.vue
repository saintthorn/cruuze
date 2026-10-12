<script setup lang="ts">
import { DialogClose, DialogContent, DialogDescription, DialogOverlay, DialogPortal, DialogRoot, DialogTitle } from 'reka-ui'

import { useUiStore } from '@/stores/ui'

const ui = useUiStore()

const sections: { title: string; shortcuts: { keys: string[]; label: string }[] }[] = [
  {
    title: 'General',
    shortcuts: [
      { keys: ['⌘', 'K'], label: 'Open the command menu' },
      { keys: ['C'], label: 'New issue' },
      { keys: ['/'], label: 'Search descriptions and comments' },
      { keys: ['G', 'I'], label: 'Go to Inbox' },
      { keys: ['G', 'M'], label: 'Go to My issues' },
      { keys: ['G', 'S'], label: 'Go to settings' },
      { keys: ['?'], label: 'Show this help' },
    ],
  },
  {
    title: 'Lists and boards',
    shortcuts: [
      { keys: ['J'], label: 'Move down' },
      { keys: ['K'], label: 'Move up' },
      { keys: ['Enter'], label: 'Open the issue' },
      { keys: ['X'], label: 'Select or deselect' },
      { keys: ['Shift', 'Click'], label: 'Select a range' },
      { keys: ['⌘', 'A'], label: 'Select all' },
      { keys: ['Esc'], label: 'Clear the selection' },
      { keys: ['F'], label: 'Filter' },
      { keys: ['⌘', 'B'], label: 'Switch between list and board' },
    ],
  },
  {
    title: 'Issues (selected, under the pointer, or open)',
    shortcuts: [
      { keys: ['S'], label: 'Change status' },
      { keys: ['P'], label: 'Set priority' },
      { keys: ['A'], label: 'Assign' },
      { keys: ['L'], label: 'Add or remove labels' },
      { keys: ['⌘', '⌫'], label: 'Archive' },
    ],
  },
]
</script>

<template>
  <DialogRoot v-model:open="ui.shortcutsOpen">
    <DialogPortal>
      <DialogOverlay class="fixed inset-0 z-40 bg-black/50" />
      <DialogContent
        class="fixed top-[10%] left-1/2 z-50 max-h-[80vh] w-[calc(100%-2rem)] max-w-lg -translate-x-1/2 overflow-y-auto rounded-xl border border-border-strong bg-elevated p-5 shadow-2xl"
      >
        <div class="flex items-center justify-between">
          <DialogTitle class="text-base font-semibold">Keyboard shortcuts</DialogTitle>
          <DialogClose class="rounded px-2 py-1 text-xs text-muted hover:bg-hover hover:text-fg">Close</DialogClose>
        </div>
        <DialogDescription class="mt-1 text-xs text-faint">⌘ is Ctrl on Windows and Linux.</DialogDescription>
        <section v-for="section in sections" :key="section.title" class="mt-5">
          <h3 class="mb-1 text-xs font-medium text-faint">{{ section.title }}</h3>
          <dl>
            <div v-for="shortcut in section.shortcuts" :key="shortcut.label" class="flex h-8 items-center justify-between">
              <dt>{{ shortcut.label }}</dt>
              <dd class="flex gap-1">
                <kbd
                  v-for="key in shortcut.keys"
                  :key="key"
                  class="flex h-5 min-w-5 items-center justify-center rounded border border-border-strong px-1.5 text-[11px] text-muted"
                >
                  {{ key }}
                </kbd>
              </dd>
            </div>
          </dl>
        </section>
      </DialogContent>
    </DialogPortal>
  </DialogRoot>
</template>
