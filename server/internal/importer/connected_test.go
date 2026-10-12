package importer

import (
	"context"
	"strings"
	"testing"
)

// A token that is handed out, and replaced when it is refused.
type rotating struct {
	current string
	renewed int
}

func (r *rotating) token(_ context.Context, used string) (string, error) {
	if used != "" && used == r.current {
		r.current += "+"
		r.renewed++
	}
	return r.current, nil
}

func TestJiraThroughAConnection(t *testing.T) {
	// The same site, reached by way of Atlassian's address for it.
	fake := jiraCloudFake(t)
	site := fake.routes
	fake.routes = map[string][]fakeResponse{}
	for key, answers := range site {
		method, rest, _ := strings.Cut(key, " ")
		fake.routes[method+" /ex/jira/cloud-1"+rest] = answers
	}
	tokens := &rotating{current: "access"}
	source, err := NewJiraConnected(fake.client(), "cloud-1", "https://acme.atlassian.net/", tokens.token, []string{"web"})
	if err != nil {
		t.Fatal(err)
	}
	preview, err := source.Preview(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	// It is the same origin as with a token, so either way updates the other's import.
	if preview.Origin != "jira:acme.atlassian.net" || preview.Label != "acme.atlassian.net" {
		t.Errorf("preview = %+v", preview)
	}
	all, _ := collect(t, source)
	if len(all.Issues) != 2 || !strings.HasPrefix(all.Issues[0].URL, "https://acme.atlassian.net/browse/") {
		t.Fatalf("issues = %+v", all.Issues)
	}
	for _, req := range fake.requests {
		if req.URL.Host != "api.atlassian.com" || req.Header.Get("Authorization") != "Bearer access" {
			t.Fatalf("request to %s with %q", req.URL, req.Header.Get("Authorization"))
		}
	}
	if _, err := NewJiraConnected(fake.client(), "", "https://acme.atlassian.net", tokens.token, nil); err == nil {
		t.Error("a connection that names no site was accepted")
	}
}

func TestARefusedTokenIsRenewedOnce(t *testing.T) {
	fake := &fakeAPI{t: t, routes: map[string][]fakeResponse{
		"GET /thing": {{status: 401, body: `{}`}, {status: 200, body: `{"ok":true}`}},
		"GET /gone":  {{status: 401, body: `{}`}},
	}}
	tokens := &rotating{current: "old"}
	api := &apiClient{http: fake.client(), name: "Tracker", headers: map[string]string{}, token: tokens.token}
	var out struct {
		OK bool `json:"ok"`
	}
	if _, err := api.get(context.Background(), "https://tracker.example/thing", &out); err != nil || !out.OK {
		t.Fatalf("get = %+v, %v", out, err)
	}
	if got := fake.requests[1].Header.Get("Authorization"); got != "Bearer old+" || tokens.renewed != 1 {
		t.Errorf("second try sent %q after %d renewals", got, tokens.renewed)
	}
	// Still refused with the new one: that is the answer.
	if _, err := api.get(context.Background(), "https://tracker.example/gone", nil); err == nil || tokens.renewed != 2 || len(fake.requests) != 4 {
		t.Errorf("err = %v after %d renewals and %d requests", err, tokens.renewed, len(fake.requests))
	}
}
