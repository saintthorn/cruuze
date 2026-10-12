package importer

import (
	"context"
	"errors"
	"strconv"
	"strings"
	"testing"
	"time"
)

const ghRepo = "/repos/acme/widgets"

func githubFake(t *testing.T) *fakeAPI {
	return &fakeAPI{t: t, routes: map[string][]fakeResponse{
		"GET " + ghRepo: ok(`{"full_name":"acme/widgets","has_issues":true}`),
		"GET /search/issues?per_page=1&q=repo%3Aacme%2Fwidgets+is%3Aissue": ok(`{"total_count":3}`),
		"GET " + ghRepo + "/assignees?per_page=100":                        ok(`[{"login":"ada"},{"login":"bob"}]`),
		"GET " + ghRepo + "/labels?per_page=100":                           ok(`[{"name":"bug","color":"d73a4a"},{"name":"good first issue","color":"7057ff"}]`),
		"GET " + ghRepo + "/milestones?per_page=100&state=all": ok(`[
			{"number":1,"title":"v1.0","description":"First release","state":"closed","due_on":"2026-03-01T08:00:00Z"},
			{"number":2,"title":"v2.0","description":"","state":"open","due_on":null}]`),
		// Two pages of issues; the second request is whatever the Link header says.
		"GET " + ghRepo + "/issues?direction=asc&per_page=100&sort=created&state=all": {{status: 200, header: map[string]string{
			"Link": `<https://api.github.com` + ghRepo + `/issues?page=2>; rel="next", <https://api.github.com` + ghRepo + `/issues?page=2>; rel="last"`,
		}, body: `[
			{"number":1,"title":"Crash on start","body":"It crashes.","state":"closed","state_reason":"completed",
			 "user":{"login":"ada"},"assignee":{"login":"bob"},"labels":[{"name":"bug","color":"d73a4a"}],
			 "milestone":{"number":1,"title":"v1.0"},"created_at":"2026-01-10T09:00:00Z","updated_at":"2026-02-01T10:00:00Z",
			 "closed_at":"2026-02-01T10:00:00Z","html_url":"https://github.com/acme/widgets/issues/1"},
			{"number":2,"title":"Add dark mode","body":null,"state":"open","state_reason":null,
			 "user":{"login":"stranger"},"assignee":null,"labels":[],"milestone":null,
			 "created_at":"2026-01-11T09:00:00Z","updated_at":"2026-01-11T09:00:00Z","closed_at":null,
			 "html_url":"https://github.com/acme/widgets/issues/2"},
			{"number":3,"title":"A pull request","body":"","state":"open","user":{"login":"ada"},"labels":[],
			 "created_at":"2026-01-12T09:00:00Z","updated_at":"2026-01-12T09:00:00Z",
			 "pull_request":{"url":"https://api.github.com/repos/acme/widgets/pulls/3"},"html_url":"https://github.com/acme/widgets/pull/3"}]`}},
		"GET " + ghRepo + "/issues?page=2": ok(`[
			{"number":4,"title":"Won't do this","body":"No.","state":"closed","state_reason":"not_planned",
			 "user":{"login":"bob"},"assignee":null,"labels":[{"name":"good first issue","color":"7057ff"}],"milestone":null,
			 "created_at":"2026-01-13T09:00:00Z","updated_at":"2026-01-14T09:00:00Z","closed_at":"2026-01-14T09:00:00Z",
			 "html_url":"https://github.com/acme/widgets/issues/4"}]`),
		"GET " + ghRepo + "/issues/comments?direction=asc&per_page=100&sort=created": ok(`[
			{"id":9001,"body":"Fixed in #3.","user":{"login":"bob"},"issue_url":"https://api.github.com/repos/acme/widgets/issues/1","created_at":"2026-02-01T09:59:00Z"},
			{"id":9002,"body":"LGTM","user":{"login":"carol"},"issue_url":"https://api.github.com/repos/acme/widgets/issues/3","created_at":"2026-02-02T09:00:00Z"}]`),
	}}
}

