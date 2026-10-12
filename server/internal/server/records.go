package server

import (
	"context"
	"encoding/json"
	"time"

	"github.com/google/uuid"
	openapi_types "github.com/oapi-codegen/runtime/types"

	"cruuze/server/internal/api"
	"cruuze/server/internal/db/dbgen"
	"cruuze/server/internal/syncer"
)

// This file maps database rows to the records clients sync, and holds the
// writes that several handlers share. Every write to a synced table must log
// a sync action in the same transaction.

func userRecord(u dbgen.User, role string, joinedAt time.Time) api.UserRecord {
	out := api.UserRecord{
		Id:       u.ID,
		Email:    u.Email,
		Name:     u.Name,
		Role:     api.WorkspaceRole(role),
		JoinedAt: joinedAt,
	}
	if u.AvatarUrl != "" {
		out.AvatarUrl = &u.AvatarUrl
	}
	return out
}

func teamRecord(t dbgen.Team) api.TeamRecord {
	return api.TeamRecord{
		Id:              t.ID,
		Name:            t.Name,
		Key:             t.Key,
		Kind:            api.TeamKind(t.Kind),
		Private:         t.Private,
		CycleWeeks:      int32(t.CycleWeeks),
		TriageEnabled:   t.TriageEnabled,
		AutoArchiveDays: t.AutoArchiveDays,
		StaleDays:       t.StaleDays,
		AutoCloseDays:   t.AutoCloseDays,
		AutoCloseParent: t.AutoCloseParent,
		AutoAssign:      t.AutoAssign,
		AutoCycle:       t.AutoCycle,
		ReviewStateId:   t.ReviewStateID,
		CreatedAt:       t.CreatedAt,
	}
}

func projectRecord(p dbgen.Project) api.Project {
	return api.Project{
		Id:           p.ID,
		Name:         p.Name,
		Description:  p.Description,
		Status:       api.ProjectStatus(p.Status),
		LeadId:       p.LeadID,
		StartDate:    apiDate(p.StartDate),
		TargetDate:   apiDate(p.TargetDate),
		Color:        p.Color,
		InitiativeId: p.InitiativeID,
		CreatedAt:    p.CreatedAt,
	}
}

func milestoneRecord(m dbgen.Milestone) api.Milestone {
	return api.Milestone{Id: m.ID, ProjectId: m.ProjectID, Name: m.Name, TargetDate: apiDate(m.TargetDate), SortOrder: m.SortOrder}
}

func cycleRecord(c dbgen.Cycle) api.Cycle {
	return api.Cycle{
		Id:       c.ID,
		TeamId:   c.TeamID,
		Number:   c.Number,
		Name:     c.Name,
		StartsAt: openapi_types.Date{Time: c.StartsAt},
		EndsAt:   openapi_types.Date{Time: c.EndsAt},
	}
}

func relationRecord(r dbgen.IssueRelation) api.Relation {
	return api.Relation{Id: r.ID, IssueId: r.IssueID, RelatedIssueId: r.RelatedIssueID, Type: api.RelationType(r.Type)}
}

func templateRecord(t dbgen.IssueTemplate) api.Template {
	out := api.Template{
		Id:          t.ID,
		TeamId:      t.TeamID,
		Name:        t.Name,
		Title:       t.Title,
		Description: t.Description,
		Priority:    int32(t.Priority),
		LabelIds:    t.LabelIds,
	}
	if out.LabelIds == nil {
		out.LabelIds = []uuid.UUID{}
	}
	return out
}

func viewRecord(v dbgen.View) api.View {
	out := api.View{Id: v.ID, Name: v.Name, OwnerId: v.OwnerID, Shared: v.Shared, TeamId: v.TeamID, Options: map[string]any{}}
	// Stored as encoded by us, so this can't fail; an empty object if it somehow does.
	_ = json.Unmarshal(v.Options, &out.Options)
	return out
}

func favoriteRecord(f dbgen.Favorite) api.Favorite {
	return api.Favorite{Id: f.ID, Kind: api.FavoriteKind(f.Kind), TargetId: f.TargetID, SortOrder: f.SortOrder}
}

func workflowStateRecord(s dbgen.WorkflowState) api.WorkflowState {
	return api.WorkflowState{
		Id:       s.ID,
		TeamId:   s.TeamID,
		Name:     s.Name,
		Type:     api.WorkflowStateType(s.Type),
		Color:    s.Color,
		Position: s.Position,
	}
}

