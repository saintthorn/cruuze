package importer

import (
	"context"
	"encoding/base64"
	"net/url"
	"strings"
	"testing"
	"time"
)

const jiraFieldList = "summary,description,status,priority,assignee,reporter,labels,components,duedate,created,updated,resolutiondate,resolution,issuetype,parent,issuelinks,comment,attachment"

func jiraSearchKey(prefix, project, fields string, extra url.Values) string {
	query := url.Values{"jql": {jiraJQL(project)}, "maxResults": {"100"}, "fields": {fields}, "expand": {"changelog"}}
	for name, values := range extra {
		query[name] = values
	}
	return "GET " + prefix + "?" + query.Encode()
}

func jiraCloudFake(t *testing.T) *fakeAPI {
	fields := jiraFieldList + ",customfield_10020"
	return &fakeAPI{t: t, routes: map[string][]fakeResponse{
		"GET /rest/api/2/serverInfo":                             ok(`{"deploymentType":"Cloud","serverTitle":"Jira"}`),
		"GET /rest/api/2/myself":                                 ok(`{"accountId":"me"}`),
		"GET /rest/api/3/project/search?startAt=0&maxResults=50": ok(`{"values":[{"id":"10000","key":"WEB","name":"Website"},{"id":"10001","key":"OPS","name":"Operations"}],"isLast":true}`),
		"POST /rest/api/3/search/approximate-count":              ok(`{"count":2}`),
		"GET /rest/api/3/users/search?startAt=0&maxResults=200": ok(`[
			{"accountId":"acc-ada","displayName":"Ada Lovelace","emailAddress":"ada@example.com","accountType":"atlassian","active":true},
			{"accountId":"acc-bot","displayName":"Automation","accountType":"app","active":true},
			{"accountId":"acc-gone","displayName":"Left","accountType":"atlassian","active":false}]`),
		"GET /rest/api/3/field": ok(`[
			{"id":"summary","name":"Summary","schema":{}},
			{"id":"customfield_10016","name":"Story point estimate","schema":{"custom":"com.pyxis.greenhopper.jira:jsw-story-points"}},
			{"id":"customfield_10020","name":"Sprint","schema":{"custom":"com.pyxis.greenhopper.jira:gh-sprint"}}]`),
		"GET /rest/api/3/project/WEB/statuses": ok(`[
			{"name":"Task","statuses":[
				{"id":"3","name":"In Progress","statusCategory":{"key":"indeterminate"}},
				{"id":"1","name":"To Do","statusCategory":{"key":"new"}},
				{"id":"5","name":"Done","statusCategory":{"key":"done"}}]},
			{"name":"Bug","statuses":[
				{"id":"1","name":"To Do","statusCategory":{"key":"new"}},
				{"id":"7","name":"Won't Fix","statusCategory":{"key":"done"}},
				{"id":"8","name":"Backlog","statusCategory":{"key":"new"}}]}]`),
		"GET /rest/agile/1.0/board?maxResults=50&projectKeyOrId=WEB": ok(`{"values":[{"id":4,"type":"scrum"},{"id":5,"type":"kanban"}]}`),
		"GET /rest/agile/1.0/board/4/sprint?startAt=0&maxResults=50": ok(`{"isLast":true,"values":[
			{"id":31,"name":"Sprint B","state":"active","startDate":"2026-09-15T08:00:00.000Z","endDate":"2026-09-29T08:00:00.000Z"},
			{"id":30,"name":"Sprint A","state":"closed","startDate":"2026-09-01T08:00:00.000Z","endDate":"2026-09-15T08:00:00.000Z"},
			{"id":32,"name":"Future","state":"future"}]}`),
		jiraSearchKey("/rest/api/3/search/jql", "WEB", fields, nil): ok(`{"issues":[
			{"id":"1001","key":"WEB-1","fields":{
				"summary":"Build the homepage",
				"description":{"type":"doc","version":1,"content":[{"type":"paragraph","content":[{"type":"text","text":"Make it "},{"type":"text","text":"fast","marks":[{"type":"strong"}]}]}]},
				"status":{"id":"5","name":"Done","statusCategory":{"key":"done"}},
				"priority":{"name":"Highest"},"resolution":{"name":"Done"},"issuetype":{"name":"Epic"},
				"assignee":{"accountId":"acc-ada","displayName":"Ada Lovelace"},"reporter":{"accountId":"acc-bob","displayName":"Bob"},
				"labels":["frontend"],"components":[{"name":"Web"}],"duedate":"2026-09-30",
				"created":"2026-09-01T10:00:00.000+0200","updated":"2026-09-20T10:00:00.000+0000","resolutiondate":"2026-09-20T10:00:00.000+0000",
				"customfield_10016":5,"customfield_10020":[{"id":30,"name":"Sprint A"},{"id":31,"name":"Sprint B"}],
				"issuelinks":[
					{"type":{"name":"Blocks"},"outwardIssue":{"key":"WEB-2"}},
					{"type":{"name":"Relates"},"inwardIssue":{"key":"OPS-9"}}],
				"attachment":[{"id":"900","filename":"mock.png","content":"https://acme.atlassian.net/rest/api/3/attachment/content/900"}],
				"comment":{"total":2,"comments":[{"id":"c1","author":{"accountId":"acc-ada","displayName":"Ada Lovelace"},
					"body":{"type":"doc","content":[{"type":"paragraph","content":[{"type":"text","text":"Started."}]}]},"created":"2026-09-02T10:00:00.000+0000"}]}},
			 "changelog":{"total":2,"histories":[
				{"created":"2026-09-20T10:00:00.000+0000","items":[{"field":"status","from":"3","to":"5"},{"field":"resolution","from":null,"to":"10000"}]},
				{"created":"2026-09-02T09:00:00.000+0000","items":[{"field":"assignee","from":null,"to":"acc-ada"},{"field":"status","from":"1","to":"3"}]}]}},
			{"id":"1002","key":"WEB-2","fields":{
				"summary":"Old idea","description":null,
				"status":{"id":"5","name":"Done","statusCategory":{"key":"done"}},
				"priority":null,"resolution":{"name":"Won't Do"},"issuetype":{"name":"Task"},
				"assignee":null,"reporter":null,"labels":[],"components":[],"duedate":null,
				"created":"2026-09-03T10:00:00.000+0000","updated":"2026-09-04T10:00:00.000+0000","resolutiondate":"2026-09-04T10:00:00.000+0000",
				"parent":{"id":"1001","key":"WEB-1"},"customfield_10016":null,"customfield_10020":null,
				"issuelinks":[],"attachment":[],"comment":{"total":0,"comments":[]}},
			 "changelog":{"total":0,"histories":[]}}],
			"isLast":true}`),
		"GET /rest/api/3/issue/WEB-1/comment?startAt=1&maxResults=100": ok(`{"comments":[{"id":"c2","author":{"accountId":"acc-bob","displayName":"Bob"},
			"body":{"type":"doc","content":[{"type":"paragraph","content":[{"type":"text","text":"Nice."}]}]},"created":"2026-09-21T10:00:00.000+0000"}]}`),
	}}
}

