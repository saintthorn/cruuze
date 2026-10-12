package importer

import (
	"context"
	"errors"
	"net/url"
	"strings"
	"testing"
	"time"
)

// scripted is a source that emits numbered bundles and then fails, or not.
type scripted struct {
	bundles int
	fail    error
	emitted int
}

func (s *scripted) Preview(context.Context) (Preview, error) { return Preview{}, nil }

func (s *scripted) Open(context.Context, Attachment) (ReadCloser, string, error) {
	return nil, "", errors.New("no files")
}

func (s *scripted) Fetch(ctx context.Context, emit func(Bundle) error, progress func(string)) error {
	for i := range s.bundles {
		if err := emit(Bundle{Issues: []Issue{{Number: int32(i)}}}); err != nil {
			return err
		}
		s.emitted++
	}
	return s.fail
}

func TestReadLoadsInOrderWhileTheSourceReadsOn(t *testing.T) {
	source := &scripted{bundles: 20}
	var got []int32
	err := Read(context.Background(), source, func(b Bundle) error {
		time.Sleep(time.Millisecond) // slower than the source
		got = append(got, b.Issues[0].Number)
		return nil
	}, func(string) {})
	if err != nil {
		t.Fatal(err)
	}
	for i, n := range got {
		if int(n) != i {
			t.Fatalf("loaded out of order: %v", got)
		}
	}
	if len(got) != 20 {
		t.Errorf("loaded %d bundles, want 20", len(got))
	}
}

func TestReadKeepsWhatArrivedBeforeTheSourceFailed(t *testing.T) {
	gone := errors.New("the tracker went away")
	loaded := 0
	err := Read(context.Background(), &scripted{bundles: 3, fail: gone}, func(Bundle) error {
		loaded++
		return nil
	}, func(string) {})
	if !errors.Is(err, gone) || loaded != 3 {
		t.Errorf("err = %v with %d bundles loaded, want the source's error and 3", err, loaded)
	}
}

func TestReadStopsTheSourceWhenLoadingFails(t *testing.T) {
	full := errors.New("the database is full")
	source := &scripted{bundles: 1000}
	loaded := 0
	err := Read(context.Background(), source, func(Bundle) error {
		loaded++
		if loaded == 2 {
			return full
		}
		return nil
	}, func(string) {})
	if !errors.Is(err, full) || loaded != 2 {
		t.Errorf("err = %v after %d loads, want the load's error after 2", err, loaded)
	}
	if source.emitted > 2+readAhead+1 {
		t.Errorf("the source handed over %d bundles after loading had failed", source.emitted)
	}
}

func TestJiraJQLSince(t *testing.T) {
	now := time.Date(2026, 10, 7, 12, 0, 0, 0, time.UTC)
	got := jiraJQLSince("ENG", now.Add(-90*time.Minute), now)
	if got != `project = "ENG" AND updated >= "-91m" ORDER BY created ASC` {
		t.Errorf("jql = %s", got)
	}
}

func TestGitHubReadsOnlyWhatChanged(t *testing.T) {
	fake := githubFake(t)
	since := time.Date(2026, 10, 1, 8, 0, 0, 0, time.UTC)
	// The same answers, at the addresses that ask only for what changed.
	for key, answers := range fake.routes {
		path, raw, _ := strings.Cut(key, "?")
		query, _ := url.ParseQuery(raw)
		if query.Get("sort") != "created" {
			continue
		}
		query.Set("since", since.Format(time.RFC3339))
		fake.routes[path+"?"+query.Encode()] = answers
		delete(fake.routes, key)
	}
	source, err := NewGitHub(fake.client(), "acme/widgets", "", "")
	if err != nil {
		t.Fatal(err)
	}
	source.ReadSince(since)
	if all, _ := collect(t, source); len(all.Issues) == 0 {
		t.Error("nothing read")
	}
}
