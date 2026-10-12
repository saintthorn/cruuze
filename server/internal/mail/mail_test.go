package mail

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

type roundTrip func(*http.Request) (*http.Response, error)

func (f roundTrip) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }

func TestDirSender(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "outbox")
	sender := DirSender{Dir: dir}
	for _, subject := range []string{"First", "Second"} {
		if err := sender.Send(context.Background(), Message{To: "ada+test@example.com", Subject: subject, Text: "Hello\n"}); err != nil {
			t.Fatal(err)
		}
	}
	// An address can't climb out of the directory.
	if err := sender.Send(context.Background(), Message{To: "../../evil", Subject: "x", Text: "x"}); err != nil {
		t.Fatal(err)
	}
	files, err := os.ReadDir(dir)
	if err != nil || len(files) != 3 {
		t.Fatalf("%d files (%v), want 3", len(files), err)
	}
	var mine []string
	for _, f := range files { // ReadDir sorts by name
		if strings.HasSuffix(f.Name(), "-ada+test@example.com.txt") {
			mine = append(mine, f.Name())
		}
		if strings.Contains(f.Name(), "/") || strings.Contains(f.Name(), "..__") {
			t.Errorf("unsafe file name %q", f.Name())
		}
	}
	if len(mine) != 2 {
		t.Fatalf("files for ada = %v", mine)
	}
	latest, _ := os.ReadFile(filepath.Join(dir, mine[1]))
	if string(latest) != "To: ada+test@example.com\nSubject: Second\n\nHello\n" {
		t.Errorf("latest message = %q", latest)
	}
}

func TestResendSender(t *testing.T) {
	var got map[string]any
	status := http.StatusOK
	sender := ResendSender{APIKey: "re_test", From: "Cruuze <noreply@example.com>", Client: &http.Client{
		Transport: roundTrip(func(r *http.Request) (*http.Response, error) {
			if r.URL.String() != resendURL || r.Header.Get("Authorization") != "Bearer re_test" {
				t.Errorf("request = %s, auth %q", r.URL, r.Header.Get("Authorization"))
			}
			if err := json.NewDecoder(r.Body).Decode(&got); err != nil {
				t.Error(err)
			}
			return &http.Response{StatusCode: status, Body: io.NopCloser(strings.NewReader(`{"message":"nope"}`))}, nil
		}),
	}}

	msg := Message{To: "ada@example.com", Subject: "Hello", Text: "Hi Ada"}
	if err := sender.Send(context.Background(), msg); err != nil {
		t.Fatal(err)
	}
	to, _ := got["to"].([]any)
	if got["from"] != sender.From || len(to) != 1 || to[0] != msg.To || got["subject"] != msg.Subject || got["text"] != msg.Text {
		t.Errorf("payload = %v", got)
	}

	status = http.StatusForbidden
	if err := sender.Send(context.Background(), msg); err == nil || !strings.Contains(err.Error(), "403") {
		t.Errorf("error = %v, want one naming the 403", err)
	}
}