func TestGitHubPreview(t *testing.T) {
	fake := githubFake(t)
	source, err := NewGitHub(fake.client(), " https://github.com/acme/widgets.git ", "ghp_secret", "")
	if err != nil {
		t.Fatal(err)
	}
	preview, err := source.Preview(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if preview.Origin != "github:acme/widgets" || preview.Label != "acme/widgets" || len(preview.Teams) != 1 || len(preview.Users) != 2 {
		t.Fatalf("preview = %+v", preview)
	}
	if team := preview.Teams[0]; team.Key != "WIDGETS" || team.Name != "widgets" || team.Issues != 3 {
		t.Errorf("team = %+v", team)
	}
	if u := preview.Users[0]; u.ID != "ada" || u.GitHubLogin != "ada" {
		t.Errorf("user = %+v", u)
	}
	// The token goes in a header, on every request, and nowhere else.
	for _, req := range fake.requests {
		if req.Header.Get("Authorization") != "Bearer ghp_secret" || strings.Contains(req.URL.String(), "ghp_secret") {
			t.Errorf("request %s: authorization %q", req.URL, req.Header.Get("Authorization"))
		}
	}
}

func TestGitHubFetch(t *testing.T) {
	source, err := NewGitHub(githubFake(t).client(), "acme/widgets", "", "")
	if err != nil {
		t.Fatal(err)
	}
	all, stages := collect(t, source)

	if len(all.Teams) != 1 || all.Teams[0].ID != "github:acme/widgets" || len(all.Teams[0].States) != 3 {
		t.Fatalf("teams = %+v", all.Teams)
	}
	if len(all.Labels) != 2 || all.Labels[0].Color != "d73a4a" {
		t.Errorf("labels = %+v", all.Labels)
	}
	// Milestones become projects.
	if len(all.Projects) != 2 || all.Projects[0].Name != "v1.0" || all.Projects[0].Status != "completed" || !strings.HasPrefix(all.Projects[0].TargetDate, "2026-03-01") || all.Projects[1].Status != "started" {
		t.Errorf("projects = %+v", all.Projects)
	}
	// Three issues: the pull request is left out.
	if len(all.Issues) != 3 {
		t.Fatalf("%d issues, want 3", len(all.Issues))
	}
	first, second, third := all.Issues[0], all.Issues[1], all.Issues[2]
	if first.ID != "1" || first.Number != 1 || first.State != ghCompleted || first.Creator != "ada" || first.Assignee != "bob" ||
		first.Project != "milestone:1" || len(first.Labels) != 1 || first.CompletedAt == nil || first.CompletedAt.Format(time.DateOnly) != "2026-02-01" ||
		first.URL != "https://github.com/acme/widgets/issues/1" || first.CreatedAt.IsZero() {
		t.Errorf("issue 1 = %+v", first)
	}
	if second.State != ghOpen || second.Description != "" || second.Creator != "stranger" || second.Assignee != "" || second.CompletedAt != nil {
		t.Errorf("issue 2 = %+v", second)
	}
	if third.Number != 4 || third.State != ghNotPlanned {
		t.Errorf("issue 4 = %+v", third)
	}
	// Everyone who appears is listed once, so they can be matched or credited.
	var logins []string
	for _, u := range all.Users {
		logins = append(logins, u.ID)
	}
	if got := strings.Join(logins, ","); got != "ada,bob,stranger,carol" {
		t.Errorf("users = %s", got)
	}
	// Comments name their issue; the one on the pull request is the loader's to drop.
	if len(all.Comments) != 2 || all.Comments[0].Issue != "1" || all.Comments[0].ID != "9001" || all.Comments[0].Author != "bob" || all.Comments[1].Issue != "3" {
		t.Errorf("comments = %+v", all.Comments)
	}
	if len(stages) == 0 || !strings.Contains(stages[len(stages)-1], "comments") {
		t.Errorf("stages = %v", stages)
	}
}

func TestGitHubErrors(t *testing.T) {
	if _, err := NewGitHub(nil, "not a repo", "", ""); err == nil {
		t.Error("a bad repository name was accepted")
	}
	if _, err := NewGitHub(nil, "acme/widgets", "", "ftp://nope"); err == nil {
		t.Error("a bad enterprise address was accepted")
	}
	enterprise, err := NewGitHub(nil, "acme/widgets", "", "https://github.example.com/api/v3/")
	if err != nil || enterprise.origin() != "github:github.example.com:acme/widgets" || enterprise.repoURL("", nil) != "https://github.example.com/api/v3/repos/acme/widgets" {
		t.Errorf("enterprise source = %+v, %v", enterprise, err)
	}

	userMessage := func(routes map[string][]fakeResponse) string {
		t.Helper()
		source, _ := NewGitHub((&fakeAPI{t: t, routes: routes}).client(), "acme/widgets", "bad", "")
		_, err := source.Preview(context.Background())
		var userErr *UserError
		if !errors.As(err, &userErr) {
			t.Fatalf("error = %v, want one for the user", err)
		}
		return userErr.Message
	}
	if got := userMessage(map[string][]fakeResponse{"GET " + ghRepo: {{status: 401, body: `{"message":"Bad credentials"}`}}}); !strings.Contains(got, "refused the credentials") {
		t.Errorf("401: %q", got)
	}
	if got := userMessage(map[string][]fakeResponse{"GET " + ghRepo: {{status: 404, body: `{}`}}}); !strings.Contains(got, "can't see it") {
		t.Errorf("404: %q", got)
	}
	if got := userMessage(map[string][]fakeResponse{"GET " + ghRepo: ok(`{"full_name":"acme/widgets","has_issues":false}`)}); !strings.Contains(got, "issues turned off") {
		t.Errorf("no issues: %q", got)
	}
}

func TestRateLimitIsWaitedOut(t *testing.T) {
	waits := noSleep(t)
	reset := strconv.FormatInt(time.Now().Add(90*time.Second).Unix(), 10)
	fake := &fakeAPI{t: t, routes: map[string][]fakeResponse{
		"GET /thing": {
			{status: 403, body: `{"message":"API rate limit exceeded"}`, header: map[string]string{"X-RateLimit-Remaining": "0", "X-RateLimit-Reset": reset}},
			{status: 429, body: `{}`, header: map[string]string{"Retry-After": "7"}},
			{status: 502, body: `bad gateway`},
			{status: 200, body: `{"ok":true}`},
		},
	}}
	var stages []string
	api := &apiClient{http: fake.client(), name: "GitHub", progress: func(s string) { stages = append(stages, s) }}
	var out struct{ OK bool }
	if _, err := api.get(context.Background(), "https://api.example/thing", &out); err != nil || !out.OK {
		t.Fatalf("get: %v, %+v", err, out)
	}
	if len(*waits) != 3 || (*waits)[0] < 80*time.Second || (*waits)[0] > 100*time.Second || (*waits)[1] != 8*time.Second {
		t.Errorf("waits = %v", *waits)
	}
	if len(stages) != 2 || !strings.Contains(stages[0], "Waiting for GitHub") {
		t.Errorf("stages = %v", stages)
	}
	// A limit that won't lift within the hour is reported, not sat through.
	far := strconv.FormatInt(time.Now().Add(3*time.Hour).Unix(), 10)
	fake.routes["GET /later"] = []fakeResponse{{status: 403, header: map[string]string{"X-RateLimit-Remaining": "0", "X-RateLimit-Reset": far}}}
	_, err := api.get(context.Background(), "https://api.example/later", nil)
	var userErr *UserError
	if !errors.As(err, &userErr) || !strings.Contains(userErr.Message, "limiting") {
		t.Errorf("a three-hour limit: %v", err)
	}
}
