import createClient from 'openapi-fetch'

import type { components, paths } from './schema'

export type User = components['schemas']['User']
export type Me = components['schemas']['Me']
export type Workspace = components['schemas']['Workspace']
export type WorkspaceRole = components['schemas']['WorkspaceRole']
export type WorkspaceMember = components['schemas']['WorkspaceMember']
export type WorkspaceInvite = components['schemas']['WorkspaceInvite']
export type Team = components['schemas']['Team']

export const api = createClient<paths>({ baseUrl: '/api' })

let onSessionExpired: (() => void) | undefined

/** Registers what to do when the server rejects the session of a signed-in user. */
export function setSessionExpiredHandler(handler: () => void) {
  onSessionExpired = handler
}

api.use({
  onResponse({ response, schemaPath }) {
    // A 401 from /me just means "not signed in"; anywhere else the session died.
    if (response.status === 401 && schemaPath !== '/me') onSessionExpired?.()
  },
})

/** An error response from the API, carrying its stable `code`. */
export class ApiError extends Error {
  constructor(
    readonly status: number,
    readonly code: string,
    message: string,
  ) {
    super(message)
    this.name = 'ApiError'
  }
}

type FetchResult<T> = {
  data?: T
  error?: unknown
  response: Response
}

/**
 * Resolves to the response body of an openapi-fetch call, or throws an
 * {@link ApiError}. Responses without a body resolve to `undefined`.
 */
export async function unwrap<T>(call: Promise<FetchResult<T>>): Promise<T> {
  let result: FetchResult<T>
  try {
    result = await call
  } catch {
    throw new ApiError(0, 'network', "Couldn't reach the server. Check your connection.")
  }
  if (result.response.ok) return result.data as T

  const body = result.error as Partial<components['schemas']['Error']> | undefined
  throw new ApiError(
    result.response.status,
    body?.code ?? 'unknown',
    body?.message ?? 'Something went wrong.',
  )
}

/** A message suitable for showing to the user. */
export function errorMessage(err: unknown): string {
  return err instanceof ApiError ? err.message : 'Something went wrong.'
}
