package watcher

import (
	"context"
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestDebouncedModificationSendsOneEvent(t *testing.T) {
	path := writeTempMarkdown(t, "# original\n")
	w := newTestWatcher(t, path, 30*time.Millisecond)
	defer w.Close()

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	events, errs := w.Watch(ctx)

	for _, content := range []string{"# one\n", "# two\n", "# three\n"} {
		if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
			t.Fatal(err)
		}
		time.Sleep(5 * time.Millisecond)
	}

	event := waitForEvent(t, events, errs)
	if event.Path != absPath(t, path) {
		t.Fatalf("expected event path %q, got %q", absPath(t, path), event.Path)
	}
	assertNoEvent(t, events, errs, 90*time.Millisecond)
}

func TestWatcherSurvivesRenameAndRecreate(t *testing.T) {
	path := writeTempMarkdown(t, "# original\n")
	w := newTestWatcher(t, path, 20*time.Millisecond)
	defer w.Close()

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	events, errs := w.Watch(ctx)

	renamed := path + ".old"
	if err := os.Rename(path, renamed); err != nil {
		t.Fatal(err)
	}
	waitForEvent(t, events, errs)

	if err := os.WriteFile(path, []byte("# recreated\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	event := waitForEvent(t, events, errs)
	if event.Path != absPath(t, path) {
		t.Fatalf("expected recreated event path %q, got %q", absPath(t, path), event.Path)
	}
}

func TestWatcherSurvivesDeleteAndRecreate(t *testing.T) {
	path := writeTempMarkdown(t, "# original\n")
	w := newTestWatcher(t, path, 20*time.Millisecond)
	defer w.Close()

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	events, errs := w.Watch(ctx)

	if err := os.Remove(path); err != nil {
		t.Fatal(err)
	}
	waitForEvent(t, events, errs)

	if err := os.WriteFile(path, []byte("# recreated\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	event := waitForEvent(t, events, errs)
	if event.Path != absPath(t, path) {
		t.Fatalf("expected recreated event path %q, got %q", absPath(t, path), event.Path)
	}
}

func writeTempMarkdown(t *testing.T, content string) string {
	t.Helper()

	path := filepath.Join(t.TempDir(), "doc.md")
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
	return path
}

func newTestWatcher(t *testing.T, path string, debounce time.Duration) *Watcher {
	t.Helper()

	w, err := New(path, debounce)
	if err != nil {
		t.Fatal(err)
	}
	return w
}

func waitForEvent(t *testing.T, events <-chan Event, errs <-chan error) Event {
	t.Helper()

	select {
	case event, ok := <-events:
		if !ok {
			t.Fatal("events channel closed")
		}
		return event
	case err := <-errs:
		t.Fatalf("watcher error: %v", err)
	case <-time.After(2 * time.Second):
		t.Fatal("timed out waiting for watcher event")
	}
	return Event{}
}

func assertNoEvent(t *testing.T, events <-chan Event, errs <-chan error, d time.Duration) {
	t.Helper()

	select {
	case event := <-events:
		t.Fatalf("unexpected watcher event: %+v", event)
	case err := <-errs:
		t.Fatalf("watcher error: %v", err)
	case <-time.After(d):
	}
}

func absPath(t *testing.T, path string) string {
	t.Helper()

	abs, err := filepath.Abs(path)
	if err != nil {
		t.Fatal(err)
	}
	return filepath.Clean(abs)
}
