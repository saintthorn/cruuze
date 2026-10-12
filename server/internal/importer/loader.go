package importer

import (
	"context"
	"fmt"
	"maps"
	"regexp"
	"slices"
	"strings"
	"sync"
	"time"
	"unicode/utf8"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"

	"cruuze/server/internal/blob"
	"cruuze/server/internal/db"
	"cruuze/server/internal/db/dbgen"
	"cruuze/server/internal/syncer"
)

// Limits the loader holds imported text to; they are the app's own.
const (
	maxTitleLen       = 500
	maxDescriptionLen = 100_000
	maxCommentLen     = 50_000
	maxNameLen        = 80
	maxWarnings       = 200
)

// Kinds of imported thing, as recorded in import_items.
const (
	kindTeam       = "team"
	kindState      = "state"
	kindLabel      = "label"
	kindProject    = "project"
	kindMilestone  = "milestone"
	kindCycle      = "cycle"
	kindIssue      = "issue"
	kindComment    = "comment"
	kindAttachment = "attachment"
)

var (
	teamKeyPattern = regexp.MustCompile(`^[A-Z][A-Z0-9]{0,6}$`)
	colorPattern   = regexp.MustCompile(`^#[0-9a-fA-F]{6}$`)
	stateKinds     = []string{KindTriage, KindBacklog, KindUnstarted, KindStarted, KindCompleted, KindCanceled}
	projectStates  = []string{"backlog", "planned", "started", "paused", "completed", "canceled"}
	palette        = []string{"#5e6ad2", "#26b5ce", "#4cb782", "#f2c94c", "#f2994a", "#eb5757", "#bb87fc", "#95a2b3"}
)

// defaultStates are what a team gets when the source brings no statuses of
// its own, and what fills in a kind the source's workflow lacks.
var defaultStates = []State{
	{Name: "Backlog", Kind: KindBacklog, Color: "#95a2b3"},
	{Name: "Todo", Kind: KindUnstarted, Color: "#e2e2e2"},
	{Name: "In Progress", Kind: KindStarted, Color: "#f2c94c"},
	{Name: "Done", Kind: KindCompleted, Color: "#5e6ad2"},
	{Name: "Canceled", Kind: KindCanceled, Color: "#95a2b3"},
}

// Options are the choices made in the import wizard.
type Options struct {
	// TeamMap sends a source team (by its ID) into an existing team instead
	// of the one that matches by key, or a new one.
	TeamMap map[string]uuid.UUID
	// UserMap says who a source user (by their ID) is here. A nil value
	// means nobody: their work is left unassigned and credited by name.
	// Users not in the map are matched by email.
	UserMap map[string]*uuid.UUID
	// GitHubLogins are the members who have said who they are on GitHub,
	// by lowercased username.
	GitHubLogins map[string]uuid.UUID
	// AsProjects brings each source team in as a project of the team it is
	// sent to, rather than as a team of its own: its issues join that team
	// and are filed under a project named after it.
	AsProjects bool
}

// Loader writes bundles into a workspace on behalf of one import.
//
// It writes records directly, without a sync log entry apiece: an import can
// be tens of thousands of records, and no client could use them as a stream.
// Finish tells every client to load the workspace afresh instead.
type Loader struct {
	pool        *pgxpool.Pool
	workspaceID uuid.UUID
	importID    uuid.UUID
	actorID     uuid.UUID
	origin      string
	opts        Options
	now         time.Time

	members     map[uuid.UUID]bool // workspace members
	people      *People            // who a source user is here
	teams       map[uuid.UUID]dbgen.Team
	hidden      map[uuid.UUID]bool                  // teams the actor can't see
	states      map[uuid.UUID][]dbgen.WorkflowState // by team
	labels      map[string]uuid.UUID                // by lowercased name
	projects    map[string]uuid.UUID                // by lowercased name
	milestones  map[uuid.UUID]map[string]uuid.UUID  // by project, by lowercased name
	milestoneOf map[uuid.UUID]uuid.UUID             // milestone → its project

	// mapped is what this origin's IDs have become, by kind.
	mapped map[string]map[string]uuid.UUID
	// users resolves a source user; a nil entry is "nobody here".
	users     map[string]*uuid.UUID
	userNames map[string]string
	plans     map[string]*teamPlan // by source team ID

	parents   []pendingLink
	relations []pendingRelation
	// queued are rows that nothing reads back while a bundle loads; they
	// are sent together rather than one round trip each (see flush).
	queued struct {
		items      []dbgen.UpsertImportItemsParams
		activities []dbgen.ImportIssueActivitiesParams
		history    []dbgen.InsertIssueStateHistoriesParams
		comments   []dbgen.ImportCommentsParams
		edits      []dbgen.UpdateImportedCommentsParams
	}
	// files are attachments to copy once their issues are written; store
	// and source say where to and from (see CopyFiles).
	files   []pendingFiles
	store   blob.Store
	source  Source
	copier  fileCopier
	touched map[uuid.UUID]bool // teams that received issues

	// mu guards Counts and Warnings, which are read for progress reports
	// while the import runs.
	mu sync.Mutex
	// Counts is how many of each thing were created or updated.
	Counts map[string]int
	// Warnings are things the person should know that didn't stop the import.
	Warnings []string
	warned   map[string]bool
}

// inc counts one more of something.
func (l *Loader) inc(what string) {
	l.mu.Lock()
	l.Counts[what]++
	l.mu.Unlock()
}

// Progress is the counts so far, safe to call while the import runs.
func (l *Loader) Progress() map[string]int {
	l.mu.Lock()
	defer l.mu.Unlock()
	return maps.Clone(l.Counts)
}

// teamPlan is where a source team's issues go and how.
type teamPlan struct {
	team dbgen.Team
	// keepNumbers: the team has no issues but this origin's, so imported
	// issues keep the numbers they had.
	keepNumbers bool
	states      map[string]dbgen.WorkflowState // source state ID → state
	minSort     float64
	// foreignCycles: the team had cycles of its own, which imported ones
	// would collide with.
	foreignCycles bool
	// project: the project the source team's issues are filed under, when
	// source teams come in as projects.
	project *uuid.UUID
}

type pendingLink struct {
	issue  uuid.UUID
	parent string
}

type pendingRelation struct {
	issue uuid.UUID
	rel   Relation
}

