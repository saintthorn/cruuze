package server

import (
	"context"
	"regexp"
	"slices"
	"strconv"

	"github.com/google/uuid"

	"cruuze/server/internal/api"
	"cruuze/server/internal/db/dbgen"
	"cruuze/server/internal/syncer"
)

// This file holds what issue changes and comments have in common: who gets
// subscribed and notified, and the activity feed.

// Mentions are written in Markdown as links: [@Ada](mention://user/<id>).
var mentionPattern = regexp.MustCompile(`\(mention://user/([0-9a-fA-F-]{36})\)`)

// mentionedUsers returns the users mentioned in a piece of Markdown.
func mentionedUsers(markdown string) []uuid.UUID {
	var ids []uuid.UUID
	for _, match := range mentionPattern.FindAllStringSubmatch(markdown, -1) {
		if id, err := uuid.Parse(match[1]); err == nil && !slices.Contains(ids, id) {
			ids = append(ids, id)
		}
	}
	return ids
}

// visibleTo returns those of the given users who may see the team's issues,
// so that mentions and notifications never reveal an issue to anyone else.
func visibleTo(ctx context.Context, q *dbgen.Queries, team dbgen.Team, userIDs []uuid.UUID) ([]uuid.UUID, error) {
	if len(userIDs) == 0 {
		return nil, nil
	}
	return q.ListUsersWhoCanSeeTeam(ctx, dbgen.ListUsersWhoCanSeeTeamParams{TeamID: team.ID, UserIds: userIDs})
}

// notify tells each recipient about something that happened on an issue.
// The actor is never notified about their own action, and neither is anyone
// who can't see the issue. A zero actorID means the system did it.
func notify(ctx context.Context, q *dbgen.Queries, team dbgen.Team, issue dbgen.Issue, actorID uuid.UUID, kind api.NotificationType, commentID *uuid.UUID, recipients []uuid.UUID) error {
	recipients = slices.DeleteFunc(uniqueIDs(recipients), func(id uuid.UUID) bool { return id == actorID })
	recipients, err := visibleTo(ctx, q, team, recipients)
	if err != nil {
		return err
	}
	var actor *uuid.UUID
	if actorID != uuid.Nil {
		actor = &actorID
	}
	for _, userID := range recipients {
		n, err := q.CreateNotification(ctx, dbgen.CreateNotificationParams{
			ID:          newID(),
			WorkspaceID: issue.WorkspaceID,
			UserID:      userID,
			IssueID:     issue.ID,
			ActorID:     actor,
			CommentID:   commentID,
			Type:        string(kind),
		})
		if err != nil {
			return err
		}
		if _, err := recordNotification(ctx, q, n, syncer.Insert); err != nil {
			return err
		}
	}
	return nil
}

func recordNotification(ctx context.Context, q *dbgen.Queries, n dbgen.Notification, action string) (int64, error) {
	return syncer.Record(ctx, q, syncer.Change{
		WorkspaceID: n.WorkspaceID,
		Model:       api.SyncModelNotification,
		ModelID:     n.ID,
		Action:      action,
		Data:        notificationRecord(n),
		UserID:      &n.UserID,
	})
}

// activity is one entry for an issue's activity feed.
type activity struct {
	kind                 api.ActivityType
	from, to             *string
	addedLabels, removed []uuid.UUID
}