func issueRecord(i dbgen.Issue) api.Issue {
	out := api.Issue{
		Id:            i.ID,
		TeamId:        i.TeamID,
		Number:        i.Number,
		Title:         i.Title,
		Description:   i.Description,
		StateId:       i.StateID,
		Priority:      int32(i.Priority),
		AssigneeId:    i.AssigneeID,
		CreatorId:     i.CreatorID,
		SortOrder:     i.SortOrder,
		LabelIds:      i.LabelIds,
		SubscriberIds: i.SubscriberIds,
		ProjectId:     i.ProjectID,
		MilestoneId:   i.MilestoneID,
		CycleId:       i.CycleID,
		ParentId:      i.ParentID,
		CreatedAt:     i.CreatedAt,
		UpdatedAt:     i.UpdatedAt,
		ArchivedAt:    i.ArchivedAt,
		StartedAt:     i.StartedAt,
		CompletedAt:   i.CompletedAt,
	}
	// Always arrays in JSON, never null.
	if out.LabelIds == nil {
		out.LabelIds = []uuid.UUID{}
	}
	if out.SubscriberIds == nil {
		out.SubscriberIds = []uuid.UUID{}
	}
	if i.DueDate != nil {
		out.DueDate = &openapi_types.Date{Time: *i.DueDate}
	}
	if i.LoggedSeconds > 0 {
		out.LoggedSeconds = &i.LoggedSeconds
	}
	return out
}

func commentRecord(c dbgen.Comment) api.Comment {
	return api.Comment{Id: c.ID, IssueId: c.IssueID, UserId: c.UserID, Body: c.Body, CreatedAt: c.CreatedAt, EditedAt: c.EditedAt}
}

func activityRecord(a dbgen.IssueActivity) api.Activity {
	out := api.Activity{
		Id:        a.ID,
		IssueId:   a.IssueID,
		ActorId:   a.ActorID,
		Type:      api.ActivityType(a.Type),
		From:      a.FromValue,
		To:        a.ToValue,
		CreatedAt: a.CreatedAt,
	}
	if len(a.AddedLabelIds) > 0 {
		out.AddedLabelIds = &a.AddedLabelIds
	}
	if len(a.RemovedLabelIds) > 0 {
		out.RemovedLabelIds = &a.RemovedLabelIds
	}
	return out
}

func notificationRecord(n dbgen.Notification) api.Notification {
	return api.Notification{
		Id:        n.ID,
		Type:      api.NotificationType(n.Type),
		IssueId:   n.IssueID,
		ActorId:   n.ActorID,
		CommentId: n.CommentID,
		CreatedAt: n.CreatedAt,
		ReadAt:    n.ReadAt,
	}
}

func attachmentRecord(a dbgen.Attachment) api.Attachment {
	return api.Attachment{
		Id:          a.ID,
		IssueId:     a.IssueID,
		UploaderId:  a.UploaderID,
		Filename:    a.Filename,
		ContentType: a.ContentType,
		Size:        a.Size,
		CreatedAt:   a.CreatedAt,
	}
}

func pullRequestRecord(p dbgen.PullRequest) api.PullRequest {
	return api.PullRequest{
		Id:        p.ID,
		IssueId:   p.IssueID,
		Url:       p.Url,
		Repo:      p.Repo,
		Number:    p.Number,
		Title:     p.Title,
		Branch:    p.Branch,
		Author:    p.Author,
		Status:    api.PullRequestStatus(p.Status),
		CreatedAt: p.CreatedAt,
		UpdatedAt: p.UpdatedAt,
	}
}

func labelRecord(l dbgen.Label) api.Label {
	return api.Label{Id: l.ID, Name: l.Name, Color: l.Color}
}

type workflowTemplate []struct {
	name, kind, color string
}

// defaultWorkflow is the set of states an engineering team starts with. The
// migration that introduced workflow states gives existing teams the same set.
var defaultWorkflow = workflowTemplate{
	{"Backlog", "backlog", "#95a2b3"},
	{"Todo", "unstarted", "#e2e2e2"},
	{"In Progress", "started", "#f2c94c"},
	{"Done", "completed", "#5e6ad2"},
	{"Canceled", "canceled", "#95a2b3"},
}

// teamWorkflows is what a team of each kind starts with, in words its
// people use. Every status can be renamed, added to or removed afterwards.
var teamWorkflows = map[api.TeamKind]workflowTemplate{
	api.TeamKindEngineering: defaultWorkflow,
	// Support works a queue: new tickets wait in triage (turned on for the team).
	api.TeamKindSupport: {
		{"Open", "unstarted", "#e2e2e2"},
		{"In Progress", "started", "#f2c94c"},
		{"Waiting on Customer", "started", "#4ea7fc"},
		{"Resolved", "completed", "#5e6ad2"},
		{"Won't Fix", "canceled", "#95a2b3"},
	},
	api.TeamKindMarketing: {
		{"Ideas", "backlog", "#95a2b3"},
		{"Planned", "unstarted", "#e2e2e2"},
		{"In Production", "started", "#f2c94c"},
		{"In Review", "started", "#4ea7fc"},
		{"Published", "completed", "#5e6ad2"},
		{"Dropped", "canceled", "#95a2b3"},
	},
	api.TeamKindDesign: {
		{"Backlog", "backlog", "#95a2b3"},
		{"To Do", "unstarted", "#e2e2e2"},
		{"Exploring", "started", "#f2c94c"},
		{"In Review", "started", "#4ea7fc"},
		{"Approved", "completed", "#5e6ad2"},
		{"Canceled", "canceled", "#95a2b3"},
	},
	api.TeamKindGeneral: {
		{"To Do", "unstarted", "#e2e2e2"},
		{"Doing", "started", "#f2c94c"},
		{"Done", "completed", "#5e6ad2"},
		{"Canceled", "canceled", "#95a2b3"},
	},
}

