package importer

import (
	"context"
	"io"
	"net/http"
	"strings"
	"sync"
	"testing"
	"time"
)

// fakeAPI stands in for a tracker: it answers requests from a table of
// canned responses, keyed by method, path and query.
type fakeAPI struct {
	t  *testing.T
	mu sync.Mutex
	// routes maps "GET /path?query" to an answer. A list of answers is
	// served one after another, then the last one repeats.
	routes   map[string][]fakeResponse
	requests []*http.Request
	bodies   []string
}

type fakeResponse struct {
	status int
	body   string
	header map[string]string
}

func ok(body string) []fakeResponse { return []fakeResponse{{status: 200, body: body}} }

func (f *fakeAPI) RoundTrip(req *http.Request) (*http.Response, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	body := ""
	if req.Body != nil {
		raw, _ := io.ReadAll(req.Body)
		body = string(raw)
	}
	f.requests = append(f.requests, req)
	f.bodies = append(f.bodies, body)
	key := req.Method + " " + req.URL.Path
	if req.URL.RawQuery != "" {
		key += "?" + req.URL.RawQuery
	}
	answers, found := f.routes[key]
	if !found {
		f.t.Errorf("unexpected request: %s", key)
		return &http.Response{StatusCode: 404, Header: http.Header{}, Body: io.NopCloser(strings.NewReader(`{"message":"Not Found"}`)), Request: req}, nil
	}
	answer := answers[0]
	if len(answers) > 1 {
		f.routes[key] = answers[1:]
	}
	header := http.Header{}
	for name, value := range answer.header {
		header.Set(name, value)
	}
	return &http.Response{StatusCode: answer.status, Header: header, Body: io.NopCloser(strings.NewReader(answer.body)), Request: req}, nil
}

func (f *fakeAPI) client() *http.Client { return &http.Client{Transport: f} }

// collect runs a source's Fetch and merges everything it emits.
func collect(t *testing.T, s Source) (Bundle, []string) {
	t.Helper()
	var all Bundle
	var stages []string
	err := s.Fetch(context.Background(), func(b Bundle) error {
		all.Users = append(all.Users, b.Users...)
		all.Teams = append(all.Teams, b.Teams...)
		all.Labels = append(all.Labels, b.Labels...)
		all.Projects = append(all.Projects, b.Projects...)
		all.Cycles = append(all.Cycles, b.Cycles...)
		all.Issues = append(all.Issues, b.Issues...)
		all.Comments = append(all.Comments, b.Comments...)
		return nil
	}, func(stage string) { stages = append(stages, stage) })
	if err != nil {
		t.Fatalf("fetch: %v", err)
	}
	return all, stages
}

// noSleep makes waits instant for a test and records how long was asked for.
func noSleep(t *testing.T) *[]time.Duration {
	t.Helper()
	var waits []time.Duration
	original := sleep
	sleep = func(_ context.Context, d time.Duration) error {
		waits = append(waits, d)
		return nil
	}
	t.Cleanup(func() { sleep = original })
	return &waits
}
