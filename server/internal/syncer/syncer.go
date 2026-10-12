// Package syncer implements the sync log: every change to a synced record is
// appended to it inside the transaction that makes the change, and clients
// replay the log to stay current.
package syncer

import (
	"context"
	"encoding/json"
	"fmt"
	"slices"
	"time"

	"github.com/google/uuid"

	"cruuze/server/internal/api"
	"cruuze/server/internal/db/dbgen"
)

// Channel is the Postgres NOTIFY channel that announces new sync actions.
// The payload is the workspace ID.
const Channel = "cruuze_sync"

const (
	Insert = string(api.SyncActionActionI)
	Update = string(api.SyncActionActionU)
	Delete = string(api.SyncActionActionD)
)

// Change describes one change to a synced record.
type Change struct {
	WorkspaceID uuid.UUID
	Model       api.SyncModel
	ModelID     uuid.UUID
	Action      string
	// Data is the record after the change (before it, for deletes). It is
	// stored as JSON and sent to clients as-is.
	Data any
	// Team limits who can see the change; nil means the whole workspace.
	Team *dbgen.Team
	// UserID makes the change private to one user, e.g. their notifications.
	UserID *uuid.UUID
	// Resets marks a change to who can see Team (it went private or public):
	// clients of people outside the team bootstrap again when they reach it.
	Resets bool
	// MembersOnly keeps the change from guests.
	MembersOnly bool
}

// Record appends a change to the workspace's sync log and returns its sync
// ID. q must be bound to the transaction that makes the change: allocating
// the sync ID locks the workspace row until commit, which is what keeps sync
// IDs gapless and visible in order.
func Record(ctx context.Context, q *dbgen.Queries, c Change) (int64, error) {
	data, err := json.Marshal(c.Data)
	if err != nil {
		return 0, fmt.Errorf("marshal %s: %w", c.Model, err)
	}
	syncID, err := q.NextSyncID(ctx, c.WorkspaceID)
	if err != nil {
		return 0, err
	}
	params := dbgen.InsertSyncActionParams{
		WorkspaceID: c.WorkspaceID,
		SyncID:      syncID,
		Model:       string(c.Model),
		ModelID:     c.ModelID,
		Action:      c.Action,
		Data:        data,
		UserID:      c.UserID,
		Resets:      c.Resets,
		MembersOnly: c.MembersOnly,
	}
	if c.Team != nil {
		params.TeamID = &c.Team.ID
		params.TeamPrivate = c.Team.Private
	}
	if err := q.InsertSyncAction(ctx, params); err != nil {
		return 0, err
	}
	if c.UserID == nil && slices.Contains(WebhookModels, c.Model) {
		if err := enqueueWebhooks(ctx, q, c, syncID, data); err != nil {
			return 0, err
		}
	}
	return syncID, q.NotifySync(ctx, c.WorkspaceID.String())
}

// WebhookModels are the models whose changes webhooks can be sent.
var WebhookModels = []api.SyncModel{
	api.SyncModelIssue, api.SyncModelComment, api.SyncModelProject, api.SyncModelMilestone,
	api.SyncModelCycle, api.SyncModelLabel, api.SyncModelAttachment, api.SyncModelPullRequest,
}

// WebhookPayload is the body of a webhook delivery.
type WebhookPayload struct {
	// Action is create, update or remove.
	Action string        `json:"action"`
	Type   api.SyncModel `json:"type"`
	// Data is the record after the change (before it, for removals).
	Data        json.RawMessage `json:"data"`
	WorkspaceID uuid.UUID       `json:"workspaceId"`
	// SyncID orders changes within a workspace; deliveries can arrive out of order.
	SyncID    int64     `json:"syncId"`
	CreatedAt time.Time `json:"createdAt"`
}

var webhookActions = map[string]string{Insert: "create", Update: "update", Delete: "remove"}

// enqueueWebhooks queues the change for every webhook that wants it. Being in
// the same transaction, a delivery exists exactly when its change committed.
func enqueueWebhooks(ctx context.Context, q *dbgen.Queries, c Change, syncID int64, data []byte) error {
	payload, err := json.Marshal(WebhookPayload{
		Action:      webhookActions[c.Action],
		Type:        c.Model,
		Data:        data,
		WorkspaceID: c.WorkspaceID,
		SyncID:      syncID,
		CreatedAt:   time.Now().UTC(),
	})
	if err != nil {
		return err
	}
	params := dbgen.EnqueueWebhookDeliveriesParams{
		WorkspaceID: c.WorkspaceID,
		Model:       string(c.Model),
		Action:      webhookActions[c.Action],
		Payload:     payload,
	}
	if c.Team != nil {
		params.TeamID = &c.Team.ID
		params.TeamPrivate = c.Team.Private
	}
	return q.EnqueueWebhookDeliveries(ctx, params)
}

// Access is what one user may see in a workspace.
type Access struct {
	UserID uuid.UUID
	// Guest users only see teams they belong to.
	Guest bool
	// Teams holds the IDs of the teams the user belongs to.
	Teams map[uuid.UUID]bool
}

// LoadAccess reads a user's current access to a workspace.
func LoadAccess(ctx context.Context, q *dbgen.Queries, workspaceID, userID uuid.UUID, role string) (*Access, error) {
	teamIDs, err := q.ListMemberTeamIDs(ctx, dbgen.ListMemberTeamIDsParams{WorkspaceID: workspaceID, UserID: userID})
	if err != nil {
		return nil, err
	}
	a := &Access{UserID: userID, Guest: role == string(api.WorkspaceRoleGuest), Teams: make(map[uuid.UUID]bool, len(teamIDs))}
	for _, id := range teamIDs {
		a.Teams[id] = true
	}
	return a, nil
}