// logActivities appends entries to an issue's activity feed.
// actorID is nil for changes the system makes, such as rolling a cycle over.
func logActivities(ctx context.Context, q *dbgen.Queries, team dbgen.Team, issue dbgen.Issue, actorID *uuid.UUID, entries []activity) error {
	for _, e := range entries {
		params := dbgen.CreateIssueActivityParams{
			ID:              newID(),
			WorkspaceID:     issue.WorkspaceID,
			IssueID:         issue.ID,
			ActorID:         actorID,
			Type:            string(e.kind),
			FromValue:       e.from,
			ToValue:         e.to,
			AddedLabelIds:   e.addedLabels,
			RemovedLabelIds: e.removed,
		}
		if params.AddedLabelIds == nil {
			params.AddedLabelIds = []uuid.UUID{}
		}
		if params.RemovedLabelIds == nil {
			params.RemovedLabelIds = []uuid.UUID{}
		}
		row, err := q.CreateIssueActivity(ctx, params)
		if err != nil {
			return err
		}
		_, err = syncer.Record(ctx, q, syncer.Change{
			WorkspaceID: issue.WorkspaceID,
			Model:       api.SyncModelActivity,
			ModelID:     row.ID,
			Action:      syncer.Insert,
			Data:        activityRecord(row),
			Team:        &team,
		})
		if err != nil {
			return err
		}
	}
	return nil
}

// issueChanges lists what differs between two versions of an issue, as
// activity entries. Reordering is not an activity.
func issueChanges(before, after dbgen.Issue) []activity {
	var out []activity
	text := func(s string) *string { return &s }
	id := func(u *uuid.UUID) *string {
		if u == nil {
			return nil
		}
		return text(u.String())
	}

	if before.Title != after.Title {
		out = append(out, activity{kind: api.ActivityTypeTitle, from: text(before.Title), to: text(after.Title)})
	}
	if before.Description != after.Description {
		out = append(out, activity{kind: api.ActivityTypeDescription})
	}
	if before.StateID != after.StateID {
		out = append(out, activity{kind: api.ActivityTypeState, from: id(&before.StateID), to: id(&after.StateID)})
	}
	if before.Priority != after.Priority {
		out = append(out, activity{
			kind: api.ActivityTypePriority,
			from: text(strconv.Itoa(int(before.Priority))),
			to:   text(strconv.Itoa(int(after.Priority))),
		})
	}
	if !equalPtr(before.AssigneeID, after.AssigneeID) {
		out = append(out, activity{kind: api.ActivityTypeAssignee, from: id(before.AssigneeID), to: id(after.AssigneeID)})
	}
	if !equalPtr(before.ProjectID, after.ProjectID) {
		out = append(out, activity{kind: api.ActivityTypeProject, from: id(before.ProjectID), to: id(after.ProjectID)})
	}
	if !equalPtr(before.MilestoneID, after.MilestoneID) {
		out = append(out, activity{kind: api.ActivityTypeMilestone, from: id(before.MilestoneID), to: id(after.MilestoneID)})
	}
	if !equalPtr(before.CycleID, after.CycleID) {
		out = append(out, activity{kind: api.ActivityTypeCycle, from: id(before.CycleID), to: id(after.CycleID)})
	}
	if !equalPtr(before.ParentID, after.ParentID) {
		out = append(out, activity{kind: api.ActivityTypeParent, from: id(before.ParentID), to: id(after.ParentID)})
	}
	added := without(after.LabelIds, before.LabelIds)
	removed := without(before.LabelIds, after.LabelIds)
	if len(added) > 0 || len(removed) > 0 {
		out = append(out, activity{kind: api.ActivityTypeLabels, addedLabels: added, removed: removed})
	}
	if (before.DueDate == nil) != (after.DueDate == nil) || (before.DueDate != nil && !before.DueDate.Equal(*after.DueDate)) {
		var from, to *string
		if before.DueDate != nil {
			from = text(before.DueDate.Format("2006-01-02"))
		}
		if after.DueDate != nil {
			to = text(after.DueDate.Format("2006-01-02"))
		}
		out = append(out, activity{kind: api.ActivityTypeDueDate, from: from, to: to})
	}
	return out
}

func equalPtr[T comparable](a, b *T) bool {
	if a == nil || b == nil {
		return a == b
	}
	return *a == *b
}

// without returns the elements of a that are not in b.
func without(a, b []uuid.UUID) []uuid.UUID {
	var out []uuid.UUID
	for _, id := range a {
		if !slices.Contains(b, id) {
			out = append(out, id)
		}
	}
	return out
}
