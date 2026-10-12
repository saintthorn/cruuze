<script setup lang="ts">
import { SmilePlus } from 'lucide-vue-next'
import { computed, ref } from 'vue'

import RichText from '@/components/editor/LazyRichText.vue'
import PickerMenu from '@/components/PickerMenu.vue'
import BaseButton from '@/components/ui/BaseButton.vue'
import UserAvatar from '@/components/UserAvatar.vue'
import { fullDateTime, timeAgo } from '@/lib/time'
import { useSyncStore } from '@/stores/sync'
import { useWorkspaceStore } from '@/stores/workspace'
import type { Comment } from '@/sync/types'

const props = defineProps<{ comment: Comment }>()

const sync = useSyncStore()
const workspace = useWorkspaceStore()

/** The reactions the server accepts. */
const EMOJI = ['👍', '👎', '❤️', '🎉', '😄', '👀', '🚀']
const emojiOptions = EMOJI.map((emoji) => ({ key: emoji, label: emoji }))

const author = computed(() => sync.user(props.comment.userId))
const authorName = computed(() => (author.value ? author.value.name || author.value.email : 'Deleted user'))
const mine = computed(() => props.comment.userId === sync.currentUserId())
const canDelete = computed(() => mine.value || workspace.isAdmin)

// One chip per emoji, with who reacted.
const reactions = computed(() => {
  const byEmoji = new Map<string, string[]>()
  for (const reaction of sync.reactionsForComment(props.comment.id)) {
    byEmoji.set(reaction.emoji, [...(byEmoji.get(reaction.emoji) ?? []), reaction.userId])
  }
  return [...byEmoji.entries()].map(([emoji, userIds]) => ({
    emoji,
    count: userIds.length,
    mine: userIds.includes(sync.currentUserId()),
    names: userIds.map((id) => sync.user(id)?.name || 'Someone').join(', '),
  }))
})
const myEmoji = computed(() => reactions.value.filter((r) => r.mine).map((r) => r.emoji))

const editing = ref(false)
const draft = ref('')

function startEditing() {
  draft.value = props.comment.body
  editing.value = true
}

function save() {
  if (draft.value.trim() && draft.value !== props.comment.body) sync.updateComment(props.comment.id, draft.value)
  editing.value = false
}

function remove() {
  if (window.confirm('Delete this comment?')) sync.deleteComment(props.comment.id)
}
</script>

<template>
  <article class="rounded-lg border border-border bg-elevated" :aria-label="`Comment by ${authorName}`">
    <header class="flex items-center gap-2 px-4 pt-3 text-xs">
      <UserAvatar :name="author?.name ?? ''" :email="author?.email" :avatar-url="author?.avatarUrl" :size="20" />
      <span class="font-medium text-fg">{{ authorName }}</span>
      <time class="text-faint" :datetime="comment.createdAt" :title="fullDateTime(comment.createdAt)">
        {{ timeAgo(comment.createdAt) }}
      </time>
      <span v-if="comment.editedAt" class="text-faint" :title="`Edited ${fullDateTime(comment.editedAt)}`">(edited)</span>
      <span class="ml-auto flex gap-1">
        <button v-if="mine && !editing" type="button" class="rounded px-1.5 py-0.5 text-faint hover:bg-hover hover:text-fg" @click="startEditing">
          Edit
        </button>
        <button v-if="canDelete" type="button" class="rounded px-1.5 py-0.5 text-faint hover:bg-hover hover:text-danger" @click="remove">
          Delete
        </button>
      </span>
    </header>

    <div class="px-4 py-2 text-[14px] leading-relaxed">
      <template v-if="editing">
        <RichText v-model="draft" label="Edit comment" class="min-h-12" @submit="save" />
        <div class="mt-2 flex justify-end gap-2">
          <BaseButton size="sm" variant="ghost" @click="editing = false">Cancel</BaseButton>
          <BaseButton size="sm" variant="primary" @click="save">Save</BaseButton>
        </div>
      </template>
      <RichText v-else :model-value="comment.body" :editable="false" label="Comment" />
    </div>

    <footer class="flex flex-wrap items-center gap-1.5 px-4 pb-3">
      <button
        v-for="reaction in reactions"
        :key="reaction.emoji"
        type="button"
        class="flex h-6 items-center gap-1 rounded-full border px-2 text-xs"
        :class="reaction.mine ? 'border-accent bg-accent/15' : 'border-border-strong hover:bg-hover'"
        :aria-pressed="reaction.mine"
        :aria-label="`${reaction.emoji} ${reaction.count}, from ${reaction.names}`"
        :title="reaction.names"
        @click="sync.toggleReaction(comment.id, reaction.emoji)"
      >
        <span aria-hidden="true">{{ reaction.emoji }}</span> {{ reaction.count }}
      </button>
      <PickerMenu
        :options="emojiOptions"
        :selected="myEmoji"
        label="Add a reaction"
        trigger-class="flex size-6 items-center justify-center rounded-full text-faint hover:bg-hover hover:text-fg"
        @select="sync.toggleReaction(comment.id, $event)"
      >
        <SmilePlus class="size-3.5" aria-hidden="true" />
      </PickerMenu>
    </footer>
  </article>
</template>
