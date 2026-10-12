package blob

import (
	"context"
	"errors"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestDir(t *testing.T) {
	ctx := context.Background()
	root := t.TempDir()
	store, err := NewDir(root)
	if err != nil {
		t.Fatal(err)
	}

	if err := store.Put(ctx, "ws/file-1", strings.NewReader("hello"), 5, "text/plain"); err != nil {
		t.Fatal(err)
	}
	r, err := store.Get(ctx, "ws/file-1")
	if err != nil {
		t.Fatal(err)
	}
	got, _ := io.ReadAll(r)
	r.Close()
	if string(got) != "hello" {
		t.Errorf("Get = %q, want hello", got)
	}

	// No temporary files are left next to the blob.
	entries, _ := os.ReadDir(filepath.Join(root, "ws"))
	if len(entries) != 1 {
		t.Errorf("%d files in the directory, want 1", len(entries))
	}

	if err := store.Delete(ctx, "ws/file-1"); err != nil {
		t.Fatal(err)
	}
	if _, err := store.Get(ctx, "ws/file-1"); !errors.Is(err, ErrNotFound) {
		t.Errorf("Get after delete: %v, want ErrNotFound", err)
	}
	// Deleting something that isn't there is fine.
	if err := store.Delete(ctx, "ws/file-1"); err != nil {
		t.Errorf("second delete: %v", err)
	}

	// Keys can't climb out of the root.
	for _, key := range []string{"../escape", "ws/../../escape", ""} {
		if err := store.Put(ctx, key, strings.NewReader("x"), 1, ""); err == nil {
			t.Errorf("Put(%q) succeeded, want an error", key)
		}
	}
	if _, err := os.Stat(filepath.Join(filepath.Dir(root), "escape")); err == nil {
		t.Error("a file was written outside the root")
	}
}
