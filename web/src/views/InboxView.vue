<script setup lang="ts">
import { Inbox, MailOpen, Trash2 } from 'lucide-vue-next'
import { useRouter } from 'vue-router'

import StateIcon from '@/components/StateIcon.vue'
import BaseButton from '@/components/ui/BaseButton.vue'
import UserAvatar from '@/components/UserAvatar.vue'
import { describeNotification } from '@/lib/activity'
import { issueIdentifier } from '@/lib/issues'
import { fullDateTime, timeAgo } from '@/lib/time'
import { useSyncStore } from '@/stores/sync'
import type { Notification } from '@/sync/types'

const router = useRouter()
const sync = useSyncStore()

function issueOf(notification: Notification) {
  return sync.issues.find((i) => i.id === notification.issueId)
}

/** What happened, plus the first line of the comment it is about, as plain text. */
function summary(notification: Notification): string {
  const what = describeNotification(notification, sync.user(notification.actorId))
  const firstLine = (sync.comment(notification.commentId)?.body ?? '')
    .split('\n')[0]!
    .replace(/!?\[([^\]]*)\]\([^)]*\)/g, '$1') // links and images: keep their text
    .replace(/\\?[*_`~]/g, '') // emphasis and code markers
    .trim()
    .slice(0, 140)
  return firstLine ? `${what}: ${firstLine}` : what
}

function open(notification: Notification) {
  const issue = issueOf(notification)
  if (!issue) return
  sync.markNotification(notification.id, true)
  const issueRef = issue.number > 0 ? issueIdentifier(issue, sync.team(issue.teamId)) : issue.id
  void router.push({ name: 'issue', params: { issueRef } })
}
</script>

<template>
  <header class="flex h-11 shrink-0 items-center gap-2 border-b border-border px-5">
    <h1 class="font-medium">Inbox</h1>
    <span v-if="sync.unreadCount > 0" class="text-faint">{{ sync.unreadCount }} unread</span>
    <BaseButton v-if="sync.unreadCount > 0" size="sm" class="ml-auto" @click="sync.markAllNotificationsRead()">
      Mark all as read
    </BaseButton>
  </header>

  <ul v-if="sync.notifications.length > 0" class="flex-1 overflow-y-auto" aria-label="Notifications">
    <li
      v-for="notification in sync.notifications"
      :key="notification.id"
      class="group flex items-center gap-3 border-b border-border pr-4 pl-5 hover:bg-hover"
    >
      <span
        class="size-2 shrink-0 rounded-full"
        :class="notification.readAt ? 'bg-transparent' : 'bg-accent'"
        :aria-label="notification.readAt ? undefined : 'Unread'"
        :role="notification.readAt ? undefined : 'img'"
      />
      <button type="button" class="flex min-w-0 flex-1 items-center gap-3 py-2.5 text-left" @click="open(notification)">
        <UserAvatar
          :name="sync.user(notification.actorId)?.name ?? ''"
          :email="sync.user(notification.actorId)?.email"
          :avatar-url="sync.user(notification.actorId)?.avatarUrl"
          :size="28"
        />
        <span class="min-w-0 flex-1">
          <span class="flex items-center gap-2" :class="notification.readAt ? 'text-muted' : 'font-medium text-fg'">
            <StateIcon
              v-if="sync.state(issueOf(notification)?.stateId ?? '')"
              :type="sync.state(issueOf(notification)!.stateId)!.type"
              :color="sync.state(issueOf(notification)!.stateId)!.color"
            />
            <span class="shrink-0 tabular-nums">
              {{ issueIdentifier(issueOf(notification)!, sync.team(issueOf(notification)!.teamId)) }}
            </span>
            <span class="truncate">{{ issueOf(notification)!.title }}</span>
          </span>
          <span class="block truncate text-xs text-muted">{{ summary(notification) }}</span>
        </span>
        <time class="shrink-0 text-xs text-faint" :datetime="notification.createdAt" :title="fullDateTime(notification.createdAt)">
          {{ timeAgo(notification.createdAt) }}
        </time>
      </button>
      <span class="flex shrink-0 gap-1 opacity-0 group-hover:opacity-100 focus-within:opacity-100">
        <button
          type="button"
          class="flex size-7 items-center justify-center rounded text-faint hover:bg-active hover:text-fg"
          :aria-label="notification.readAt ? 'Mark as unread' : 'Mark as read'"
          :title="notification.readAt ? 'Mark as unread' : 'Mark as read'"
          @click="sync.markNotification(notification.id, !notification.readAt)"
        >
          <MailOpen class="size-3.5" aria-hidden="true" />
        </button>
        <button
          type="button"
          class="flex size-7 items-center justify-center rounded text-faint hover:bg-active hover:text-danger"
          aria-label="Delete notification"
          title="Delete"
          @click="sync.deleteNotification(notification.id)"
        >
          <Trash2 class="size-3.5" aria-hidden="true" />
        </button>
      </span>
    </li>
  </ul>

  <div v-else class="flex flex-1 flex-col items-center justify-center gap-2 p-8 text-center">
    <Inbox class="size-8 text-faint" aria-hidden="true" />
    <p class="text-muted">Inbox empty</p>
  </div>
</template>
