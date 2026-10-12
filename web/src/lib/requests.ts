import type { CustomerRequest } from '@/sync/types'

export type RequestStatus = CustomerRequest['status']

/** The statuses of a customer's request, in the order they are worked through. */
export const REQUEST_STATUSES: readonly { value: RequestStatus; label: string; class: string }[] = [
  { value: 'new', label: 'New', class: 'text-accent' },
  { value: 'reviewing', label: 'Under review', class: 'text-warning' },
  { value: 'planned', label: 'Planned', class: 'text-fg' },
  { value: 'shipped', label: 'Shipped', class: 'text-success' },
  { value: 'declined', label: 'Not planned', class: 'text-faint' },
]

export function requestStatus(status: RequestStatus) {
  return REQUEST_STATUSES.find((s) => s.value === status)!
}

export const isOpenRequest = (request: Pick<CustomerRequest, 'status'>) => request.status !== 'shipped' && request.status !== 'declined'
