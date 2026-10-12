import type { components } from '@/api/schema'

type Schemas = components['schemas']

export type UserRecord = Schemas['UserRecord']
export type TeamRecord = Schemas['TeamRecord']
export type TeamMemberRecord = Schemas['TeamMemberRecord']
export type WorkflowState = Schemas['WorkflowState']
export type Label = Schemas['Label']
export type Issue = Schemas['Issue']
export type Comment = Schemas['Comment']
export type Reaction = Schemas['Reaction']
export type Activity = Schemas['Activity']
export type Attachment = Schemas['Attachment']
export type Notification = Schemas['Notification']
export type Project = Schemas['Project']
export type Milestone = Schemas['Milestone']
export type Cycle = Schemas['Cycle']
export type Relation = Schemas['Relation']
export type Template = Schemas['Template']
export type View = Schemas['View']
export type Favorite = Schemas['Favorite']
export type PullRequest = Schemas['PullRequest']
export type TimeEntry = Schemas['TimeEntry']
export type Initiative = Schemas['Initiative']
export type ProjectDependency = Schemas['ProjectDependency']
export type ProjectUpdate = Schemas['ProjectUpdate']
export type CustomerRequest = Schemas['CustomerRequest']
export type SyncBootstrap = Schemas['SyncBootstrap']
export type IssueDetails = Schemas['IssueDetails']

/** Every synced model and the shape of its records. */
export interface Models {
  User: UserRecord
  Team: TeamRecord
  TeamMember: TeamMemberRecord
  WorkflowState: WorkflowState
  Label: Label
  Issue: Issue
  Comment: Comment
  Reaction: Reaction
  Activity: Activity
  Attachment: Attachment
  Notification: Notification
  Project: Project
  Milestone: Milestone
  Cycle: Cycle
  Relation: Relation
  Template: Template
  View: View
  Favorite: Favorite
  PullRequest: PullRequest
  TimeEntry: TimeEntry
  Initiative: Initiative
  ProjectDependency: ProjectDependency
  ProjectUpdate: ProjectUpdate
  CustomerRequest: CustomerRequest
}
export type ModelName = keyof Models

export const MODEL_NAMES: readonly ModelName[] = [
  'User',
  'Team',
  'TeamMember',
  'WorkflowState',
  'Label',
  'Issue',
  'Comment',
  'Reaction',
  'Activity',
  'Attachment',
  'Notification',
  'Project',
  'Milestone',
  'Cycle',
  'Relation',
  'Template',
  'View',
  'Favorite',
  'PullRequest',
  'TimeEntry',
  'Initiative',
  'ProjectDependency',
  'ProjectUpdate',
  'CustomerRequest',
]

/** One change from the server's sync log. */
export interface SyncAction {
  syncId: number
  model: ModelName
  modelId: string
  action: 'I' | 'U' | 'D'
  /** The record after the change (before it, for deletes). */
  data: Models[ModelName]
}

/** A frame the server sends on the sync socket. */
export type SyncMessage =
  | { type: 'actions'; lastSyncId: number; actions: SyncAction[] }
  | { type: 'reset' }

/** A record as stored: tagged with its model and its key within that model. */
export interface StoredRecord {
  model: ModelName
  key: string
  value: Models[ModelName]
}

/** The key that identifies a record within its model. */
export function recordKey<M extends ModelName>(model: M, record: Models[M]): string {
  if (model === 'TeamMember') {
    const member = record as TeamMemberRecord
    return `${member.teamId}:${member.userId}`
  }
  if (model === 'Reaction') return reactionKey(record as Reaction)
  return (record as { id: string }).id
}

/** A reaction has no ID of its own: one per comment, person and emoji. */
export function reactionKey(reaction: Reaction): string {
  return `${reaction.commentId}:${reaction.userId}:${reaction.emoji}`
}