func TestJiraCloudPreview(t *testing.T) {
	fake := jiraCloudFake(t)
	source, err := NewJira(fake.client(), "acme.atlassian.net/", "ada@example.com", "tok", nil)
	if err != nil {
		t.Fatal(err)
	}
	preview, err := source.Preview(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if preview.Origin != "jira:acme.atlassian.net" || preview.Label != "acme.atlassian.net" || len(preview.Teams) != 2 || preview.Teams[0].ID != "WEB" || preview.Teams[0].Issues != 2 {
		t.Fatalf("preview = %+v", preview)
	}
	// People only: no apps, nobody who has left.
	if len(preview.Users) != 1 || preview.Users[0].ID != "acc-ada" || preview.Users[0].Email != "ada@example.com" {
		t.Errorf("users = %+v", preview.Users)
	}
	want := "Basic " + base64.StdEncoding.EncodeToString([]byte("ada@example.com:tok"))
	for _, req := range fake.requests {
		if req.Header.Get("Authorization") != want {
			t.Errorf("%s: authorization %q", req.URL.Path, req.Header.Get("Authorization"))
		}
	}
}

func TestJiraCloudFetch(t *testing.T) {
	source, _ := NewJira(jiraCloudFake(t).client(), "https://acme.atlassian.net", "ada@example.com", "tok", []string{"web"})
	all, _ := collect(t, source)

	if len(all.Teams) != 1 || all.Teams[0].Key != "WEB" {
		t.Fatalf("teams = %+v", all.Teams)
	}
	var states []string
	for _, s := range all.Teams[0].States {
		states = append(states, s.ID+"="+s.Name+":"+s.Kind)
	}
	want := "WEB:8=Backlog:backlog, WEB:1=To Do:unstarted, WEB:3=In Progress:started, WEB:5=Done:completed, WEB:7=Won't Fix:canceled, WEB:canceled=Canceled:canceled"
	if got := strings.Join(states, ", "); got != want {
		t.Errorf("states = %s", got)
	}
	// Dated sprints, numbered in the order they ran.
	if len(all.Cycles) != 2 || all.Cycles[0].Name != "Sprint A" || all.Cycles[0].Number != 1 || all.Cycles[0].StartsAt != "2026-09-01" || all.Cycles[1].ID != "WEB:sprint:31" {
		t.Errorf("cycles = %+v", all.Cycles)
	}
	if len(all.Issues) != 2 {
		t.Fatalf("%d issues", len(all.Issues))
	}
	epic, idea := all.Issues[0], all.Issues[1]
	if epic.ID != "WEB-1" || epic.Number != 1 || epic.Title != "Build the homepage" || epic.Description != "Make it **fast**" ||
		epic.State != "WEB:5" || epic.Priority != 1 || epic.Assignee != "acc-ada" || epic.Creator != "acc-bob" ||
		epic.DueDate != "2026-09-30" || epic.Cycle != "WEB:sprint:31" ||
		epic.URL != "https://acme.atlassian.net/browse/WEB-1" || epic.CompletedAt == nil {
		t.Errorf("WEB-1 = %+v", epic)
	}
	if !epic.CreatedAt.Equal(time.Date(2026, 9, 1, 8, 0, 0, 0, time.UTC)) {
		t.Errorf("created = %v, want the offset applied", epic.CreatedAt)
	}
	if got := strings.Join(epic.Labels, ","); got != "frontend,Web,Epic" {
		t.Errorf("labels = %s", got)
	}
	if len(epic.Relations) != 2 || epic.Relations[0] != (Relation{RelBlocks, "WEB-2"}) || epic.Relations[1] != (Relation{RelRelated, "OPS-9"}) {
		t.Errorf("relations = %+v", epic.Relations)
	}
	if len(epic.Attachments) != 1 || epic.Attachments[0].Filename != "mock.png" {
		t.Errorf("attachments = %+v", epic.Attachments)
	}
	// Both comments: the one that came with the issue and the one fetched after.
	if len(epic.Comments) != 2 || epic.Comments[0].Body != "Started." || epic.Comments[1].Author != "acc-bob" {
		t.Errorf("comments = %+v", epic.Comments)
	}
	var history []string
	for _, h := range epic.History {
		history = append(history, h.State+"@"+h.At.Format("01-02"))
	}
	if got := strings.Join(history, " "); got != "WEB:5@09-20 WEB:3@09-02 WEB:1@09-01" {
		t.Errorf("history = %s", got)
	}
	// Done, but resolved "Won't Do": canceled here. And a child of the epic.
	if idea.State != "WEB:canceled" || idea.Parent != "WEB-1" || idea.Description != "" || idea.Priority != 0 || idea.Assignee != "" || strings.Join(idea.Labels, ",") != "Task" {
		t.Errorf("WEB-2 = %+v", idea)
	}
	var people []string
	for _, u := range all.Users {
		people = append(people, u.ID)
	}
	if got := strings.Join(people, ","); got != "acc-ada,acc-bob" {
		t.Errorf("users = %s", got)
	}
}

func TestJiraServerFetch(t *testing.T) {
	fields := jiraFieldList + ",customfield_10005,customfield_10006"
	fake := &fakeAPI{t: t, routes: map[string][]fakeResponse{
		"GET /jira/rest/api/2/serverInfo": ok(`{"deploymentType":"Server","serverTitle":"Acme Jira","version":"9.4.0"}`),
		"GET /jira/rest/api/2/myself":     ok(`{"key":"ada"}`),
		"GET /jira/rest/api/2/field": ok(`[
			{"id":"customfield_10002","name":"Story Points","schema":{"custom":"com.atlassian.jira.plugin.system.customfieldtypes:float"}},
			{"id":"customfield_10005","name":"Sprint","schema":{"custom":"com.pyxis.greenhopper.jira:gh-sprint"}},
			{"id":"customfield_10006","name":"Epic Link","schema":{"custom":"com.pyxis.greenhopper.jira:gh-epic-link"}}]`),
		"GET /jira/rest/api/2/project":                                     ok(`[{"id":"1","key":"PLAT","name":"Platform"}]`),
		"GET /jira/rest/api/2/project/PLAT/statuses":                       ok(`[{"name":"Story","statuses":[{"id":"1","name":"Open","statusCategory":{"key":"new"}},{"id":"6","name":"Closed","statusCategory":{"key":"done"}}]}]`),
		"GET /jira/rest/agile/1.0/board?maxResults=50&projectKeyOrId=PLAT": {{status: 404, body: `{}`}},
		jiraSearchKey("/jira/rest/api/2/search", "PLAT", fields, url.Values{"startAt": {"0"}}): ok(`{"startAt":0,"total":2,"issues":[
			{"key":"PLAT-7","fields":{"summary":"Tune the cache","description":"h3. Why\n*Slow* pages. See [docs|https://example.com].",
				"status":{"id":"1","name":"Open","statusCategory":{"key":"new"}},"priority":{"name":"Minor"},
				"assignee":{"key":"ada","name":"ada","displayName":"Ada","emailAddress":"ada@example.com"},"reporter":null,
				"created":"2026-08-01T10:00:00.000+0000","updated":"2026-08-02T10:00:00.000+0000",
				"customfield_10002":2.5,"customfield_10005":["com.atlassian.greenhopper.service.sprint.Sprint@1f[id=12,rapidViewId=3,state=CLOSED,name=Sprint 12]"],
				"customfield_10006":"PLAT-1","comment":{"total":1,"comments":[{"id":"70","author":{"key":"ada","displayName":"Ada"},"body":"Tried {{redis}}.","created":"2026-08-02T10:00:00.000+0000"}]}}}]}`),
		jiraSearchKey("/jira/rest/api/2/search", "PLAT", fields, url.Values{"startAt": {"1"}}): ok(`{"startAt":1,"total":2,"issues":[
			{"key":"PLAT-8","fields":{"summary":"Second page","status":{"id":"6","name":"Closed","statusCategory":{"key":"done"}},
				"created":"2026-08-03T10:00:00.000+0000","updated":"2026-08-04T10:00:00.000+0000","comment":{"total":0,"comments":[]}}}]}`),
	}}
	source, err := NewJira(fake.client(), "https://jira.acme.internal/jira", "", "pat-123", nil)
	if err != nil {
		t.Fatal(err)
	}
	if source.origin() != "jira:jira.acme.internal/jira" {
		t.Errorf("origin = %s", source.origin())
	}
	all, _ := collect(t, source)
	// A personal access token goes as a bearer token.
	if got := fake.requests[0].Header.Get("Authorization"); got != "Bearer pat-123" {
		t.Errorf("authorization = %q", got)
	}
	if len(all.Issues) != 2 || len(all.Cycles) != 0 {
		t.Fatalf("%d issues, %d cycles", len(all.Issues), len(all.Cycles))
	}
	cache := all.Issues[0]
	// Wiki markup becomes Markdown; the epic link is the parent.
	if cache.Description != "### Why\n**Slow** pages. See [docs](https://example.com)." || cache.Parent != "PLAT-1" || cache.Priority != 4 ||
		cache.Assignee != "ada" || cache.Comments[0].Body != "Tried `redis`." || cache.Cycle != "" {
		t.Errorf("PLAT-7 = %+v", cache)
	}
	// Closed without a resolution date: closed when it was last changed.
	if second := all.Issues[1]; second.State != "PLAT:6" || second.CompletedAt == nil || !second.CompletedAt.Equal(second.UpdatedAt) {
		t.Errorf("PLAT-8 = %+v", second)
	}
	if len(all.Users) != 1 || all.Users[0].Email != "ada@example.com" {
		t.Errorf("users = %+v", all.Users)
	}
}

func TestJiraErrors(t *testing.T) {
	if _, err := NewJira(nil, "", "a@b.c", "tok", nil); err == nil {
		t.Error("an empty address was accepted")
	}
	if _, err := NewJira(nil, "https://acme.atlassian.net", "a@b.c", " ", nil); err == nil {
		t.Error("an empty token was accepted")
	}
	// Projects that aren't there.
	fake := jiraCloudFake(t)
	source, _ := NewJira(fake.client(), "https://acme.atlassian.net", "a@b.c", "tok", []string{"NOPE"})
	err := source.Fetch(context.Background(), func(Bundle) error { return nil }, func(string) {})
	if err == nil || !strings.Contains(err.Error(), "None of the chosen projects") {
		t.Errorf("unknown project: %v", err)
	}
}
