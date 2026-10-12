import { Extension } from '@tiptap/core'
import { PluginKey } from '@tiptap/pm/state'
import { Suggestion } from '@tiptap/suggestion'
import type { Ref } from 'vue'

import type { UserRecord } from '@/sync/types'

/** Mentions are stored as links, so they survive a round trip through Markdown. */
export const MENTION_PREFIX = 'mention://user/'

export function mentionHref(userId: string): string {
  return MENTION_PREFIX + userId
}

/** What the mention popup needs to draw itself; null while it is closed. */
export interface MentionPopup {
  items: UserRecord[]
  index: number
  /** Where the "@" is on screen. */
  rect: DOMRect | null
  choose(user: UserRecord): void
}

/**
 * Typing "@" opens a list of people; choosing one inserts their name as a
 * mention link. `popup` is the state the editor component renders.
 */
export function mentionExtension(users: () => UserRecord[], popup: Ref<MentionPopup | null>) {
  return Extension.create({
    name: 'mentionSuggestion',

    addProseMirrorPlugins() {
      return [
        Suggestion<UserRecord>({
          editor: this.editor,
          pluginKey: new PluginKey('mentionSuggestion'),
          char: '@',
          items: ({ query }) => {
            const q = query.toLowerCase()
            return users()
              .filter((u) => u.name.toLowerCase().includes(q) || u.email.toLowerCase().includes(q))
              .slice(0, 6)
          },
          command: ({ editor, range, props: user }) => {
            editor
              .chain()
              .focus()
              .insertContentAt(range, [
                {
                  type: 'text',
                  text: `@${user.name || user.email}`,
                  marks: [{ type: 'link', attrs: { href: mentionHref(user.id) } }],
                },
                { type: 'text', text: ' ' },
              ])
              .run()
          },
          render: () => ({
            onStart: (props) => {
              popup.value = { items: props.items, index: 0, rect: props.clientRect?.() ?? null, choose: (u) => props.command(u) }
            },
            onUpdate: (props) => {
              popup.value = { items: props.items, index: 0, rect: props.clientRect?.() ?? null, choose: (u) => props.command(u) }
            },
            onKeyDown: ({ event }) => {
              const state = popup.value
              if (!state || state.items.length === 0) return false
              if (event.key === 'ArrowDown' || event.key === 'ArrowUp') {
                const step = event.key === 'ArrowDown' ? 1 : -1
                popup.value = { ...state, index: (state.index + step + state.items.length) % state.items.length }
                return true
              }
              if (event.key === 'Enter' || event.key === 'Tab') {
                state.choose(state.items[state.index]!)
                return true
              }
              if (event.key === 'Escape') {
                popup.value = null
                return true
              }
              return false
            },
            onExit: () => {
              popup.value = null
            },
          }),
        }),
      ]
    },
  })
}
