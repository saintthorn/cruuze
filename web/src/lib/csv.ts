import type { Issue, Label, UserRecord, WorkflowState } from '@/sync/types'

import { priorityLabel } from './issues'

/** Quotes a CSV field when it needs it. */
function field(value: string): string {
  // A leading =, +, - or @ would run as a formula when the file is opened in
  // a spreadsheet; a leading quote makes it plain text.
  if (/^[=+\-@\t\r]/.test(value)) value = `'${value}`
  return /[",\r\n]/.test(value) ? `"${value.replaceAll('"', '""')}"` : value
}

/** Encodes rows as CSV (RFC 4180: CRLF line ends, quotes doubled). */
export function toCsv(rows: readonly (readonly string[])[]): string {
  return rows.map((row) => row.map(field).join(',')).join('\r\n') + '\r\n'
}

export interface CsvContext {
  identifier(issue: Issue): string
  state(id: string): WorkflowState | undefined
  user(id: string | undefined): UserRecord | undefined
  label(id: string): Label | undefined
}

/**
 * Issues as a CSV file with the columns the importer reads, so an export can
 * be imported again (into another team or workspace).
 */
export function issuesToCsv(issues: readonly Issue[], ctx: CsvContext): string {
  const header = ['ID', 'Title', 'Description', 'Status', 'Priority', 'Assignee', 'Labels', 'Due date', 'Created']
  const rows = issues.map((issue) => [
    ctx.identifier(issue),
    issue.title,
    issue.description,
    ctx.state(issue.stateId)?.name ?? '',
    issue.priority === 0 ? '' : priorityLabel(issue.priority),
    ctx.user(issue.assigneeId)?.email ?? '',
    issue.labelIds.flatMap((id) => ctx.label(id)?.name ?? []).join(', '),
    issue.dueDate ?? '',
    issue.createdAt,
  ])
  return toCsv([header, ...rows])
}