// NewLoader prepares to import from origin into the workspace as actor.
func NewLoader(ctx context.Context, pool *pgxpool.Pool, workspaceID, importID, actorID uuid.UUID, origin string, opts Options) (*Loader, error) {
	l := &Loader{
		pool: pool, workspaceID: workspaceID, importID: importID, actorID: actorID, origin: origin, opts: opts,
		now:         time.Now().UTC(),
		members:     map[uuid.UUID]bool{},
		people:      NewPeople(actorID),
		teams:       map[uuid.UUID]dbgen.Team{},
		hidden:      map[uuid.UUID]bool{},
		states:      map[uuid.UUID][]dbgen.WorkflowState{},
		labels:      map[string]uuid.UUID{},
		projects:    map[string]uuid.UUID{},
		milestones:  map[uuid.UUID]map[string]uuid.UUID{},
		milestoneOf: map[uuid.UUID]uuid.UUID{},
		mapped:      map[string]map[string]uuid.UUID{},
		users:       map[string]*uuid.UUID{},
		userNames:   map[string]string{},
		plans:       map[string]*teamPlan{},
		touched:     map[uuid.UUID]bool{},
		Counts:      map[string]int{},
		warned:      map[string]bool{},
	}
	q := dbgen.New(pool)
	members, err := q.ListWorkspaceMembers(ctx, workspaceID)
	if err != nil {
		return nil, err
	}
	for _, m := range members {
		l.members[m.User.ID] = true
		l.people.Add(m.User.ID, m.User.Email, m.User.Name, m.User.GithubLogin)
	}
	for login, id := range opts.GitHubLogins {
		if l.members[id] {
			l.people.Add(id, "", "", login)
		}
	}
	teams, err := q.ListTeamsOfWorkspace(ctx, workspaceID)
	if err != nil {
		return nil, err
	}
	for _, t := range teams {
		l.teams[t.ID] = t
		l.hidden[t.ID] = true
	}
	// An import reaches no further than the person running it: a private
	// team they aren't in is left alone, along with what is in it.
	visible, err := q.ListTeamsForUser(ctx, dbgen.ListTeamsForUserParams{WorkspaceID: workspaceID, UserID: actorID})
	if err != nil {
		return nil, err
	}
	for _, t := range visible {
		delete(l.hidden, t.Team.ID)
	}
	states, err := q.ListWorkflowStatesOfWorkspace(ctx, workspaceID)
	if err != nil {
		return nil, err
	}
	for _, s := range states {
		l.states[s.TeamID] = append(l.states[s.TeamID], s)
	}
	labels, err := q.ListLabels(ctx, workspaceID)
	if err != nil {
		return nil, err
	}
	for _, label := range labels {
		l.labels[strings.ToLower(label.Name)] = label.ID
	}
	projects, err := q.ListProjects(ctx, workspaceID)
	if err != nil {
		return nil, err
	}
	projectIDs := map[uuid.UUID]bool{}
	for _, p := range projects {
		l.projects[strings.ToLower(p.Name)] = p.ID
		projectIDs[p.ID] = true
	}
	milestones, err := q.ListMilestones(ctx, workspaceID)
	if err != nil {
		return nil, err
	}
	milestoneIDs := map[uuid.UUID]bool{}
	for _, m := range milestones {
		if l.milestones[m.ProjectID] == nil {
			l.milestones[m.ProjectID] = map[string]uuid.UUID{}
		}
		l.milestones[m.ProjectID][strings.ToLower(m.Name)] = m.ID
		l.milestoneOf[m.ID] = m.ProjectID
		milestoneIDs[m.ID] = true
	}
	labelIDs := map[uuid.UUID]bool{}
	for _, id := range l.labels {
		labelIDs[id] = true
	}
	stateIDs := map[uuid.UUID]bool{}
	for _, s := range states {
		stateIDs[s.ID] = !l.hidden[s.TeamID]
	}
	teamIDs := map[uuid.UUID]bool{}
	for id := range l.teams {
		teamIDs[id] = !l.hidden[id]
	}
	attachmentIDs := map[uuid.UUID]bool{}
	attachments, err := q.ListAttachmentIDs(ctx, workspaceID)
	if err != nil {
		return nil, err
	}
	for _, id := range attachments {
		attachmentIDs[id] = true
	}
	cycles, err := q.ListCyclesForTeams(ctx, keys(teamIDs))
	if err != nil {
		return nil, err
	}
	cycleIDs := map[uuid.UUID]bool{}
	for _, c := range cycles {
		cycleIDs[c.ID] = !l.hidden[c.TeamID]
	}

	// What this origin brought in before. A mapping to something that has
	// since been deleted is forgotten, and the thing is made again.
	exists := map[string]map[uuid.UUID]bool{
		kindTeam: teamIDs, kindState: stateIDs, kindLabel: labelIDs,
		kindProject: projectIDs, kindMilestone: milestoneIDs, kindCycle: cycleIDs, kindAttachment: attachmentIDs,
	}
	for kind, live := range exists {
		items, err := q.ListImportItems(ctx, dbgen.ListImportItemsParams{WorkspaceID: workspaceID, Origin: origin, Kind: kind})
		if err != nil {
			return nil, err
		}
		l.mapped[kind] = map[string]uuid.UUID{}
		for _, item := range items {
			if live[item.RecordID] {
				l.mapped[kind][item.ExternalID] = item.RecordID
			}
		}
	}
	l.mapped[kindIssue] = map[string]uuid.UUID{}
	return l, nil
}

