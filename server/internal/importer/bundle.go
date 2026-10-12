// Package importer brings work in from other trackers. Every source (GitHub,
// Linear, Jira, a file) is translated into one neutral shape, the Bundle,
// and one Loader writes bundles into a workspace. A source knows nothing
// about Cruuze's tables; the loader knows nothing about any source.
package importer

import (
	"context"
	"time"
)

// Bundle is a piece of another tracker's data. A source may deliver one
// bundle or many: things refer to each other by the source's own IDs, and a
// reference may point at something in an earlier bundle, or (for an issue's
// parent and relations) a later one.
//
// It is also the documented file format for scripted imports: see
// docs/import-format.md. Dates are "2006-01-02"; times are RFC 3339.
type Bundle struct {
	Users    []User    `json:"users,omitempty"`
	Teams    []Team    `json:"teams,omitempty"`
	Labels   []Label   `json:"labels,omitempty"`
	Projects []Project `json:"projects,omitempty"`
	Cycles   []Cycle   `json:"cycles,omitempty"`
	Issues   []Issue   `json:"issues,omitempty"`
	// Comments are comments on issues that arrived earlier: for sources
	// that list comments apart from issues.
	Comments []IssueComment `json:"comments,omitempty"`
}

// IssueComment is a comment and the issue it is on.
type IssueComment struct {
	Issue string `json:"issue"`
	Comment
}

// File is a bundle as uploaded: the bundle plus where it came from.
type File struct {
	// Origin names the place the data came from, e.g. "trello:board-x".
	// IDs only need to be unique within it, and importing the same origin
	// again updates what the first import made.
	Origin string `json:"origin"`
	// Label says what it is, for the import history.
	Label string `json:"label,omitempty"`
	Bundle
}

// User is a person at the source. They are matched to workspace members by
// email; anyone unmatched is credited by name in the text instead.
type User struct {
	ID    string `json:"id"`
	Email string `json:"email,omitempty"`
	Name  string `json:"name,omitempty"`
	// GitHubLogin matches people who have given Cruuze their GitHub
	// username, since GitHub doesn't give out email addresses.
	GitHubLogin string `json:"githubLogin,omitempty"`
	// Self marks the account whose credentials the import is running with:
	// that is whoever started it, however little else the source says.
	Self bool `json:"self,omitempty"`
}

// Team is whatever the source groups issues by: a Linear team, a Jira
// project, a GitHub repository.
type Team struct {
	ID string `json:"id"`
	// Key prefixes issue numbers (ENG-12). Up to 7 letters and digits.
	Key  string `json:"key"`
	Name string `json:"name"`
	// States are the team's statuses, in workflow order. Leave empty to use
	// the team's existing (or the default) statuses.
	States []State `json:"states,omitempty"`
}

// The kinds of status. Every status is one of these, whatever it is called.
const (
	KindTriage    = "triage"
	KindBacklog   = "backlog"
	KindUnstarted = "unstarted"
	KindStarted   = "started"
	KindCompleted = "completed"
	KindCanceled  = "canceled"
)

type State struct {
	ID   string `json:"id"`
	Name string `json:"name"`
	// Kind is one of the Kind constants.
	Kind  string `json:"kind"`
	Color string `json:"color,omitempty"`
}

type Label struct {
	ID    string `json:"id"`
	Name  string `json:"name"`
	Color string `json:"color,omitempty"`
}

type Project struct {
	ID          string `json:"id"`
	Name        string `json:"name"`
	Description string `json:"description,omitempty"`
	// Status is backlog, planned, started, paused, completed or canceled.
	Status     string      `json:"status,omitempty"`
	Lead       string      `json:"lead,omitempty"`
	StartDate  string      `json:"startDate,omitempty"`
	TargetDate string      `json:"targetDate,omitempty"`
	Milestones []Milestone `json:"milestones,omitempty"`
}

type Milestone struct {
	ID         string `json:"id"`
	Name       string `json:"name"`
	TargetDate string `json:"targetDate,omitempty"`
}

// Cycle is a sprint or iteration of one team.
type Cycle struct {
	ID       string `json:"id"`
	Team     string `json:"team"`
	Number   int32  `json:"number"`
	Name     string `json:"name,omitempty"`
	StartsAt string `json:"startsAt"`
	EndsAt   string `json:"endsAt"`
}