// CanSeeTeam reports whether the user may see records scoped to a team.
func (a *Access) CanSeeTeam(teamID uuid.UUID, private bool) bool {
	return a.Teams[teamID] || (!private && !a.Guest)
}

// Action is a sync action as sent to clients (the SyncAction API schema).
type Action struct {
	SyncID  int64           `json:"syncId"`
	Model   string          `json:"model"`
	ModelID uuid.UUID       `json:"modelId"`
	Action  string          `json:"action"`
	Data    json.RawMessage `json:"data"`
}

// Batch is the result of reading the log on behalf of one user.
type Batch struct {
	// Actions the user may see, in order.
	Actions []Action
	// Cursor is the last sync ID examined, visible or not.
	Cursor int64
	// More reports that the log may hold further actions after Cursor.
	More bool
	// Reset reports that the user's access changed at Cursor+1, so their copy
	// can't be patched and must be bootstrapped again.
	Reset bool
}

// Fetch reads up to limit actions after the given sync ID and filters them to
// what access allows. Joining or leaving a public team is applied to access
// in place; a change that alters what the user can see (their membership of
// a private team, their role, a team's privacy, their removal from the
// workspace) stops the batch with Reset set.
func Fetch(ctx context.Context, q *dbgen.Queries, workspaceID uuid.UUID, access *Access, after int64, limit int32) (Batch, error) {
	batch := Batch{Cursor: after}
	rows, err := q.ListSyncActionsAfter(ctx, dbgen.ListSyncActionsAfterParams{
		WorkspaceID: workspaceID,
		SyncID:      after,
		Limit:       limit,
	})
	if err != nil {
		return batch, err
	}
	batch.More = len(rows) == int(limit)

	for _, row := range rows {
		// TeamMember actions use the user's ID as their model ID.
		if row.Model == string(api.SyncModelTeamMember) && row.ModelID == access.UserID && row.TeamID != nil {
			if row.TeamPrivate || access.Guest {
				batch.Reset = true
				return batch, nil
			}
			if row.Action == Delete {
				delete(access.Teams, *row.TeamID)
			} else {
				access.Teams[*row.TeamID] = true
			}
		}
		// The user's own record: gone means they were removed, and a change
		// between guest and not changes which teams they see.
		if row.Model == string(api.SyncModelUser) && row.ModelID == access.UserID {
			var user struct {
				Role api.WorkspaceRole `json:"role"`
			}
			if row.Action == Delete || json.Unmarshal(row.Data, &user) != nil || (user.Role == api.WorkspaceRoleGuest) != access.Guest {
				batch.Reset = true
				return batch, nil
			}
		}
		// A reset for the whole workspace: records were written without being
		// logged one by one (an import), so every copy starts over.
		if row.Resets && row.TeamID == nil {
			batch.Reset = true
			return batch, nil
		}
		if row.Resets && row.TeamID != nil && !access.Teams[*row.TeamID] && !access.Guest {
			batch.Reset = true
			return batch, nil
		}
		private := (row.UserID != nil && *row.UserID != access.UserID) || (row.MembersOnly && access.Guest)
		if !private && (row.TeamID == nil || access.CanSeeTeam(*row.TeamID, row.TeamPrivate)) {
			batch.Actions = append(batch.Actions, Action{
				SyncID:  row.SyncID,
				Model:   row.Model,
				ModelID: row.ModelID,
				Action:  row.Action,
				Data:    row.Data,
			})
		}
		batch.Cursor = row.SyncID
	}
	return batch, nil
}

// CursorValid reports whether a client that has applied everything up to
// after can be brought current by replaying the log. It can't if the log has
// been pruned past that point, or if after is ahead of the workspace (the
// client's copy came from a database that has since been restored).
func CursorValid(ctx context.Context, q *dbgen.Queries, workspaceID uuid.UUID, after int64) (bool, error) {
	last, err := q.GetLastSyncID(ctx, workspaceID)
	if err != nil {
		return false, err
	}
	if after < 0 || after > last {
		return false, nil
	}
	if after == last {
		return true, nil
	}
	oldest, err := q.GetOldestSyncIDAfter(ctx, dbgen.GetOldestSyncIDAfterParams{WorkspaceID: workspaceID, SyncID: after})
	if err != nil {
		return false, err
	}
	return oldest == after+1, nil
}

// ResetModel is the model name of the log entry that ResetAll writes. No
// client ever receives it: reaching it is what makes them bootstrap again.
const ResetModel = "Reset"

// ResetAll makes every client of the workspace throw its copy away and
// bootstrap again. It is for changes too large to log record by record,
// such as an import: the records are written directly, and then this.
func ResetAll(ctx context.Context, q *dbgen.Queries, workspaceID uuid.UUID) error {
	syncID, err := q.NextSyncID(ctx, workspaceID)
	if err != nil {
		return err
	}
	err = q.InsertSyncAction(ctx, dbgen.InsertSyncActionParams{
		WorkspaceID: workspaceID,
		SyncID:      syncID,
		Model:       ResetModel,
		ModelID:     workspaceID,
		Action:      Update,
		Data:        []byte("{}"),
		Resets:      true,
	})
	if err != nil {
		return err
	}
	return q.NotifySync(ctx, workspaceID.String())
}