func keys[K comparable, V any](m map[K]V) []K {
	out := make([]K, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	return out
}

// warn records something the person should know, once.
func (l *Loader) warn(format string, args ...any) {
	message := fmt.Sprintf(format, args...)
	l.mu.Lock()
	defer l.mu.Unlock()
	if l.warned[message] {
		return
	}
	l.warned[message] = true
	if len(l.Warnings) < maxWarnings {
		l.Warnings = append(l.Warnings, message)
	}
}

// remember records what a source ID became. created says this import made
// the record, so undoing the import removes it. The row is written with the
// rest of the bundle (see flush); the loader itself knows at once.
func (l *Loader) remember(kind, externalID string, recordID uuid.UUID, created bool, sourceUpdated *time.Time) {
	if l.mapped[kind] != nil {
		l.mapped[kind][externalID] = recordID
	}
	l.queued.items = append(l.queued.items, l.item(kind, externalID, recordID, created, sourceUpdated))
}

func (l *Loader) item(kind, externalID string, recordID uuid.UUID, created bool, sourceUpdated *time.Time) dbgen.UpsertImportItemsParams {
	params := dbgen.UpsertImportItemsParams{
		WorkspaceID: l.workspaceID, Origin: l.origin, Kind: kind, ExternalID: externalID,
		RecordID: recordID, SourceUpdatedAt: sourceUpdated,
	}
	if created {
		params.CreatedBy = &l.importID
	}
	return params
}

// flush writes the queued rows, each kind in one round trip. The order is
// the order they depend on each other in: a comment's item after the comment.
func (l *Loader) flush(ctx context.Context, q *dbgen.Queries) error {
	var failed error
	note := func(_ int, err error) {
		if err != nil && failed == nil {
			failed = err
		}
	}
	pending := l.queued
	l.queued.items, l.queued.activities, l.queued.history, l.queued.comments, l.queued.edits = nil, nil, nil, nil, nil
	if len(pending.activities) > 0 {
		q.ImportIssueActivities(ctx, pending.activities).Exec(note)
	}
	if failed == nil && len(pending.history) > 0 {
		q.InsertIssueStateHistories(ctx, pending.history).Exec(note)
	}
	if failed == nil && len(pending.comments) > 0 {
		q.ImportComments(ctx, pending.comments).Exec(note)
	}
	if failed == nil && len(pending.edits) > 0 {
		q.UpdateImportedComments(ctx, pending.edits).Exec(note)
	}
	if failed == nil && len(pending.items) > 0 {
		q.UpsertImportItems(ctx, pending.items).Exec(note)
	}
	return failed
}

// clip shortens text to at most max bytes without cutting a character in two.
func clip(text string, max int) string {
	if len(text) <= max {
		return text
	}
	text = text[:max]
	for !utf8.ValidString(text) {
		text = text[:len(text)-1]
	}
	return text
}

func cleanName(raw, fallback string) string {
	name := strings.Join(strings.Fields(raw), " ")
	if name == "" {
		name = fallback
	}
	return clip(name, maxNameLen)
}

func parseDay(raw string) *time.Time {
	if raw == "" {
		return nil
	}
	// A full timestamp is fine too; the day is what counts.
	if len(raw) > len(time.DateOnly) {
		raw = raw[:len(time.DateOnly)]
	}
	day, err := time.Parse(time.DateOnly, raw)
	if err != nil {
		return nil
	}
	return &day
}

func color(raw string, n int) string {
	if !strings.HasPrefix(raw, "#") && raw != "" {
		raw = "#" + raw
	}
	if colorPattern.MatchString(raw) {
		return strings.ToLower(raw)
	}
	return palette[n%len(palette)]
}

// Load writes one bundle. It is one transaction: a bundle is imported whole
// or not at all. Its attachments are copied afterwards, in the background.
func (l *Loader) Load(ctx context.Context, b Bundle) error {
	l.files = nil
	// Nothing queued by a bundle that failed is written with this one.
	l.queued.items, l.queued.activities, l.queued.history, l.queued.comments, l.queued.edits = nil, nil, nil, nil, nil
	err := db.InTx(ctx, l.pool, func(q *dbgen.Queries) error {
		l.loadUsers(b.Users)
		for _, team := range b.Teams {
			if _, err := l.planTeam(ctx, q, team); err != nil {
				return err
			}
		}
		for _, label := range b.Labels {
			if _, err := l.loadLabel(ctx, q, label); err != nil {
				return err
			}
		}
		for _, project := range b.Projects {
			if err := l.loadProject(ctx, q, project); err != nil {
				return err
			}
		}
		for _, cycle := range b.Cycles {
			if err := l.loadCycle(ctx, q, cycle); err != nil {
				return err
			}
		}
		if err := l.loadIssues(ctx, q, b.Issues); err != nil {
			return err
		}
		// Comments that come apart from their issues look up what the
		// issues' own comments became.
		if err := l.flush(ctx, q); err != nil {
			return err
		}
		if err := l.loadComments(ctx, q, b.Comments); err != nil {
			return err
		}
		return l.flush(ctx, q)
	})
	if err != nil {
		return err
	}
	return l.copyFiles(ctx)
}

// --- People ----------------------------------------------------------------------

func (l *Loader) loadUsers(users []User) {
	for _, u := range users {
		if u.ID == "" {
			continue
		}
		name := u.Name
		if name == "" {
			name = u.Email
		}
		l.userNames[u.ID] = name
		if choice, chosen := l.opts.UserMap[u.ID]; chosen {
			if choice != nil && !l.members[*choice] {
				choice = nil // not a member (any more): nobody
			}
			l.users[u.ID] = choice
			continue
		}
		l.users[u.ID] = l.people.Match(u)
	}
}

// user resolves a source user to a member, or nil for nobody.
func (l *Loader) user(externalID string) *uuid.UUID {
	if externalID == "" {
		return nil
	}
	if id, known := l.users[externalID]; known {
		return id
	}
	// Never listed: the wizard's choice still applies.
	if choice := l.opts.UserMap[externalID]; choice != nil && l.members[*choice] {
		return choice
	}
	return nil
}

// credit is how to name a source user in text.
func (l *Loader) credit(externalID string) string {
	if name := l.userNames[externalID]; name != "" {
		return name
	}
	return externalID
}

// --- Teams and their statuses --------------------------------------------------------

func (l *Loader) planTeam(ctx context.Context, q *dbgen.Queries, src Team) (*teamPlan, error) {
	if src.ID == "" {
		return nil, userError("A team in the import has no ID.")
	}
	plan := l.plans[src.ID]
	if plan == nil {
		team, err := l.resolveTeam(ctx, q, src)
		if err != nil {
			return nil, err
		}
		plan = &teamPlan{team: team, states: map[string]dbgen.WorkflowState{}}
		foreign, err := q.CountForeignIssuesInTeam(ctx, dbgen.CountForeignIssuesInTeamParams{TeamID: team.ID, Origin: l.origin})
		if err != nil {
			return nil, err
		}
		plan.keepNumbers = foreign == 0
		if !plan.keepNumbers {
			l.warn("%s already had issues of its own, so imported issues were given new numbers there.", team.Key)
		}
		if plan.minSort, err = q.MinIssueSortOrder(ctx, team.ID); err != nil {
			return nil, err
		}
		cycles, err := q.ListCyclesForTeam(ctx, team.ID)
		if err != nil {
			return nil, err
		}
		for _, c := range cycles {
			if !l.isMapped(kindCycle, c.ID) {
				plan.foreignCycles = true
			}
		}
		if l.opts.AsProjects {
			if plan.project, err = l.teamProject(ctx, q, src, team); err != nil {
				return nil, err
			}
		}
		l.plans[src.ID] = plan
	}
	if err := l.loadStates(ctx, q, plan, src.States); err != nil {
		return nil, err
	}
	return plan, nil
}

// teamProject finds or makes the project that stands for a source team when
// teams come in as projects: the one it had last time, one of the same name,
// or a new one.
func (l *Loader) teamProject(ctx context.Context, q *dbgen.Queries, src Team, team dbgen.Team) (*uuid.UUID, error) {
	externalID := "team:" + src.ID
	if id, ok := l.mapped[kindProject][externalID]; ok {
		return &id, nil
	}
	name := cleanName(src.Name, team.Key)
	if id, ok := l.projects[strings.ToLower(name)]; ok {
		l.remember(kindProject, externalID, id, false, nil)
		return &id, nil
	}
	project, err := q.CreateProject(ctx, dbgen.CreateProjectParams{
		ID: newID(), WorkspaceID: l.workspaceID, Name: name, Status: "started",
		Color: palette[len(l.projects)%len(palette)],
	})
	if err != nil {
		return nil, err
	}
	l.projects[strings.ToLower(name)] = project.ID
	l.inc("projects")
	l.remember(kindProject, externalID, project.ID, true, nil)
	return &project.ID, nil
}

// isMapped reports whether a record is one this origin brought in.
func (l *Loader) isMapped(kind string, recordID uuid.UUID) bool {
	for _, id := range l.mapped[kind] {
		if id == recordID {
			return true
		}
	}
	return false
}

// resolveTeam finds where a source team goes: where the wizard said, where
// it went last time, the team with the same key, or a new team.
func (l *Loader) resolveTeam(ctx context.Context, q *dbgen.Queries, src Team) (dbgen.Team, error) {
	if id, chosen := l.opts.TeamMap[src.ID]; chosen {
		team, ok := l.teams[id]
		if !ok || l.hidden[id] {
			return team, userError(fmt.Sprintf("The team chosen for %q doesn't exist in this workspace.", src.Name))
		}
		l.remember(kindTeam, src.ID, team.ID, false, nil)
		return team, nil
	}
	if id, ok := l.mapped[kindTeam][src.ID]; ok {
		return l.teams[id], nil
	}
	key := teamKey(src.Key, src.Name)
	for _, team := range l.teams {
		if team.Key == key && l.hidden[team.ID] {
			return team, userError(fmt.Sprintf("The key %s is taken. Choose a team for %q.", key, src.Name))
		}
		if team.Key == key {
			l.remember(kindTeam, src.ID, team.ID, false, nil)
			return team, nil
		}
	}
	if l.opts.AsProjects {
		return dbgen.Team{}, userError(fmt.Sprintf("Choose which team %q becomes a project of.", src.Name))
	}
	team, err := q.CreateTeam(ctx, dbgen.CreateTeamParams{
		ID: newID(), WorkspaceID: l.workspaceID, Name: cleanName(src.Name, key), Key: key, Kind: "engineering",
	})
	if err != nil {
		return team, err
	}
	// Whoever runs the import is in the teams it makes.
	if _, err := q.AddTeamMember(ctx, dbgen.AddTeamMemberParams{TeamID: team.ID, UserID: l.actorID}); err != nil {
		return team, err
	}
	l.teams[team.ID] = team
	l.inc("teams")
	l.remember(kindTeam, src.ID, team.ID, true, nil)
	return team, nil
}

// teamKey makes a valid team key from the source's, or from the name.
func teamKey(key, name string) string {
	clean := func(raw string) string {
		var b strings.Builder
		for _, r := range strings.ToUpper(raw) {
			if (r >= 'A' && r <= 'Z') || (r >= '0' && r <= '9' && b.Len() > 0) {
				b.WriteRune(r)
			}
			if b.Len() == 7 {
				break
			}
		}
		return b.String()
	}
	if k := clean(key); teamKeyPattern.MatchString(k) {
		return k
	}
	if k := clean(name); teamKeyPattern.MatchString(k) {
		return k
	}
	return "IMP"
}

func (l *Loader) loadStates(ctx context.Context, q *dbgen.Queries, plan *teamPlan, states []State) error {
	team := plan.team
	create := func(src State) (dbgen.WorkflowState, error) {
		position, err := q.MaxWorkflowStatePosition(ctx, team.ID)
		if err != nil {
			return dbgen.WorkflowState{}, err
		}
		state, err := q.CreateWorkflowState(ctx, dbgen.CreateWorkflowStateParams{
			ID: newID(), WorkspaceID: l.workspaceID, TeamID: team.ID,
			Name: cleanName(src.Name, "Status"), Type: src.Kind, Color: color(src.Color, len(l.states[team.ID])), Position: position + 1,
		})
		if err != nil {
			return state, err
		}
		l.states[team.ID] = append(l.states[team.ID], state)
		l.inc("statuses")
		return state, nil
	}
	for _, src := range states {
		if src.ID == "" || plan.states[src.ID].ID != uuid.Nil {
			continue
		}
		if !slices.Contains(stateKinds, src.Kind) {
			return userError(fmt.Sprintf("Status %q has kind %q; kinds are %s.", src.Name, src.Kind, strings.Join(stateKinds, ", ")))
		}
		// One it became before, one of the same name, or a new one.
		if id, ok := l.mapped[kindState][src.ID]; ok {
			if i := slices.IndexFunc(l.states[team.ID], func(s dbgen.WorkflowState) bool { return s.ID == id }); i >= 0 {
				plan.states[src.ID] = l.states[team.ID][i]
				continue
			}
		}
		if i := slices.IndexFunc(l.states[team.ID], func(s dbgen.WorkflowState) bool { return strings.EqualFold(s.Name, strings.TrimSpace(src.Name)) }); i >= 0 {
			plan.states[src.ID] = l.states[team.ID][i]
			l.remember(kindState, src.ID, l.states[team.ID][i].ID, false, nil)
			continue
		}
		state, err := create(src)
		if err != nil {
			return err
		}
		plan.states[src.ID] = state
		l.remember(kindState, src.ID, state.ID, true, nil)
	}
	// The app counts on every team having somewhere to put new work, work in
	// progress, finished work and abandoned work.
	for _, kind := range []string{KindUnstarted, KindStarted, KindCompleted, KindCanceled} {
		if l.firstState(team.ID, kind) != nil || (kind == KindUnstarted && l.firstState(team.ID, KindBacklog) != nil) {
			continue
		}
		i := slices.IndexFunc(defaultStates, func(s State) bool { return s.Kind == kind })
		state, err := create(defaultStates[i])
		if err != nil {
			return err
		}
		// Recorded so that undoing the import takes it away again.
		l.remember(kindState, "default:"+team.ID.String()+":"+kind, state.ID, true, nil)
	}
	return nil
}

// firstState is a team's first status of a kind, in workflow order.
func (l *Loader) firstState(teamID uuid.UUID, kind string) *dbgen.WorkflowState {
	var first *dbgen.WorkflowState
	for i, s := range l.states[teamID] {
		if s.Type == kind && (first == nil || s.Position < first.Position) {
			first = &l.states[teamID][i]
		}
	}
	return first
}

// --- Labels, projects, cycles ------------------------------------------------------------

func (l *Loader) loadLabel(ctx context.Context, q *dbgen.Queries, src Label) (uuid.UUID, error) {
	name := cleanName(src.Name, "")
	if name == "" {
		return uuid.Nil, nil
	}
	externalID := src.ID
	if externalID == "" {
		externalID = strings.ToLower(name)
	}
	if id, ok := l.mapped[kindLabel][externalID]; ok {
		return id, nil
	}
	if id, ok := l.labels[strings.ToLower(name)]; ok {
		l.remember(kindLabel, externalID, id, false, nil)
		return id, nil
	}
	label, err := q.CreateLabel(ctx, dbgen.CreateLabelParams{ID: newID(), WorkspaceID: l.workspaceID, Name: name, Color: color(src.Color, len(l.labels))})
	if err != nil {
		return uuid.Nil, err
	}
	l.labels[strings.ToLower(name)] = label.ID
	l.inc("labels")
	l.remember(kindLabel, externalID, label.ID, true, nil)
	return label.ID, nil
}

func (l *Loader) loadProject(ctx context.Context, q *dbgen.Queries, src Project) error {
	name := cleanName(src.Name, "")
	if src.ID == "" || name == "" {
		return nil
	}
	projectID, known := l.mapped[kindProject][src.ID]
	if !known {
		if id, ok := l.projects[strings.ToLower(name)]; ok {
			projectID = id
			l.remember(kindProject, src.ID, id, false, nil)
		} else {
			status := src.Status
			if !slices.Contains(projectStates, status) {
				status = "backlog"
			}
			project, err := q.CreateProject(ctx, dbgen.CreateProjectParams{
				ID: newID(), WorkspaceID: l.workspaceID, Name: name, Description: clip(src.Description, maxDescriptionLen),
				Status: status, LeadID: l.user(src.Lead), StartDate: parseDay(src.StartDate), TargetDate: parseDay(src.TargetDate),
				Color: palette[len(l.projects)%len(palette)],
			})
			if err != nil {
				return err
			}
			projectID = project.ID
			l.projects[strings.ToLower(name)] = projectID
			l.inc("projects")
			l.remember(kindProject, src.ID, projectID, true, nil)
		}
	}
	for i, m := range src.Milestones {
		name := cleanName(m.Name, "")
		if m.ID == "" || name == "" {
			continue
		}
		if _, ok := l.mapped[kindMilestone][m.ID]; ok {
			continue
		}
		if id, ok := l.milestones[projectID][strings.ToLower(name)]; ok {
			l.remember(kindMilestone, m.ID, id, false, nil)
			continue
		}
		milestone, err := q.CreateMilestone(ctx, dbgen.CreateMilestoneParams{
			ID: newID(), WorkspaceID: l.workspaceID, ProjectID: projectID, Name: name,
			TargetDate: parseDay(m.TargetDate), SortOrder: float64(len(l.milestones[projectID]) + i + 1),
		})
		if err != nil {
			return err
		}
		if l.milestones[projectID] == nil {
			l.milestones[projectID] = map[string]uuid.UUID{}
		}
		l.milestones[projectID][strings.ToLower(name)] = milestone.ID
		l.milestoneOf[milestone.ID] = projectID
		l.inc("milestones")
		l.remember(kindMilestone, m.ID, milestone.ID, true, nil)
	}
	return nil
}

func (l *Loader) loadCycle(ctx context.Context, q *dbgen.Queries, src Cycle) error {
	plan := l.plans[src.Team]
	if src.ID == "" || plan == nil {
		return nil
	}
	if _, ok := l.mapped[kindCycle][src.ID]; ok {
		return nil
	}
	starts, ends := parseDay(src.StartsAt), parseDay(src.EndsAt)
	if starts == nil || ends == nil || ends.Before(*starts) || src.Number <= 0 {
		l.warn("A cycle of %s has no usable dates and was left out.", plan.team.Key)
		return nil
	}
	if plan.foreignCycles {
		l.warn("%s already had cycles of its own, so the source's cycles were not imported there.", plan.team.Key)
		return nil
	}
	cycle, err := q.ImportCycle(ctx, dbgen.ImportCycleParams{
		ID: newID(), WorkspaceID: l.workspaceID, TeamID: plan.team.ID, Number: src.Number,
		Name: cleanName(src.Name, ""), StartsAt: *starts, EndsAt: *ends,
	})
	if db.IsNoRows(err) {
		l.warn("%s already has a cycle %d; the source's was left out.", plan.team.Key, src.Number)
		return nil
	}
	if err != nil {
		return err
	}
	l.inc("cycles")
	l.remember(kindCycle, src.ID, cycle.ID, true, nil)
	return nil
}

// --- Issues ----------------------------------------------------------------------------

func (l *Loader) loadIssues(ctx context.Context, q *dbgen.Queries, issues []Issue) error {
	if len(issues) == 0 {
		return nil
	}
	externalIDs := make([]string, 0, len(issues))
	var commentIDs []string
	for _, issue := range issues {
		externalIDs = append(externalIDs, issue.ID)
		for _, c := range issue.Comments {
			commentIDs = append(commentIDs, c.ID)
		}
	}
	known, err := q.ListImportedIssues(ctx, dbgen.ListImportedIssuesParams{WorkspaceID: l.workspaceID, Origin: l.origin, ExternalIds: externalIDs, HiddenTeamIds: keys(l.hidden)})
	if err != nil {
		return err
	}
	existing := make(map[string]dbgen.ListImportedIssuesRow, len(known))
	for _, row := range known {
		existing[row.ExternalID] = row
	}
	comments := map[string]uuid.UUID{}
	if len(commentIDs) > 0 {
		rows, err := q.ListImportedComments(ctx, dbgen.ListImportedCommentsParams{WorkspaceID: l.workspaceID, Origin: l.origin, ExternalIds: commentIDs, HiddenTeamIds: keys(l.hidden)})
		if err != nil {
			return err
		}
		for _, row := range rows {
			comments[row.ExternalID] = row.RecordID
		}
	}
	for _, issue := range issues {
		prior, seen := existing[issue.ID]
		var before *dbgen.ListImportedIssuesRow
		if seen {
			before = &prior
		}
		if err := l.loadIssue(ctx, q, issue, before, comments); err != nil {
			return fmt.Errorf("issue %q: %w", issue.ID, err)
		}
	}
	return nil
}

func (l *Loader) loadIssue(ctx context.Context, q *dbgen.Queries, src Issue, before *dbgen.ListImportedIssuesRow, comments map[string]uuid.UUID) error {
	if src.ID == "" {
		return userError("An issue in the import has no ID.")
	}
	plan := l.plans[src.Team]
	if plan == nil {
		return userError(fmt.Sprintf("Issue %q is in team %q, which the import doesn't describe.", src.Title, src.Team))
	}
	team := plan.team

	created := src.CreatedAt
	if created.IsZero() {
		created = l.now
	}
	updated := src.UpdatedAt
	if updated.Before(created) {
		updated = created
	}
	state := l.issueState(plan, src)
	history := l.history(plan, src, state, created, updated)
	started, completed := src.StartedAt, src.CompletedAt
	// The dates follow from the history where there is one.
	for _, h := range history {
		if h.kind == KindStarted && started == nil {
			at := h.at
			started = &at
		}
	}
	closed := state.Type == KindCompleted || state.Type == KindCanceled
	if closed && completed == nil {
		at := history[len(history)-1].at
		completed = &at
	}
	if !closed {
		completed = nil
	}
	if state.Type == KindStarted && started == nil {
		started = &updated
	}
	// Old closed issues go straight to the archive, where the team's own
	// settings would have put them by now.
	var archived *time.Time
	switch {
	case src.Archived:
		archived = &updated
	case closed && team.AutoArchiveDays > 0:
		if due := completed.AddDate(0, 0, int(team.AutoArchiveDays)); due.Before(l.now) {
			archived = &due
		}
	}

	var labelIDs []uuid.UUID
	for _, name := range src.Labels {
		id, ok := l.mapped[kindLabel][name]
		if !ok {
			// A label named on the issue but never listed: by its name.
			var err error
			if id, err = l.loadLabel(ctx, q, Label{Name: name}); err != nil {
				return err
			}
		}
		if id != uuid.Nil && !slices.Contains(labelIDs, id) {
			labelIDs = append(labelIDs, id)
		}
	}
	if labelIDs == nil {
		labelIDs = []uuid.UUID{}
	}
	var projectID, milestoneID, cycleID *uuid.UUID
	if id, ok := l.mapped[kindProject][src.Project]; ok && src.Project != "" {
		projectID = &id
	}
	if projectID == nil {
		projectID = plan.project
	}
	if id, ok := l.mapped[kindMilestone][src.Milestone]; ok && src.Milestone != "" {
		// A milestone brings its project along.
		owner := l.milestoneOf[id]
		projectID, milestoneID = &owner, &id
	}
	if id, ok := l.mapped[kindCycle][src.Cycle]; ok && src.Cycle != "" {
		cycleID = &id
	}
	priority := int16(src.Priority)
	if priority < 0 || priority > 4 {
		priority = 0
	}
	title := clip(strings.Join(strings.Fields(src.Title), " "), maxTitleLen)
	if title == "" {
		title = "(untitled)"
	}
	assignee, creator := l.user(src.Assignee), l.user(src.Creator)
	description := l.description(src, creator == nil)

	var issueID uuid.UUID
	unchanged := false
	switch {
	case before == nil:
		issueID = newID()
		number := src.Number
		if plan.keepNumbers && number > 0 {
			taken, err := q.IssueNumberTaken(ctx, dbgen.IssueNumberTakenParams{TeamID: team.ID, Number: number})
			if err != nil {
				return err
			}
			if taken {
				number = 0
				l.warn("Some issues in %s were given new numbers because theirs were taken.", team.Key)
			}
		} else {
			number = 0
		}
		if number == 0 {
			var err error
			// Past anything already there, imported or not.
			if err = q.RaiseNextIssueNumber(ctx, team.ID); err != nil {
				return err
			}
			if number, err = q.NextIssueNumber(ctx, team.ID); err != nil {
				return err
			}
		}
		subscribers := []uuid.UUID{}
		for _, id := range []*uuid.UUID{creator, assignee} {
			if id != nil && !slices.Contains(subscribers, *id) {
				subscribers = append(subscribers, *id)
			}
		}
		err := q.ImportIssue(ctx, dbgen.ImportIssueParams{
			ID: issueID, WorkspaceID: l.workspaceID, TeamID: team.ID, Number: number,
			Title: title, Description: description, StateID: state.ID, Priority: priority,
			AssigneeID: assignee, CreatorID: creator,
			// Newest first, below nothing that was already there.
			SortOrder: plan.minSort - 1 - created.Sub(time.Date(2000, 1, 1, 0, 0, 0, 0, time.UTC)).Hours()/24,
			LabelIds:  labelIDs, DueDate: parseDay(src.DueDate), SubscriberIds: subscribers,
			ProjectID: projectID, MilestoneID: milestoneID, CycleID: cycleID,
			CreatedAt: created, UpdatedAt: updated, StartedAt: started, CompletedAt: completed, ArchivedAt: archived,
		})
		if err != nil {
			return err
		}
		l.queued.activities = append(l.queued.activities, dbgen.ImportIssueActivitiesParams{
			ID: newID(), WorkspaceID: l.workspaceID, IssueID: issueID, ActorID: creator, Type: "created", CreatedAt: created,
		})
		if err := l.writeHistory(ctx, q, team, issueID, history); err != nil {
			return err
		}
		l.remember(kindIssue, src.ID, issueID, true, &updated)
		l.inc("issues")
	case before.SourceUpdatedAt != nil && !updated.After(*before.SourceUpdatedAt):
		// Unchanged at the source since last time: left as it is here.
		issueID = before.RecordID
		l.mapped[kindIssue][src.ID] = issueID
		unchanged = true
		l.inc("unchanged")
	default:
		issueID = before.RecordID
		// It stays in the team it went to; only a new import can move it.
		if before.TeamID != team.ID {
			if other, ok := l.teams[before.TeamID]; ok {
				team = other
				state = l.stateIn(team.ID, state)
			}
		}
		err := q.UpdateImportedIssue(ctx, dbgen.UpdateImportedIssueParams{
			ID: issueID, Title: title, Description: description, StateID: state.ID, Priority: priority,
			AssigneeID: assignee, LabelIds: labelIDs, DueDate: parseDay(src.DueDate),
			ProjectID: projectID, MilestoneID: milestoneID, CycleID: cycleID,
			UpdatedAt: updated, StartedAt: started, CompletedAt: completed, ArchivedAt: archived,
		})
		if err != nil {
			return err
		}
		if err := l.writeHistory(ctx, q, team, issueID, history); err != nil {
			return err
		}
		l.remember(kindIssue, src.ID, issueID, false, &updated)
		l.inc("updated")
	}
	l.touched[team.ID] = true

	for _, c := range src.Comments {
		if err := l.loadComment(ctx, q, issueID, c, updated, comments); err != nil {
			return err
		}
	}

	// Unchanged issues had their files copied the first time.
	if len(src.Attachments) > 0 && !unchanged {
		l.files = append(l.files, pendingFiles{issue: issueID, attachments: src.Attachments})
	}

	// Other issues may not be here yet; these wait for Finish.
	if src.Parent != "" {
		l.parents = append(l.parents, pendingLink{issue: issueID, parent: src.Parent})
	}
	for _, rel := range src.Relations {
		l.relations = append(l.relations, pendingRelation{issue: issueID, rel: rel})
	}
	return nil
}

// loadComment writes one comment on an issue, or brings it up to date if it
// was imported before. known is what earlier imports made of the comments
// in this bundle.
func (l *Loader) loadComment(ctx context.Context, q *dbgen.Queries, issueID uuid.UUID, c Comment, fallback time.Time, known map[string]uuid.UUID) error {
	if c.ID == "" || strings.TrimSpace(c.Body) == "" {
		return nil
	}
	author := l.user(c.Author)
	body := c.Body
	if author == nil && c.Author != "" {
		body = "**" + l.credit(c.Author) + "** wrote:\n\n" + body
	}
	body = clip(body, maxCommentLen)
	if id, ok := known[c.ID]; ok {
		l.queued.edits = append(l.queued.edits, dbgen.UpdateImportedCommentsParams{ID: id, Body: body})
		return nil
	}
	at := c.CreatedAt
	if at.IsZero() {
		at = fallback
	}
	commentID := newID()
	l.queued.comments = append(l.queued.comments, dbgen.ImportCommentsParams{
		ID: commentID, WorkspaceID: l.workspaceID, IssueID: issueID, UserID: author, Body: body, CreatedAt: at,
	})
	known[c.ID] = commentID
	l.inc("comments")
	l.remember(kindComment, c.ID, commentID, true, nil)
	return nil
}

// loadComments writes comments that arrived apart from their issues.
func (l *Loader) loadComments(ctx context.Context, q *dbgen.Queries, comments []IssueComment) error {
	if len(comments) == 0 {
		return nil
	}
	var commentIDs, missing []string
	for _, c := range comments {
		commentIDs = append(commentIDs, c.ID)
		if _, ok := l.mapped[kindIssue][c.Issue]; !ok && !slices.Contains(missing, c.Issue) {
			missing = append(missing, c.Issue)
		}
	}
	// Issues from an earlier import, which this run hasn't touched.
	if len(missing) > 0 {
		rows, err := q.ListImportedIssues(ctx, dbgen.ListImportedIssuesParams{WorkspaceID: l.workspaceID, Origin: l.origin, ExternalIds: missing, HiddenTeamIds: keys(l.hidden)})
		if err != nil {
			return err
		}
		for _, row := range rows {
			l.mapped[kindIssue][row.ExternalID] = row.RecordID
		}
	}
	rows, err := q.ListImportedComments(ctx, dbgen.ListImportedCommentsParams{WorkspaceID: l.workspaceID, Origin: l.origin, ExternalIds: commentIDs, HiddenTeamIds: keys(l.hidden)})
	if err != nil {
		return err
	}
	known := make(map[string]uuid.UUID, len(rows))
	for _, row := range rows {
		known[row.ExternalID] = row.RecordID
	}
	for _, c := range comments {
		issueID, ok := l.mapped[kindIssue][c.Issue]
		if !ok {
			continue // on something that wasn't imported, such as a pull request
		}
		if err := l.loadComment(ctx, q, issueID, c.Comment, l.now, known); err != nil {
			return err
		}
	}
	return nil
}

// description is the issue's text with a line saying where it came from,
// and who wrote it if they aren't anyone here.
func (l *Loader) description(src Issue, creditCreator bool) string {
	var notes []string
	if src.URL != "" {
		notes = append(notes, fmt.Sprintf("Imported from [%s](%s).", sourceName(src.URL), src.URL))
	}
	if creditCreator && src.Creator != "" {
		notes = append(notes, "Opened by "+l.credit(src.Creator)+".")
	}
	if len(notes) == 0 {
		return clip(src.Description, maxDescriptionLen)
	}
	footer := "\n\n---\n_" + strings.Join(notes, " ") + "_"
	body := strings.TrimRight(clip(src.Description, maxDescriptionLen-len(footer)), "\n ")
	return strings.TrimLeft(body+footer, "\n")
}

// sourceName is the host of a URL, for "Imported from github.com".
func sourceName(url string) string {
	rest := url
	if i := strings.Index(rest, "://"); i >= 0 {
		rest = rest[i+3:]
	}
	if i := strings.IndexAny(rest, "/?#"); i >= 0 {
		rest = rest[:i]
	}
	return rest
}

// issueState picks the status for an issue: the one the source names, or
// failing that one that fits whether the issue is done.
func (l *Loader) issueState(plan *teamPlan, src Issue) dbgen.WorkflowState {
	if state, ok := plan.states[src.State]; ok && src.State != "" {
		return state
	}
	teamID := plan.team.ID
	order := []string{KindBacklog, KindUnstarted, KindStarted}
	switch {
	case src.CompletedAt != nil:
		order = []string{KindCompleted}
	case src.StartedAt != nil:
		order = []string{KindStarted, KindUnstarted, KindBacklog}
	}
	for _, kind := range order {
		if state := l.firstState(teamID, kind); state != nil {
			return *state
		}
	}
	return l.states[teamID][0]
}

// stateIn finds the equivalent of a status in another team: same name, else
// the first of its kind.
func (l *Loader) stateIn(teamID uuid.UUID, like dbgen.WorkflowState) dbgen.WorkflowState {
	for _, s := range l.states[teamID] {
		if strings.EqualFold(s.Name, like.Name) {
			return s
		}
	}
	if s := l.firstState(teamID, like.Type); s != nil {
		return *s
	}
	return l.states[teamID][0]
}

// step is one status an issue was in.
type step struct {
	stateID uuid.UUID
	kind    string
	at      time.Time
}

// history is every status the issue has been in, oldest first, ending with
// the one it is in. It comes from the source where the source has it, and
// is otherwise pieced together from the issue's dates.
func (l *Loader) history(plan *teamPlan, src Issue, current dbgen.WorkflowState, created, updated time.Time) []step {
	teamID := plan.team.ID
	of := func(s dbgen.WorkflowState, at time.Time) step { return step{stateID: s.ID, kind: s.Type, at: at} }
	initial := func() dbgen.WorkflowState {
		for _, kind := range []string{KindUnstarted, KindBacklog} {
			if s := l.firstState(teamID, kind); s != nil {
				return *s
			}
		}
		return current
	}

	var steps []step
	changes := slices.Clone(src.History)
	slices.SortStableFunc(changes, func(a, b StateChange) int { return a.At.Compare(b.At) })
	for _, change := range changes {
		state, ok := plan.states[change.State]
		if !ok || change.At.IsZero() {
			continue
		}
		at := change.At
		if at.Before(created) {
			at = created
		}
		if n := len(steps); n > 0 && steps[n-1].stateID == state.ID {
			continue
		}
		steps = append(steps, of(state, at))
	}
	if len(steps) == 0 {
		// No history: created, perhaps started, perhaps closed.
		closed := current.Type == KindCompleted || current.Type == KindCanceled
		end := updated
		if src.CompletedAt != nil {
			end = *src.CompletedAt
		}
		switch {
		case src.StartedAt != nil && src.StartedAt.After(created):
			steps = append(steps, of(initial(), created))
			if started := l.firstState(teamID, KindStarted); started != nil && current.Type != KindStarted {
				steps = append(steps, of(*started, *src.StartedAt))
			}
			if closed {
				steps = append(steps, of(current, end))
			} else {
				steps = append(steps, of(current, *src.StartedAt))
			}
		case closed && end.After(created):
			steps = append(steps, of(initial(), created), of(current, end))
		default:
			steps = append(steps, of(current, created))
		}
	}
	// It begins at creation and ends in the current status.
	if steps[0].at.After(created.Add(time.Second)) {
		steps = append([]step{of(initial(), created)}, steps...)
	}
	if last := steps[len(steps)-1]; last.stateID != current.ID {
		at := updated
		if src.CompletedAt != nil {
			at = *src.CompletedAt
		}
		if at.Before(last.at) {
			at = last.at
		}
		steps = append(steps, of(current, at))
	}
	// Drop a step that repeats the one before it.
	out := steps[:1]
	for _, s := range steps[1:] {
		if s.stateID != out[len(out)-1].stateID {
			out = append(out, s)
		}
	}
	return out
}

// writeHistory replaces an issue's status history with the given one. (The
// database logged one entry, dated now, when the issue was written.)
func (l *Loader) writeHistory(ctx context.Context, q *dbgen.Queries, team dbgen.Team, issueID uuid.UUID, steps []step) error {
	if err := q.DeleteIssueStateHistory(ctx, issueID); err != nil {
		return err
	}
	for i, s := range steps {
		var left *time.Time
		if i+1 < len(steps) {
			left = &steps[i+1].at
		}
		stateID := s.stateID
		l.queued.history = append(l.queued.history, dbgen.InsertIssueStateHistoriesParams{
			WorkspaceID: l.workspaceID, TeamID: team.ID, IssueID: issueID,
			StateID: &stateID, StateType: s.kind, EnteredAt: s.at, LeftAt: left,
		})
	}
	return nil
}

// --- Finishing -------------------------------------------------------------------------

// Finish ties issues to their parents and relations (which may have arrived
// in any order), moves each team's numbering past what was imported, and
// tells every client to load the workspace again.
func (l *Loader) Finish(ctx context.Context) error {
	// What stopped the files doesn't stop the rest being tied together.
	filesErr := l.WaitFiles()
	if err := l.finish(ctx); err != nil {
		return err
	}
	return filesErr
}

func (l *Loader) finish(ctx context.Context) error {
	return db.InTx(ctx, l.pool, func(q *dbgen.Queries) error {
		// Issues from earlier imports can be referred to as well.
		items, err := q.ListImportItems(ctx, dbgen.ListImportItemsParams{WorkspaceID: l.workspaceID, Origin: l.origin, Kind: kindIssue})
		if err != nil {
			return err
		}
		issues := make(map[string]uuid.UUID, len(items))
		for _, item := range items {
			issues[item.ExternalID] = item.RecordID
		}
		for _, link := range l.parents {
			parent, ok := issues[link.parent]
			if !ok || parent == link.issue {
				continue
			}
			if err := q.SetIssueParentQuietly(ctx, dbgen.SetIssueParentQuietlyParams{ID: link.issue, ParentID: &parent}); err != nil {
				return err
			}
		}
		for _, pending := range l.relations {
			other, ok := issues[pending.rel.Issue]
			if !ok || other == pending.issue {
				continue
			}
			from, to, kind := pending.issue, other, pending.rel.Type
			switch kind {
			case RelBlockedBy:
				from, to, kind = other, pending.issue, RelBlocks
			case RelRelated:
				// "Related" has no direction; sources that list it on both
				// issues must not make it twice.
				if strings.Compare(from.String(), to.String()) > 0 {
					from, to = to, from
				}
			case RelBlocks, RelDuplicate:
			default:
				continue
			}
			err := q.ImportIssueRelation(ctx, dbgen.ImportIssueRelationParams{
				ID: newID(), WorkspaceID: l.workspaceID, IssueID: from, RelatedIssueID: to, Type: kind,
			})
			if err != nil {
				return err
			}
		}
		for teamID := range l.touched {
			if err := q.RaiseNextIssueNumber(ctx, teamID); err != nil {
				return err
			}
		}
		return syncer.ResetAll(ctx, q, l.workspaceID)
	})
}

// newID returns a time-ordered ID, like every other record's.
func newID() uuid.UUID {
	id, err := uuid.NewV7()
	if err != nil {
		return uuid.New()
	}
	return id
}
