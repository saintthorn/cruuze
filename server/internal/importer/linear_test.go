package importer

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"
)

// Linear answers every query at one address, so the fake serves its answers
// in the order the importer asks.
func linearFake(t *testing.T, answers ...string) *fakeAPI {
	responses := make([]fakeResponse, 0, len(answers))
	for _, body := range answers {
		responses = append(responses, fakeResponse{status: 200, body: body})
	}
	return &fakeAPI{t: t, routes: map[string][]fakeResponse{"POST /graphql": responses}}
}

const (
	lnOrganization = `{"data":{"organization":{"name":"Acme","urlKey":"Acme-Co"}}}`
	lnTeams        = `{"data":{"teams":{"nodes":[
		{"id":"team-eng","key":"ENG","name":"Engineering","issueCount":2,"states":{"nodes":[
			{"id":"st-done","name":"Done","type":"completed","color":"#5e6ad2","position":3},
			{"id":"st-prog","name":"In Progress","type":"started","color":"#f2c94c","position":2},
			{"id":"st-todo","name":"Todo","type":"unstarted","color":"#e2e2e2","position":1},
			{"id":"st-review","name":"In Review","type":"started","color":"#4ea7fc","position":2.5},
			{"id":"st-dup","name":"Duplicate","type":"duplicate","color":"#95a2b3","position":5}]}},
		{"id":"team-des","key":"DES","name":"Design","issueCount":9,"states":{"nodes":[]}}],
		"pageInfo":{"hasNextPage":false,"endCursor":null}}}}`
	lnUsers = `{"data":{"users":{"nodes":[
		{"id":"u1","name":"Ada Lovelace","displayName":"ada","email":"ada@example.com"},
		{"id":"u2","name":"","displayName":"bob","email":"bob@example.com"}],
		"pageInfo":{"hasNextPage":false,"endCursor":null}}}}`
	lnLabels   = `{"data":{"issueLabels":{"nodes":[{"id":"lb1","name":"Bug","color":"#eb5757"}],"pageInfo":{"hasNextPage":false}}}}`
	lnProjects = `{"data":{"projects":{"nodes":[{"id":"pr1","name":"Launch","description":"Ship it","state":"started",
		"startDate":"2026-09-01","targetDate":"2026-12-01","lead":{"id":"u1"},
		"projectMilestones":{"nodes":[{"id":"ms1","name":"Beta","targetDate":"2026-10-15"}]}}],"pageInfo":{"hasNextPage":false}}}}`
	lnCycles = `{"data":{"cycles":{"nodes":[{"id":"cy1","number":12,"name":"","startsAt":"2026-09-28T00:00:00.000Z","endsAt":"2026-10-12T00:00:00.000Z","team":{"id":"team-eng"}}],
		"pageInfo":{"hasNextPage":false}}}}`
	lnIssues = `{"data":{"issues":{"nodes":[
		{"id":"is1","number":41,"title":"Fix login","description":"It fails.","priority":1,"estimate":3,"dueDate":"2026-10-20",
		 "createdAt":"2026-09-01T10:00:00.000Z","updatedAt":"2026-09-10T10:00:00.000Z","startedAt":"2026-09-02T10:00:00.000Z",
		 "completedAt":"2026-09-10T10:00:00.000Z","canceledAt":null,"archivedAt":null,"url":"https://linear.app/acme-co/issue/ENG-41",
		 "state":{"id":"st-done"},"team":{"id":"team-eng"},"assignee":{"id":"u1"},"creator":{"id":"u2"},
		 "project":{"id":"pr1"},"projectMilestone":{"id":"ms1"},"cycle":{"id":"cy1"},"parent":null,
		 "labels":{"nodes":[{"id":"lb1"}]},
		 "comments":{"nodes":[{"id":"cm1","body":"Looking.","createdAt":"2026-09-02T11:00:00.000Z","user":{"id":"u1"}}],"pageInfo":{"hasNextPage":true,"endCursor":"c-1"}},
		 "relations":{"nodes":[{"type":"blocks","relatedIssue":{"id":"is2"}},{"type":"similar","relatedIssue":{"id":"is9"}}]},
		 "history":{"nodes":[
			{"createdAt":"2026-09-10T10:00:00.000Z","fromState":{"id":"st-prog"},"toState":{"id":"st-done"}},
			{"createdAt":"2026-09-05T10:00:00.000Z","fromState":null,"toState":null},
			{"createdAt":"2026-09-02T10:00:00.000Z","fromState":{"id":"st-todo"},"toState":{"id":"st-prog"}}],"pageInfo":{"hasNextPage":false}}},
		{"id":"is2","number":42,"title":"Canceled idea","description":null,"priority":0,"estimate":null,"dueDate":null,
		 "createdAt":"2026-09-03T10:00:00.000Z","updatedAt":"2026-09-04T10:00:00.000Z","startedAt":null,"completedAt":null,
		 "canceledAt":"2026-09-04T10:00:00.000Z","archivedAt":"2026-09-20T10:00:00.000Z","url":"https://linear.app/acme-co/issue/ENG-42",
		 "state":{"id":"st-dup"},"team":{"id":"team-eng"},"assignee":null,"creator":null,"project":null,"projectMilestone":null,"cycle":null,
		 "parent":{"id":"is1"},"labels":{"nodes":[]},"comments":{"nodes":[],"pageInfo":{"hasNextPage":false}},
		 "relations":{"nodes":[]},"history":{"nodes":[],"pageInfo":{"hasNextPage":false}}}],
		"pageInfo":{"hasNextPage":false,"endCursor":"i-2"}}}}`
	lnMoreComments = `{"data":{"issue":{"comments":{"nodes":[{"id":"cm2","body":"Done.","createdAt":"2026-09-10T09:00:00.000Z","user":null}],"pageInfo":{"hasNextPage":false}}}}}`
)