type Issue struct {
	ID   string `json:"id"`
	Team string `json:"team"`
	// Number is kept when the team has no other issues, so ENG-12 stays ENG-12.
	Number      int32  `json:"number,omitempty"`
	Title       string `json:"title"`
	Description string `json:"description,omitempty"` // Markdown
	// State is the ID of one of the team's states. Empty picks one from
	// whether the issue is completed.
	State string `json:"state,omitempty"`
	// Priority is 0 (none), 1 (urgent), 2 (high), 3 (medium) or 4 (low).
	Priority  int      `json:"priority,omitempty"`
	Assignee  string   `json:"assignee,omitempty"`
	Creator   string   `json:"creator,omitempty"`
	Labels    []string `json:"labels,omitempty"`
	DueDate   string   `json:"dueDate,omitempty"`
	Project   string   `json:"project,omitempty"`
	Milestone string   `json:"milestone,omitempty"`
	Cycle     string   `json:"cycle,omitempty"`
	// Parent is the issue this is a sub-issue of.
	Parent string `json:"parent,omitempty"`

	CreatedAt   time.Time  `json:"createdAt"`
	UpdatedAt   time.Time  `json:"updatedAt,omitempty"`
	StartedAt   *time.Time `json:"startedAt,omitempty"`
	CompletedAt *time.Time `json:"completedAt,omitempty"`
	Archived    bool       `json:"archived,omitempty"`

	// History is every status the issue has been in, oldest first, starting
	// with the one it was created in. It is what cycle time and forecasts
	// are computed from; without it, it is pieced together from the dates.
	History     []StateChange `json:"history,omitempty"`
	Comments    []Comment     `json:"comments,omitempty"`
	Relations   []Relation    `json:"relations,omitempty"`
	Attachments []Attachment  `json:"attachments,omitempty"`
	// URL is the issue at the source; it is linked from the description.
	URL string `json:"url,omitempty"`
}

// StateChange says the issue moved into State at At.
type StateChange struct {
	State string    `json:"state"`
	At    time.Time `json:"at"`
}

type Comment struct {
	ID        string    `json:"id"`
	Author    string    `json:"author,omitempty"`
	Body      string    `json:"body"` // Markdown
	CreatedAt time.Time `json:"createdAt"`
}

// Relation kinds, read from the issue that carries them.
const (
	RelBlocks    = "blocks"     // this issue blocks the other
	RelBlockedBy = "blocked_by" // the other blocks this one
	RelDuplicate = "duplicate"  // this issue is a duplicate of the other
	RelRelated   = "related"
)

type Relation struct {
	Type  string `json:"type"`
	Issue string `json:"issue"`
}

// Attachment is a file on an issue. The loader asks the source for its bytes.
type Attachment struct {
	ID       string `json:"id"`
	Filename string `json:"filename"`
	URL      string `json:"url"`
}

// Preview is what a source has to offer, before anything is imported: enough
// to decide where each team goes and who each person is.
type Preview struct {
	// Origin and Label are as for File.
	Origin string        `json:"origin"`
	Label  string        `json:"label"`
	Teams  []PreviewTeam `json:"teams"`
	Users  []User        `json:"users"`
}

type PreviewTeam struct {
	ID   string `json:"id"`
	Key  string `json:"key"`
	Name string `json:"name"`
	// Issues is how many issues it has, or -1 if the source doesn't say cheaply.
	Issues int `json:"issues"`
}

// Source is another tracker, or a file from one.
type Source interface {
	// Preview connects and says what is there. It should be quick.
	Preview(ctx context.Context) (Preview, error)
	// Fetch reads everything, handing it over in bundles as it goes. It
	// reports what it is doing through progress.
	Fetch(ctx context.Context, emit func(Bundle) error, progress func(stage string)) error
	// Open returns an attachment's bytes, for sources whose files need the
	// source's credentials to read.
	Open(ctx context.Context, a Attachment) (body ReadCloser, contentType string, err error)
}

// Incremental is a source that can read only what changed since a time,
// for an import that has run to the end before.
type Incremental interface {
	ReadSince(t time.Time)
}

// readAhead is how many bundles a source may be ahead of the loader.
const readAhead = 4

// Read fetches everything from a source, loading each bundle as it arrives.
// The source reads on while earlier bundles are written; they are loaded
// one at a time, in the order the source handed them over. What arrived
// before the source failed is still loaded.
func Read(ctx context.Context, source Source, load func(Bundle) error, progress func(stage string)) error {
	ctx, cancel := context.WithCancel(ctx)
	defer cancel()
	bundles := make(chan Bundle, readAhead)
	loaded := make(chan error, 1)
	go func() {
		var err error
		for b := range bundles {
			if err != nil {
				continue
			}
			if err = load(b); err != nil {
				cancel() // the source has nothing to read for
			}
		}
		loaded <- err
	}()
	fetchErr := source.Fetch(ctx, func(b Bundle) error {
		if err := ctx.Err(); err != nil {
			return err
		}
		select {
		case bundles <- b:
			return nil
		case <-ctx.Done():
			return ctx.Err()
		}
	}, progress)
	close(bundles)
	if err := <-loaded; err != nil {
		return err
	}
	return fetchErr
}

// ReadCloser is io.ReadCloser, named here so sources needn't import io for it.
type ReadCloser interface {
	Read(p []byte) (int, error)
	Close() error
}

// UserError is a failure the person running the import should read as it
// is: a wrong token, a repository that doesn't exist.
type UserError struct{ Message string }

func (e *UserError) Error() string { return e.Message }

func userError(message string) error { return &UserError{Message: message} }