// createTeam inserts a team with the workflow for its kind and its creator
// as the first member.
func createTeam(ctx context.Context, q *dbgen.Queries, params dbgen.CreateTeamParams, creatorID uuid.UUID) (dbgen.Team, error) {
	if params.Kind == "" {
		params.Kind = string(api.TeamKindEngineering)
	}
	// What customers send in is looked at before it joins the queue.
	params.TriageEnabled = params.Kind == string(api.TeamKindSupport)
	team, err := q.CreateTeam(ctx, params)
	if err != nil {
		return team, err
	}
	_, err = syncer.Record(ctx, q, syncer.Change{
		WorkspaceID: team.WorkspaceID,
		Model:       api.SyncModelTeam,
		ModelID:     team.ID,
		Action:      syncer.Insert,
		Data:        teamRecord(team),
		Team:        &team,
	})
	if err != nil {
		return team, err
	}

	for i, st := range teamWorkflows[api.TeamKind(team.Kind)] {
		state, err := q.CreateWorkflowState(ctx, dbgen.CreateWorkflowStateParams{
			ID:          newID(),
			WorkspaceID: team.WorkspaceID,
			TeamID:      team.ID,
			Name:        st.name,
			Type:        st.kind,
			Color:       st.color,
			Position:    float64(i),
		})
		if err != nil {
			return team, err
		}
		_, err = syncer.Record(ctx, q, syncer.Change{
			WorkspaceID: team.WorkspaceID,
			Model:       api.SyncModelWorkflowState,
			ModelID:     state.ID,
			Action:      syncer.Insert,
			Data:        workflowStateRecord(state),
			Team:        &team,
		})
		if err != nil {
			return team, err
		}
	}

	if team.TriageEnabled {
		if err := ensureTriageState(ctx, q, team); err != nil {
			return team, err
		}
	}

	_, err = setTeamMember(ctx, q, team, creatorID, true)
	return team, err
}

// setTeamMember adds or removes a team member, logging the change if there
// was one. It returns the sync ID of the change, or 0 if nothing changed.
func setTeamMember(ctx context.Context, q *dbgen.Queries, team dbgen.Team, userID uuid.UUID, member bool) (int64, error) {
	var (
		changed int64
		err     error
		action  = syncer.Insert
	)
	if member {
		changed, err = q.AddTeamMember(ctx, dbgen.AddTeamMemberParams{TeamID: team.ID, UserID: userID})
	} else {
		action = syncer.Delete
		changed, err = q.RemoveTeamMember(ctx, dbgen.RemoveTeamMemberParams{TeamID: team.ID, UserID: userID})
	}
	if err != nil || changed == 0 {
		return 0, err
	}
	return syncer.Record(ctx, q, syncer.Change{
		WorkspaceID: team.WorkspaceID,
		Model:       api.SyncModelTeamMember,
		// The user's ID, so the sync socket can spot changes to its own access.
		ModelID: userID,
		Action:  action,
		Data:    api.TeamMemberRecord{TeamId: team.ID, UserId: userID},
		Team:    &team,
	})
}

// recordUserJoined logs a new member's record in the workspace they joined.
func recordUserJoined(ctx context.Context, q *dbgen.Queries, workspaceID uuid.UUID, user dbgen.User, role string, joinedAt time.Time) error {
	_, err := syncer.Record(ctx, q, syncer.Change{
		WorkspaceID: workspaceID,
		Model:       api.SyncModelUser,
		ModelID:     user.ID,
		Action:      syncer.Insert,
		Data:        userRecord(user, role, joinedAt),
	})
	return err
}

// recordUserChanged logs the user's updated record in every workspace they
// belong to.
func recordUserChanged(ctx context.Context, q *dbgen.Queries, user dbgen.User) error {
	// Ordered by workspace ID, so concurrent profile updates take the
	// workspace locks in the same order and can't deadlock.
	memberships, err := q.ListUserMemberships(ctx, user.ID)
	if err != nil {
		return err
	}
	for _, m := range memberships {
		_, err := syncer.Record(ctx, q, syncer.Change{
			WorkspaceID: m.WorkspaceID,
			Model:       api.SyncModelUser,
			ModelID:     user.ID,
			Action:      syncer.Update,
			Data:        userRecord(user, m.Role, m.JoinedAt),
		})
		if err != nil {
			return err
		}
	}
	return nil
}
