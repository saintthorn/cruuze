import { api, ApiError, unwrap } from '@/api/client'

import { AccessLost, type Transport } from './engine'
import { sendIssueUpdates, sendMutation } from './mutations'
import type { SyncMessage } from './types'

/** The real transport: REST for bootstrap and mutations, a WebSocket for the log. */
export function httpTransport(workspaceId: string): Transport {
  return {
    bootstrap: () =>
      unwrap(api.GET('/workspaces/{workspaceId}/sync/bootstrap', { params: { path: { workspaceId } } })).catch((err) => {
        // The server hides workspaces from non-members rather than refusing them.
        throw err instanceof ApiError && err.status === 404 ? new AccessLost() : err
      }),

    lookupIssue: (ref) =>
      unwrap(api.GET('/workspaces/{workspaceId}/issues/lookup', { params: { path: { workspaceId }, query: { ref } } })).catch((err) => {
        if (err instanceof ApiError && err.status === 404) return null
        throw err
      }),

    issueDetails: (issueId) => unwrap(api.GET('/issues/{issueId}/details', { params: { path: { issueId } } })),

    connect(after, handlers) {
      const scheme = location.protocol === 'https:' ? 'wss' : 'ws'
      const ws = new WebSocket(`${scheme}://${location.host}/api/workspaces/${workspaceId}/sync/ws?after=${after}`)
      ws.onopen = () => handlers.onOpen()
      ws.onmessage = (event) => handlers.onMessage(JSON.parse(event.data as string) as SyncMessage)
      ws.onclose = () => handlers.onClose()
      return { close: () => ws.close() }
    },

    send: sendMutation,
    sendIssueUpdates,
  }
}