func TestLinearPreview(t *testing.T) {
	fake := linearFake(t, lnOrganization, lnTeams, lnUsers)
	source, err := NewLinear(fake.client(), " lin_api_secret ", nil)
	if err != nil {
		t.Fatal(err)
	}
	preview, err := source.Preview(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if preview.Origin != "linear:acme-co" || preview.Label != "Acme (Linear)" || len(preview.Teams) != 2 || len(preview.Users) != 2 {
		t.Fatalf("preview = %+v", preview)
	}
	if team := preview.Teams[0]; team.ID != "team-eng" || team.Key != "ENG" || team.Issues != 2 {
		t.Errorf("team = %+v", team)
	}
	if u := preview.Users[1]; u.Email != "bob@example.com" || u.Name != "bob" {
		t.Errorf("user = %+v", u)
	}
	// A personal key is sent as it is, in the header only.
	for i, req := range fake.requests {
		if req.Header.Get("Authorization") != "lin_api_secret" || strings.Contains(fake.bodies[i], "lin_api_secret") {
			t.Errorf("request %d: authorization %q", i, req.Header.Get("Authorization"))
		}
	}
}

func TestLinearFetch(t *testing.T) {
	fake := linearFake(t, lnTeams, lnUsers, lnLabels, lnProjects, lnCycles, lnIssues, lnMoreComments)
	source, _ := NewLinear(fake.client(), "key", []string{"team-eng"})
	all, _ := collect(t, source)

	// Only the chosen team, with its statuses in workflow order and kinds kept.
	if len(all.Teams) != 1 || all.Teams[0].Key != "ENG" {
		t.Fatalf("teams = %+v", all.Teams)
	}
	var states []string
	for _, s := range all.Teams[0].States {
		states = append(states, s.Name+":"+s.Kind)
	}
	if got := strings.Join(states, ", "); got != "Todo:unstarted, In Progress:started, In Review:started, Done:completed, Duplicate:canceled" {
		t.Errorf("states = %s", got)
	}
	if len(all.Projects) != 1 || all.Projects[0].Lead != "u1" || len(all.Projects[0].Milestones) != 1 || all.Projects[0].TargetDate != "2026-12-01" {
		t.Errorf("projects = %+v", all.Projects)
	}
	// A cycle that ends at midnight on the 12th has the 11th as its last day.
	if len(all.Cycles) != 1 || all.Cycles[0].Number != 12 || all.Cycles[0].StartsAt != "2026-09-28" || all.Cycles[0].EndsAt != "2026-10-11" {
		t.Errorf("cycles = %+v", all.Cycles)
	}
	if len(all.Issues) != 2 {
		t.Fatalf("%d issues", len(all.Issues))
	}
	login, idea := all.Issues[0], all.Issues[1]
	if login.ID != "is1" || login.Number != 41 || login.State != "st-done" || login.Priority != 1 || login.DueDate != "2026-10-20" || login.Assignee != "u1" || login.Creator != "u2" || login.Project != "pr1" || login.Milestone != "ms1" ||
		login.Cycle != "cy1" || len(login.Labels) != 1 || login.Archived || login.URL == "" {
		t.Errorf("ENG-41 = %+v", login)
	}
	// Both pages of comments; "similar" is a kind of related.
	if len(login.Comments) != 2 || login.Comments[1].ID != "cm2" || login.Comments[1].Author != "" {
		t.Errorf("comments = %+v", login.Comments)
	}
	if len(login.Relations) != 2 || login.Relations[0] != (Relation{Type: RelBlocks, Issue: "is2"}) || login.Relations[1].Type != RelRelated {
		t.Errorf("relations = %+v", login.Relations)
	}
	// The status changes, plus what it was created as.
	var history []string
	for _, h := range login.History {
		history = append(history, h.State+"@"+h.At.Format("01-02"))
	}
	if got := strings.Join(history, " "); got != "st-done@09-10 st-prog@09-02 st-todo@09-01" {
		t.Errorf("history = %s", got)
	}
	// Canceled counts as closed, at the time it was canceled.
	if idea.State != "st-dup" || idea.CompletedAt == nil || idea.CompletedAt.Format(time.DateOnly) != "2026-09-04" || !idea.Archived || idea.Parent != "is1" || idea.Description != "" {
		t.Errorf("ENG-42 = %+v", idea)
	}
	// The issues query asks only for the chosen team.
	if body := fake.bodies[5]; !strings.Contains(body, `"teams":["team-eng"]`) || !strings.Contains(body, "includeArchived: true") {
		t.Errorf("issues query = %s", body)
	}
}

func TestLinearErrors(t *testing.T) {
	if _, err := NewLinear(nil, "  ", nil); err == nil {
		t.Error("an empty key was accepted")
	}
	noSleep(t)
	message := func(fake *fakeAPI) string {
		t.Helper()
		source, _ := NewLinear(fake.client(), "key", nil)
		_, err := source.Preview(context.Background())
		var userErr *UserError
		if !errors.As(err, &userErr) {
			t.Fatalf("error = %v, want one for the user", err)
		}
		return userErr.Message
	}
	refused := &fakeAPI{t: t, routes: map[string][]fakeResponse{"POST /graphql": {{status: 401, body: `{"errors":[{"message":"Authentication required"}]}`}}}}
	if got := message(refused); !strings.Contains(got, "refused the API key") {
		t.Errorf("401: %q", got)
	}
	// A schema error is reported with Linear's own words.
	fake := linearFake(t, `{"errors":[{"message":"Cannot query field \"nope\" on type \"Organization\".","extensions":{"code":"GRAPHQL_VALIDATION_FAILED"}}]}`)
	source, _ := NewLinear(fake.client(), "key", nil)
	if _, err := source.Preview(context.Background()); err == nil || !strings.Contains(err.Error(), "Cannot query field") {
		t.Errorf("schema error: %v", err)
	}
	// A rate limit is waited out, then the query is asked again.
	limited := &fakeAPI{t: t, routes: map[string][]fakeResponse{"POST /graphql": {
		{status: 400, body: `{"errors":[{"message":"Rate limit exceeded","extensions":{"code":"RATELIMITED"}}]}`},
		{status: 200, body: lnOrganization}, {status: 200, body: lnTeams}, {status: 200, body: lnUsers},
	}}}
	source, _ = NewLinear(limited.client(), "key", nil)
	if preview, err := source.Preview(context.Background()); err != nil || len(preview.Teams) != 2 {
		t.Errorf("after a rate limit: %+v, %v", preview, err)
	}
}
