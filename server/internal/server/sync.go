package server

import (
	"context"
	"net/http"
	"net/url"
	"strconv"
	"time"

	"github.com/coder/websocket"
	"github.com/coder/websocket/wsjson"
	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"cruuze/server/internal/api"
	"cruuze/server/internal/db/dbgen"
	"cruuze/server/internal/syncer"
)

const (
	// syncBatchSize is how many log entries a socket reads and sends at a time.
	syncBatchSize = 500
	// syncPingInterval is how often an idle socket is pinged. Each ping also
	// re-reads the log, which covers any wake-up the hub missed.
	syncPingInterval = 30 * time.Second
	syncWriteTimeout = 10 * time.Second
	// A socket re-checks that its session still exists every this many pings.
	syncSessionCheckEvery = 10
)

func (s *Server) GetSyncBootstrap(w http.ResponseWriter, r *http.Request, workspaceId api.WorkspaceId) {
	role, ok := s.requireWorkspaceRole(w, r, workspaceId)
	if !ok {
		return
	}
	ctx := r.Context()

	// One snapshot for everything, so the records are exactly the state as of
	// lastSyncId.
	tx, err := s.pool.BeginTx(ctx, pgx.TxOptions{IsoLevel: pgx.RepeatableRead, AccessMode: pgx.ReadOnly})
	if err != nil {
		s.internalError(w, r, err)
		return
	}
	defer tx.Rollback(ctx) //nolint:errcheck // read-only

	out, err := loadBootstrap(ctx, dbgen.New(tx), workspaceId, currentSession(r).User.ID, role)
	if err != nil {
		s.internalError(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, out)
}

func loadBootstrap(ctx context.Context, q *dbgen.Queries, workspaceID, userID uuid.UUID, role string) (api.SyncBootstrap, error) {
	out := api.SyncBootstrap{
		Users:               []api.UserRecord{},
		Teams:               []api.TeamRecord{},
		TeamMembers:         []api.TeamMemberRecord{},
		WorkflowStates:      []api.WorkflowState{},
		Labels:              []api.Label{},
		Issues:              []api.Issue{},
		Comments:            []api.Comment{},
		Attachments:         []api.Attachment{},
		Notifications:       []api.Notification{},
		Projects:            []api.Project{},
		Milestones:          []api.Milestone{},
		Cycles:              []api.Cycle{},
		Relations:           []api.Relation{},
		Templates:           []api.Template{},
		Views:               []api.View{},
		Favorites:           []api.Favorite{},
		PullRequests:        []api.PullRequest{},
		TimeEntries:         []api.TimeEntry{},
		Initiatives:         []api.Initiative{},
		ProjectDependencies: []api.ProjectDependency{},
		ProjectUpdates:      []api.ProjectUpdate{},
		CustomerRequests:    []api.CustomerRequest{},
	}
	var err error
	if out.LastSyncId, err = q.GetLastSyncID(ctx, workspaceID); err != nil {
		return out, err
	}

	members, err := q.ListWorkspaceMembers(ctx, workspaceID)
	if err != nil {
		return out, err
	}
	for _, m := range members {
		out.Users = append(out.Users, userRecord(m.User, m.Role, m.JoinedAt))
	}

	teams, err := q.ListTeamsForUser(ctx, dbgen.ListTeamsForUserParams{
		WorkspaceID: workspaceID,
		UserID:      userID,
		MembersOnly: role == roleGuest,
	})
	if err != nil {
		return out, err
	}
	teamIDs := make([]uuid.UUID, 0, len(teams))
	for _, t := range teams {
		out.Teams = append(out.Teams, teamRecord(t.Team))
		teamIDs = append(teamIDs, t.Team.ID)
	}

	teamMembers, err := q.ListTeamMembersForTeams(ctx, teamIDs)
	if err != nil {
		return out, err
	}
	for _, tm := range teamMembers {
		out.TeamMembers = append(out.TeamMembers, api.TeamMemberRecord{TeamId: tm.TeamID, UserId: tm.UserID})
	}

	states, err := q.ListWorkflowStatesForTeams(ctx, teamIDs)
	if err != nil {
		return out, err
	}
	for _, st := range states {
		out.WorkflowStates = append(out.WorkflowStates, workflowStateRecord(st))
	}

	labels, err := q.ListLabels(ctx, workspaceID)
	if err != nil {
		return out, err
	}
	for _, l := range labels {
		out.Labels = append(out.Labels, labelRecord(l))
	}

	issues, err := q.ListActiveIssuesForTeams(ctx, teamIDs)
	if err != nil {
		return out, err
	}
	for _, i := range issues {
		out.Issues = append(out.Issues, issueRecord(i))
	}

	// Discussions grow without bound, so they are left out: a client loads an
	// issue's comments and activity when it opens the issue (GetIssueDetails).
	// The exception is what the inbox quotes.
	comments, err := q.ListCommentsForNotifications(ctx, dbgen.ListCommentsForNotificationsParams{
		WorkspaceID: workspaceID,
		UserID:      userID,
		TeamIds:     teamIDs,
	})
	if err != nil {
		return out, err
	}
	for _, c := range comments {
		out.Comments = append(out.Comments, commentRecord(c))
	}

	attachments, err := q.ListAttachmentsForTeams(ctx, teamIDs)
	if err != nil {
		return out, err
	}
	for _, a := range attachments {
		out.Attachments = append(out.Attachments, attachmentRecord(a))
	}

	notifications, err := q.ListNotificationsForUser(ctx, dbgen.ListNotificationsForUserParams{WorkspaceID: workspaceID, UserID: userID})
	if err != nil {
		return out, err
	}
	for _, n := range notifications {
		out.Notifications = append(out.Notifications, notificationRecord(n))
	}

	projects, err := q.ListProjects(ctx, workspaceID)
	if err != nil {
		return out, err
	}
	for _, p := range projects {
		out.Projects = append(out.Projects, projectRecord(p))
	}
	initiatives, err := q.ListInitiatives(ctx, workspaceID)
	if err != nil {
		return out, err
	}
	for _, i := range initiatives {
		out.Initiatives = append(out.Initiatives, initiativeRecord(i))
	}
	dependencies, err := q.ListProjectDependencies(ctx, workspaceID)
	if err != nil {
		return out, err
	}
	for _, d := range dependencies {
		out.ProjectDependencies = append(out.ProjectDependencies, projectDependencyRecord(d))
	}
	updates, err := q.ListRecentProjectUpdates(ctx, workspaceID)
	if err != nil {
		return out, err
	}
	for _, u := range updates {
		out.ProjectUpdates = append(out.ProjectUpdates, projectUpdateRecord(dbgen.ProjectUpdate(u)))
	}
	if role != roleGuest {
		// Customers' requests carry their addresses; guests don't get them.
		requests, err := q.ListCustomerRequests(ctx, workspaceID)
		if err != nil {
			return out, err
		}
		for _, r := range requests {
			out.CustomerRequests = append(out.CustomerRequests, customerRequestRecord(r))
		}
	}
	milestones, err := q.ListMilestones(ctx, workspaceID)
	if err != nil {
		return out, err
	}
	for _, m := range milestones {
		out.Milestones = append(out.Milestones, milestoneRecord(m))
	}
	cycles, err := q.ListCyclesForTeams(ctx, teamIDs)
	if err != nil {
		return out, err
	}
	for _, c := range cycles {
		out.Cycles = append(out.Cycles, cycleRecord(c))
	}
	relations, err := q.ListRelationsForTeams(ctx, teamIDs)
	if err != nil {
		return out, err
	}
	for _, rel := range relations {
		out.Relations = append(out.Relations, relationRecord(rel))
	}
	templates, err := q.ListTemplatesForTeams(ctx, teamIDs)
	if err != nil {
		return out, err
	}
	for _, t := range templates {
		out.Templates = append(out.Templates, templateRecord(t))
	}
	views, err := q.ListViewsForUser(ctx, dbgen.ListViewsForUserParams{WorkspaceID: workspaceID, OwnerID: userID})
	if err != nil {
		return out, err
	}
	for _, v := range views {
		out.Views = append(out.Views, viewRecord(v))
	}
	favorites, err := q.ListFavoritesForUser(ctx, dbgen.ListFavoritesForUserParams{WorkspaceID: workspaceID, UserID: userID})
	if err != nil {
		return out, err
	}
	for _, f := range favorites {
		out.Favorites = append(out.Favorites, favoriteRecord(f))
	}
	pullRequests, err := q.ListPullRequestsForTeams(ctx, teamIDs)
	if err != nil {
		return out, err
	}
	for _, p := range pullRequests {
		out.PullRequests = append(out.PullRequests, pullRequestRecord(p))
	}
	timeEntries, err := q.ListRecentTimeEntries(ctx, dbgen.ListRecentTimeEntriesParams{WorkspaceID: workspaceID, UserID: userID})
	if err != nil {
		return out, err
	}
	for _, t := range timeEntries {
		out.TimeEntries = append(out.TimeEntries, timeEntryRecord(t))
	}
	return out, nil
}

// GetIssueDetails returns an issue's comments, reactions and activity: the
// records the bootstrap leaves out.
func (s *Server) GetIssueDetails(w http.ResponseWriter, r *http.Request, issueId api.IssueId) {
	team, ok := s.requireIssueAccess(w, r, issueId)
	if !ok {
		return
	}
	ctx := r.Context()

	// One snapshot, so the records are exactly the state as of syncId and the
	// client can tell which later changes still apply.
	tx, err := s.pool.BeginTx(ctx, pgx.TxOptions{IsoLevel: pgx.RepeatableRead, AccessMode: pgx.ReadOnly})
	if err != nil {
		s.internalError(w, r, err)
		return
	}
	defer tx.Rollback(ctx) //nolint:errcheck // read-only
	q := dbgen.New(tx)

	out := api.IssueDetails{Comments: []api.Comment{}, Reactions: []api.Reaction{}, Activities: []api.Activity{}}
	err = func() error {
		var err error
		if out.SyncId, err = q.GetLastSyncID(ctx, team.WorkspaceID); err != nil {
			return err
		}
		comments, err := q.ListCommentsForIssue(ctx, issueId)
		if err != nil {
			return err
		}
		for _, c := range comments {
			out.Comments = append(out.Comments, commentRecord(c))
		}
		reactions, err := q.ListReactionsForIssue(ctx, issueId)
		if err != nil {
			return err
		}
		for _, re := range reactions {
			out.Reactions = append(out.Reactions, api.Reaction{CommentId: re.CommentID, UserId: re.UserID, Emoji: re.Emoji})
		}
		if out.InProgressSeconds, err = q.IssueTimeInProgress(ctx, issueId); err != nil {
			return err
		}
		activities, err := q.ListActivitiesForIssue(ctx, issueId)
		if err != nil {
			return err
		}
		for _, a := range activities {
			out.Activities = append(out.Activities, activityRecord(a))
		}
		return nil
	}()
	if err != nil {
		s.internalError(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, out)
}

// syncMessage is a frame on the sync socket (the SyncMessage API schema).
type syncMessage struct {
	Type       api.SyncMessageType `json:"type"`
	LastSyncID int64               `json:"lastSyncId,omitempty"`
	Actions    []syncer.Action     `json:"actions,omitempty"`
}

// syncSocket streams a workspace's sync log to one client over a WebSocket,
// starting after the sync ID the client says it has applied.
func (s *Server) syncSocket(w http.ResponseWriter, r *http.Request) {
	workspaceID, err := uuid.Parse(chi.URLParam(r, "workspaceId"))
	if err != nil {
		notFound(w)
		return
	}
	role, ok := s.requireWorkspaceRole(w, r, workspaceID)
	if !ok {
		return
	}
	cursor, err := strconv.ParseInt(r.URL.Query().Get("after"), 10, 64)
	if err != nil {
		writeError(w, http.StatusBadRequest, "bad_request", "after must be a sync ID.")
		return
	}
	sess := currentSession(r)
	access, err := syncer.LoadAccess(r.Context(), s.q, workspaceID, sess.User.ID, role)
	if err != nil {
		s.internalError(w, r, err)
		return
	}

	// Browsers send cookies with cross-site WebSocket handshakes, so the
	// Origin check here is what stops another site from reading the stream.
	conn, err := websocket.Accept(w, r, &websocket.AcceptOptions{OriginPatterns: []string{s.appHost()}})
	if err != nil {
		return // Accept has already responded
	}
	defer conn.CloseNow() //nolint:errcheck // already closed on the normal paths

	s.metrics.syncSockets.Add(1)
	defer s.metrics.syncSockets.Add(-1)

	// The client never sends data; reading only handles pings and the close.
	ctx := conn.CloseRead(r.Context())

	send := func(msg syncMessage) error {
		writeCtx, cancel := context.WithTimeout(ctx, syncWriteTimeout)
		defer cancel()
		return wsjson.Write(writeCtx, conn, msg)
	}
	reset := func() {
		if send(syncMessage{Type: api.SyncMessageTypeReset}) == nil {
			conn.Close(websocket.StatusNormalClosure, "reset") //nolint:errcheck
		}
	}

	if valid, err := syncer.CursorValid(ctx, s.q, workspaceID, cursor); err != nil {
		s.log.Error("sync socket: check cursor", "err", err)
		return
	} else if !valid {
		reset()
		return
	}
	// Replaying filters the log by what the client may see now. If that
	// changed since its cursor (a team went private, say), the actions from
	// before the change aren't its to have: it starts over instead.
	if changed, err := s.q.HasSyncResetAfter(ctx, dbgen.HasSyncResetAfterParams{WorkspaceID: workspaceID, SyncID: cursor}); err != nil {
		s.log.Error("sync socket: check resets", "err", err)
		return
	} else if changed {
		reset()
		return
	}

	// Subscribe before the first read so nothing committed in between is missed.
	wake, unsubscribe := s.hub.Subscribe(workspaceID)
	defer unsubscribe()
	ping := time.NewTicker(syncPingInterval)
	defer ping.Stop()

	for pings := 1; ; {
		for more := true; more; {
			batch, err := syncer.Fetch(ctx, s.q, workspaceID, access, cursor, syncBatchSize)
			if err != nil {
				if ctx.Err() == nil {
					s.log.Error("sync socket: read log", "err", err)
				}
				return
			}
			if batch.Reset {
				reset()
				return
			}
			if len(batch.Actions) > 0 {
				if err := send(syncMessage{Type: api.SyncMessageTypeActions, LastSyncID: batch.Cursor, Actions: batch.Actions}); err != nil {
					return
				}
			}
			cursor, more = batch.Cursor, batch.More
		}

		select {
		case <-ctx.Done():
			return
		case <-s.closing:
			conn.Close(websocket.StatusGoingAway, "server restarting") //nolint:errcheck
			return
		case <-wake:
		case <-ping.C:
			pingCtx, cancel := context.WithTimeout(ctx, syncWriteTimeout)
			err := conn.Ping(pingCtx)
			cancel()
			if err != nil {
				return
			}
			if pings%syncSessionCheckEvery == 0 {
				if !s.stillValid(ctx, sess) {
					conn.Close(websocket.StatusPolicyViolation, "signed out") //nolint:errcheck
					return
				}
			}
			pings++
		}
	}
}

// appHost is the host[:port] the web app is served from.
func (s *Server) appHost() string {
	u, err := url.Parse(s.cfg.AppURL)
	if err != nil {
		return ""
	}
	return u.Host
}
