<script setup lang="ts">
import { Image } from '@tiptap/extension-image'
import { Placeholder } from '@tiptap/extensions'
import { Markdown } from '@tiptap/markdown'
import { StarterKit } from '@tiptap/starter-kit'
import { EditorContent, useEditor } from '@tiptap/vue-3'
import { onBeforeUnmount, ref, watch } from 'vue'

import UserAvatar from '@/components/UserAvatar.vue'
import { useSyncStore } from '@/stores/sync'
import { useToastStore } from '@/stores/toasts'

import { MENTION_PREFIX, mentionExtension, type MentionPopup } from './mentions'

const props = withDefaults(
  defineProps<{
    /** Markdown. */
    modelValue: string
    /** Accessible name of the text box. */
    label: string
    placeholder?: string
    editable?: boolean
    /** Stores a pasted or dropped file and resolves to where it can be fetched. */
    upload?: (file: File) => Promise<{ url: string; name: string; image: boolean }>
  }>(),
  { editable: true },
)

const emit = defineEmits<{
  'update:modelValue': [markdown: string]
  focus: []
  blur: []
  /** ⌘/Ctrl+Enter. */
  submit: []
}>()

const sync = useSyncStore()
const toasts = useToastStore()
const mentions = ref<MentionPopup | null>(null)
/** The Markdown this editor last reported, to tell our own changes from outside ones. */
let lastEmitted = props.modelValue

const editor = useEditor({
  content: props.modelValue,
  contentType: 'markdown',
  editable: props.editable,
  extensions: [
    StarterKit.configure({
      // Underline has no Markdown form, so it wouldn't survive being saved.
      underline: false,
      link: {
        openOnClick: false,
        protocols: ['mention'],
        HTMLAttributes: { rel: 'noopener noreferrer nofollow', target: '_blank' },
      },
    }),
    Markdown,
    Image.configure({ HTMLAttributes: { loading: 'lazy' } }),
    Placeholder.configure({ placeholder: () => props.placeholder ?? '' }),
    mentionExtension(() => sync.users, mentions),
  ],
  editorProps: {
    attributes: { role: 'textbox', 'aria-multiline': 'true', 'aria-label': props.label },
    handleKeyDown(_view, event) {
      if (event.key === 'Enter' && (event.metaKey || event.ctrlKey)) {
        emit('submit')
        return true
      }
      return false
    },
    handlePaste(_view, event) {
      return insertFiles(event.clipboardData?.files)
    },
    handleDrop(_view, event) {
      return insertFiles((event as DragEvent).dataTransfer?.files)
    },
  },
  onUpdate: ({ editor }) => {
    // The editor keeps an empty paragraph at the end; don't store it.
    lastEmitted = editor.getMarkdown().trimEnd()
    emit('update:modelValue', lastEmitted)
  },
  onFocus: () => emit('focus'),
  onBlur: () => emit('blur'),
})

/** Uploads files and inserts each as an image or a link. Returns whether it took over. */
function insertFiles(files: FileList | undefined | null): boolean {
  if (!props.upload || !files || files.length === 0) return false
  for (const file of files) {
    props.upload(file).then(
      ({ url, name, image }) => {
        const chain = editor.value?.chain().focus()
        if (image) chain?.setImage({ src: url, alt: name }).run()
        else chain?.insertContent([{ type: 'text', text: name, marks: [{ type: 'link', attrs: { href: url } }] }, { type: 'text', text: ' ' }]).run()
      },
      () => toasts.show(`Couldn't upload ${file.name}`),
    )
  }
  return true
}

// Take changes that come from outside (another person's edit, the parent
// clearing the box after posting) while ignoring the echo of our own typing.
watch(
  () => props.modelValue,
  (value) => {
    if (!editor.value || value === lastEmitted) return
    lastEmitted = value
    editor.value.commands.setContent(value, { contentType: 'markdown', emitUpdate: false })
  },
)
watch(
  () => props.editable,
  (editable) => editor.value?.setEditable(editable),
)

onBeforeUnmount(() => editor.value?.destroy())

// Mentions are links to people, not places to go.
function onClick(event: MouseEvent) {
  const link = (event.target as HTMLElement).closest('a')
  if (link?.getAttribute('href')?.startsWith(MENTION_PREFIX)) event.preventDefault()
}

defineExpose({
  focus: () => editor.value?.commands.focus('end'),
  /** Inserts uploaded files at the cursor, as paste and drop do. */
  insertFiles,
})
</script>

<template>
  <div class="rich-text relative" @click="onClick">
    <EditorContent :editor="editor" />
    <ul
      v-if="mentions && mentions.items.length > 0 && mentions.rect"
      role="listbox"
      aria-label="Mention someone"
      class="fixed z-50 min-w-48 rounded-lg border border-border-strong bg-elevated p-1 shadow-xl"
      :style="{ left: `${mentions.rect.left}px`, top: `${mentions.rect.bottom + 4}px` }"
    >
      <li
        v-for="(user, index) in mentions.items"
        :key="user.id"
        role="option"
        :aria-selected="index === mentions.index"
        class="flex h-8 cursor-pointer items-center gap-2 rounded px-2 text-[13px] aria-selected:bg-active"
        @mousedown.prevent="mentions.choose(user)"
      >
        <UserAvatar :name="user.name" :email="user.email" :avatar-url="user.avatarUrl" :size="18" />
        <span class="truncate">{{ user.name || user.email }}</span>
      </li>
    </ul>
  </div>
</template>
