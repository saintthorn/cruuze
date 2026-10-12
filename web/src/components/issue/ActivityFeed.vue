<script setup lang="ts">
import { computed, ref } from 'vue'

import RichText from '@/components/editor/LazyRichText.vue'
import BaseButton from '@/components/ui/BaseButton.vue'
import UserAvatar from '@/components/UserAvatar.vue'
import { actorName, describeActivity } from '@/lib/activity'
import { issueIdentifier } from '@/lib/issues'
import { fullDateTime, timeAgo } from '@/lib/time'
import { useSyncStore } from '@/stores/sync'
import type { Activity, Comment, Issue } from '@/sync/types'

import CommentItem from './CommentItem.vue'

const props = defineProps<{
  issue: Issue
  /** Stores a file pasted or dropped into the comment box. */
  upload?: (file: File) => Promise<{ url: string; name: string; image: boolean }>
  /** Earlier comments and activity are still being fetched. */
  loading?: boolean
  /** Fetching them failed; it is retried when the connection returns. */
  failed?: boolean
}>()

const sync = useSyncStore()

type Entry = { kind: 'comment'; at: string; comment: Comment } | { kind: 'activity'; at: string; activity: Activity }

// Comments and changes, interleaved in the order they happened.
const entries = computed<Entry[]>(() =>
  [
    ...sync.commentsForIssue(props.issue.id).map((comment): Entry => ({ kind: 'comment', at: comment.createdAt, comment })),
    ...sync.activitiesForIssue(props.issue.id).map((activity): Entry => ({ kind: 'activity', at: activity.createdAt, activity })),
  ].sort((a, b) => a.at.localeCompare(b.at)),
)

const ctx = {
  user: sync.user,
  state: sync.state,
  label: sync.label,
  projectName: (id: string) => sync.project(id)?.name,
  milestoneName: (id: string) => sync.milestone(id)?.name,
  cycleName: (id: string) => {
    const cycle = sync.cycle(id)
    return cycle && sync.cycleName(cycle)
  },
  issueName: (id: string) => {
    const issue = sync.issue(id)
    return issue && issueIdentifier(issue, sync.team(issue.teamId))
  },
}

const draft = ref('')

function post() {
  if (!draft.value.trim()) return
  sync.createComment(props.issue.id, draft.value)
  draft.value = ''
}
</script>

<template>
  <section aria-labelledby="activity-heading" class="flex flex-col gap-3">
    <h2 id="activity-heading" class="font-medium">Activity</h2>

    <p v-if="loading" class="px-4 text-xs text-faint" role="status">
      {{ failed ? "Couldn't load activity." : 'Loading…' }}
    </p>
    <ol class="flex flex-col gap-3">
      <li v-for="entry in entries" :key="entry.kind === 'comment' ? entry.comment.id : entry.activity.id">
        <CommentItem v-if="entry.kind === 'comment'" :comment="entry.comment" />
        <p v-else class="flex items-center gap-2 px-4 text-xs text-muted">
          <UserAvatar
            :name="sync.user(entry.activity.actorId)?.name ?? ''"
            :email="sync.user(entry.activity.actorId)?.email"
            :avatar-url="sync.user(entry.activity.actorId)?.avatarUrl"
            :size="16"
          />
          <span class="min-w-0">
            <span class="font-medium text-fg">{{ actorName(entry.activity.actorId, sync.user(entry.activity.actorId)) }}</span>
            {{ describeActivity(entry.activity, ctx) }}
            <time class="text-faint" :datetime="entry.at" :title="fullDateTime(entry.at)"> · {{ timeAgo(entry.at) }}</time>
          </span>
        </p>
      </li>
    </ol>

    <form class="rounded-lg border border-border-strong bg-elevated focus-within:border-accent" @submit.prevent="post">
      <RichText
        v-model="draft"
        label="Leave a comment"
        placeholder="Leave a comment… Type @ to mention someone."
        class="min-h-20 px-4 pt-3 text-[14px] leading-relaxed"
        :upload="upload"
        @submit="post"
      />
      <div class="flex items-center justify-end gap-2 px-3 pb-3">
        <span class="text-xs text-faint">⌘↵ to send</span>
        <BaseButton type="submit" size="sm" variant="primary" :disabled="!draft.trim()">Comment</BaseButton>
      </div>
    </form>
  </section>
</template>
