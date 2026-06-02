package app

import (
	"bufio"
	"context"
	"encoding/json"
	"html/template"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/mengkeat/yamdview/internal/document"
	"github.com/mengkeat/yamdview/internal/fixer"
	"github.com/mengkeat/yamdview/internal/markdown"
	"github.com/mengkeat/yamdview/internal/server"
	"github.com/mengkeat/yamdview/internal/watcher"
)

var testAssets = server.Assets{
	IndexHTML: `<!DOCTYPE html><html><head><title>{{.Title}}</title><style>{{.CSS}}</style></head><body><main id="document">{{.Content}}</main><script>{{.JS}}</script></body></html>`,
	ViewerCSS: "body { margin: 0; }",
	ViewerJS:  "// test js",
}

func TestReloadLoopBroadcastsReplaceForChangedParagraph(t *testing.T) {
	path, srv, reader, changes := startReloadLoopTest(t, "# Title\n\nOriginal paragraph.\n\nTail paragraph.\n")

	if err := os.WriteFile(path, []byte("# Title\n\nUpdated paragraph.\n\nTail paragraph.\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	changes <- watcher.Event{Path: path}

	payload := readPatchPayload(t, reader)
	if len(payload.Ops) != 1 {
		t.Fatalf("expected 1 patch op, got %+v", payload.Ops)
	}
	op := payload.Ops[0]
	if op.Op != document.OpReplace {
		t.Fatalf("expected replace op, got %+v", op)
	}
	if !strings.Contains(op.HTML, "Updated paragraph") {
		t.Fatalf("replace HTML missing updated paragraph:\n%s", op.HTML)
	}
	if strings.Contains(op.HTML, "Tail paragraph") {
		t.Fatalf("replace HTML should not include unchanged tail paragraph:\n%s", op.HTML)
	}

	assertSnapshotContains(t, srv, "Updated paragraph")
}

func TestReloadLoopBroadcastsInsertForAddedHeading(t *testing.T) {
	path, srv, reader, changes := startReloadLoopTest(t, "# Title\n\nParagraph.\n")

	if err := os.WriteFile(path, []byte("# Title\n\n## Inserted\n\nParagraph.\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	changes <- watcher.Event{Path: path}

	payload := readPatchPayload(t, reader)
	if len(payload.Ops) != 1 {
		t.Fatalf("expected 1 patch op, got %+v", payload.Ops)
	}
	op := payload.Ops[0]
	if op.Op != document.OpInsertAfter && op.Op != document.OpInsertBefore {
		t.Fatalf("expected insert op, got %+v", op)
	}
	if !strings.Contains(op.HTML, "Inserted") {
		t.Fatalf("insert HTML missing heading:\n%s", op.HTML)
	}

	assertSnapshotContains(t, srv, "Inserted")
}

func TestReloadLoopBroadcastsResetForReferenceFallback(t *testing.T) {
	path, srv, reader, changes := startReloadLoopTest(t, "See [docs][docs].\n\n[docs]: https://example.com\n")

	if err := os.WriteFile(path, []byte("See [docs][docs].\n\n[docs]: https://example.org\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	changes <- watcher.Event{Path: path}

	payload := readResetPayload(t, reader)
	if payload.Op != document.OpReset || !strings.Contains(payload.HTML, "https://example.org") {
		t.Fatalf("unexpected reset payload: %+v", payload)
	}

	assertSnapshotContains(t, srv, "https://example.org")
}

func startReloadLoopTest(t *testing.T, initial string) (string, *server.Server, *bufio.Reader, chan<- watcher.Event) {
	t.Helper()

	path := filepath.Join(t.TempDir(), "doc.md")
	if err := os.WriteFile(path, []byte(initial), 0o644); err != nil {
		t.Fatal(err)
	}

	application := New(Config{MarkdownPath: path}, testAssets)
	initialSnapshot, err := application.snapshotFile()
	if err != nil {
		t.Fatal(err)
	}

	srv, err := server.New("127.0.0.1:0", testAssets, server.PageData{
		Title:   path,
		Content: template.HTML(initialSnapshot.HTML),
	})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = srv.Close() })
	srv.Start()

	resp, err := http.Get(srv.URL() + "events")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = resp.Body.Close() })

	reader := bufio.NewReader(resp.Body)
	readSSEBlock(t, reader) // connected comment

	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)
	changes := make(chan watcher.Event, 1)
	watchErrs := make(chan error)
	go application.reloadLoop(ctx, srv, changes, watchErrs, initialSnapshot)

	return path, srv, reader, changes
}

func readPatchPayload(t *testing.T, reader *bufio.Reader) struct {
	Ops []document.PatchOp `json:"ops"`
} {
	t.Helper()

	block := readSSEBlock(t, reader)
	if len(block) != 2 || block[0] != "event: patch" || !strings.HasPrefix(block[1], "data: ") {
		t.Fatalf("expected patch SSE block, got %v", block)
	}

	var payload struct {
		Ops []document.PatchOp `json:"ops"`
	}
	if err := json.Unmarshal([]byte(strings.TrimPrefix(block[1], "data: ")), &payload); err != nil {
		t.Fatalf("unmarshal patch payload: %v", err)
	}
	return payload
}

func readResetPayload(t *testing.T, reader *bufio.Reader) struct {
	Op   string `json:"op"`
	HTML string `json:"html"`
} {
	t.Helper()

	block := readSSEBlock(t, reader)
	if len(block) != 2 || block[0] != "event: reset" || !strings.HasPrefix(block[1], "data: ") {
		t.Fatalf("expected reset SSE block, got %v", block)
	}

	var payload struct {
		Op   string `json:"op"`
		HTML string `json:"html"`
	}
	if err := json.Unmarshal([]byte(strings.TrimPrefix(block[1], "data: ")), &payload); err != nil {
		t.Fatalf("unmarshal reset payload: %v", err)
	}
	return payload
}

func readSSEBlock(t *testing.T, reader *bufio.Reader) []string {
	t.Helper()

	var lines []string
	for {
		line, err := reader.ReadString('\n')
		if err != nil {
			t.Fatalf("read SSE line: %v", err)
		}
		line = strings.TrimRight(line, "\r\n")
		if line == "" {
			return lines
		}
		lines = append(lines, line)
	}
}

func assertSnapshotContains(t *testing.T, srv *server.Server, want string) {
	t.Helper()

	snapshot, err := http.Get(srv.URL() + "snapshot")
	if err != nil {
		t.Fatal(err)
	}
	body, _ := io.ReadAll(snapshot.Body)
	snapshot.Body.Close()
	if !strings.Contains(string(body), want) {
		t.Fatalf("expected snapshot to contain %q, got %q", want, body)
	}
}

func TestExportWritesStandaloneFile(t *testing.T) {
	dir := t.TempDir()
	src := filepath.Join(dir, "doc.md")
	dst := filepath.Join(dir, "out.html")

	if err := os.WriteFile(src, []byte("# Exported\n\nHello, world.\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	application := New(Config{
		MarkdownPath: src,
		Export:       dst,
		ExportView:   "tablet",
	}, testAssets)

	if err := application.Run(); err != nil {
		t.Fatal(err)
	}

	data, err := os.ReadFile(dst)
	if err != nil {
		t.Fatal(err)
	}

	out := string(data)
	if !strings.Contains(out, "Hello, world.") {
		t.Error("exported file missing content")
	}
	if !strings.Contains(out, "Exported") {
		t.Error("exported file missing title")
	}
	if !strings.Contains(out, "--measure:40rem") {
		t.Error("exported file missing tablet viewport override")
	}
	if !strings.Contains(out, testAssets.ViewerCSS) {
		t.Error("exported file missing CSS")
	}
	if !strings.Contains(out, testAssets.ViewerJS) {
		t.Error("exported file missing JS")
	}
}

func TestExportWithoutViewport(t *testing.T) {
	dir := t.TempDir()
	src := filepath.Join(dir, "doc.md")
	dst := filepath.Join(dir, "out.html")

	if err := os.WriteFile(src, []byte("# Plain\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	application := New(Config{
		MarkdownPath: src,
		Export:       dst,
	}, testAssets)

	if err := application.Run(); err != nil {
		t.Fatal(err)
	}

	data, err := os.ReadFile(dst)
	if err != nil {
		t.Fatal(err)
	}

	out := string(data)
	if !strings.Contains(out, "Plain") {
		t.Error("exported file missing content")
	}
	// No export override comment should be present.
	if strings.Contains(out, "yamdview export") {
		t.Error("exported file should not contain viewport override comment")
	}
}

func TestExportAllViewports(t *testing.T) {
	tests := []struct {
		view    string
		measure string
	}{
		{"phone", "22rem"},
		{"tablet", "40rem"},
		{"laptop", "52rem"},
		{"desktop", "62rem"},
	}

	for _, tt := range tests {
		t.Run(tt.view, func(t *testing.T) {
			dir := t.TempDir()
			src := filepath.Join(dir, "doc.md")
			dst := filepath.Join(dir, "out.html")

			if err := os.WriteFile(src, []byte("# "+tt.view+"\n"), 0o644); err != nil {
				t.Fatal(err)
			}

			application := New(Config{
				MarkdownPath: src,
				Export:       dst,
				ExportView:   tt.view,
			}, testAssets)

			if err := application.Run(); err != nil {
				t.Fatal(err)
			}

			data, err := os.ReadFile(dst)
			if err != nil {
				t.Fatal(err)
			}

			out := string(data)
			if !strings.Contains(out, tt.view) {
				t.Errorf("export for %q missing content heading", tt.view)
			}
			want := "--measure:" + tt.measure
			if !strings.Contains(out, want) {
				t.Errorf("export for %q missing measure override %q", tt.view, want)
			}
			if !strings.Contains(out, "yamdview export") {
				t.Errorf("export for %q missing override comment", tt.view)
			}
			if !strings.Contains(out, testAssets.ViewerCSS) {
				t.Errorf("export for %q missing CSS", tt.view)
			}
			if !strings.Contains(out, testAssets.ViewerJS) {
				t.Errorf("export for %q missing JS", tt.view)
			}
		})
	}
}

func TestExportRejectsUnknownViewInApp(t *testing.T) {
	dir := t.TempDir()
	src := filepath.Join(dir, "doc.md")
	dst := filepath.Join(dir, "out.html")

	if err := os.WriteFile(src, []byte("# Bad\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	application := New(Config{
		MarkdownPath: src,
		Export:       dst,
		ExportView:   "watch",
	}, testAssets)

	err := application.Run()
	if err == nil {
		t.Fatal("expected error for unknown view in app export")
	}
	if !strings.Contains(err.Error(), "unknown --export-view") {
		t.Errorf("unexpected error: %v", err)
	}

	// The output file should not be created.
	if _, err := os.Stat(dst); err == nil {
		t.Error("output file should not exist after failed export")
	}
}

func TestExportOverwritesExistingFile(t *testing.T) {
	dir := t.TempDir()
	src := filepath.Join(dir, "doc.md")
	dst := filepath.Join(dir, "out.html")

	if err := os.WriteFile(src, []byte("# New\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(dst, []byte("stale"), 0o644); err != nil {
		t.Fatal(err)
	}

	application := New(Config{
		MarkdownPath: src,
		Export:       dst,
	}, testAssets)

	if err := application.Run(); err != nil {
		t.Fatal(err)
	}

	data, err := os.ReadFile(dst)
	if err != nil {
		t.Fatal(err)
	}

	out := string(data)
	if !strings.Contains(out, "New") {
		t.Error("export did not overwrite existing file")
	}
	if strings.Contains(out, "stale") {
		t.Error("export still contains stale content")
	}
}

func TestExportFailsOnUnwritablePath(t *testing.T) {
	dir := t.TempDir()
	src := filepath.Join(dir, "doc.md")
	dst := filepath.Join(dir, "subdir", "out.html") // subdir does not exist

	if err := os.WriteFile(src, []byte("# Bad path\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	application := New(Config{
		MarkdownPath: src,
		Export:       dst,
	}, testAssets)

	err := application.Run()
	if err == nil {
		t.Fatal("expected error for unwritable export path")
	}
}

func TestExportWithNonExistentMarkdown(t *testing.T) {
	application := New(Config{
		MarkdownPath: "/tmp/does-not-exist-928374.md",
		Export:       "/tmp/out.html",
	}, testAssets)

	err := application.Run()
	if err == nil {
		t.Fatal("expected error for missing markdown file")
	}
}

func TestPersistFixesNeverModeLeavesFileUntouched(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "doc.md")
	original := "Name | Score\nAlice | 10\nBob | 9\n"
	if err := os.WriteFile(path, []byte(original), 0o644); err != nil {
		t.Fatal(err)
	}

	application := New(Config{
		MarkdownPath: path,
		WriteFixes:   "never",
	}, testAssets)

	if err := application.persistFixes([]byte(original), mustSnapshot(t, original)); err != nil {
		t.Fatal(err)
	}
	data, _ := os.ReadFile(path)
	if string(data) != original {
		t.Errorf("never mode should not modify file: %q", data)
	}
}

func TestPersistFixesInPlaceRepairsTable(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "doc.md")
	original := "Name | Score\nAlice | 10\nBob | 9\n"
	if err := os.WriteFile(path, []byte(original), 0o644); err != nil {
		t.Fatal(err)
	}

	application := New(Config{
		MarkdownPath: path,
		WriteFixes:   "in-place",
	}, testAssets)

	src, snapshot, err := application.readAndSnapshot()
	if err != nil {
		t.Fatal(err)
	}
	if err := application.persistFixes(src, snapshot); err != nil {
		t.Fatal(err)
	}
	data, _ := os.ReadFile(path)
	updated := string(data)
	if updated == original {
		t.Fatalf("expected file to be modified in-place")
	}
	if !strings.Contains(updated, "| --- |") {
		t.Errorf("repaired file missing separator: %q", updated)
	}
}

func TestPersistFixesCombinesTableAndMath(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "doc.md")
	original := "Name | Formula\nf | α²\n"
	if err := os.WriteFile(path, []byte(original), 0o644); err != nil {
		t.Fatal(err)
	}

	application := New(Config{
		MarkdownPath: path,
		WriteFixes:   "in-place",
	}, testAssets)

	src, snapshot, err := application.readAndSnapshot()
	if err != nil {
		t.Fatal(err)
	}
	if err := application.persistFixes(src, snapshot); err != nil {
		t.Fatal(err)
	}
	data, _ := os.ReadFile(path)
	updated := string(data)
	if !strings.Contains(updated, "| --- |") {
		t.Fatalf("repaired file missing separator: %q", updated)
	}
	if !strings.Contains(updated, "$") || !strings.Contains(updated, `\alpha`) {
		t.Fatalf("repaired file missing math conversion: %q", updated)
	}
}

func TestPersistFixesRepairsTableDuringFullResetSnapshot(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "doc.md")
	original := "Name | Score\nAlice | 10\n\n[docs]: https://example.com\n"
	if err := os.WriteFile(path, []byte(original), 0o644); err != nil {
		t.Fatal(err)
	}

	application := New(Config{
		MarkdownPath: path,
		WriteFixes:   "in-place",
	}, testAssets)

	src, snapshot, err := application.readAndSnapshot()
	if err != nil {
		t.Fatal(err)
	}
	if !snapshot.FullResetOnly {
		t.Fatal("expected reference definition to force full-reset snapshot")
	}
	if err := application.persistFixes(src, snapshot); err != nil {
		t.Fatal(err)
	}
	data, _ := os.ReadFile(path)
	updated := string(data)
	if !strings.Contains(updated, "| --- |") {
		t.Fatalf("repaired file missing separator: %q", updated)
	}
	if !strings.Contains(updated, "[docs]: https://example.com") {
		t.Fatalf("reference definition should be preserved: %q", updated)
	}
}

func TestPersistFixesBackupCreatesBackup(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "doc.md")
	original := "Name | Score\nAlice | 10\nBob | 9\n"
	if err := os.WriteFile(path, []byte(original), 0o644); err != nil {
		t.Fatal(err)
	}

	application := New(Config{
		MarkdownPath: path,
		WriteFixes:   "backup",
	}, testAssets)

	src, snapshot, err := application.readAndSnapshot()
	if err != nil {
		t.Fatal(err)
	}
	if err := application.persistFixes(src, snapshot); err != nil {
		t.Fatal(err)
	}

	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatal(err)
	}
	var backup string
	for _, e := range entries {
		if strings.HasPrefix(e.Name(), "doc.md.bak-") {
			backup = filepath.Join(dir, e.Name())
			break
		}
	}
	if backup == "" {
		t.Fatalf("expected backup file in %s, got entries: %+v", dir, entries)
	}
	backupData, err := os.ReadFile(backup)
	if err != nil {
		t.Fatal(err)
	}
	if string(backupData) != original {
		t.Errorf("backup should preserve original: %q", backupData)
	}
}

func TestExportPersistsFixesWhenRequested(t *testing.T) {
	dir := t.TempDir()
	src := filepath.Join(dir, "doc.md")
	dst := filepath.Join(dir, "out.html")
	original := "Name | Score\nAlice | 10\n"

	if err := os.WriteFile(src, []byte(original), 0o644); err != nil {
		t.Fatal(err)
	}

	application := New(Config{
		MarkdownPath: src,
		Export:       dst,
		WriteFixes:   "backup",
	}, testAssets)

	if err := application.Run(); err != nil {
		t.Fatal(err)
	}

	updated, err := os.ReadFile(src)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(updated), "| --- |") {
		t.Fatalf("export with write-fixes should update source: %q", updated)
	}
	if _, err := os.Stat(dst); err != nil {
		t.Fatalf("export file missing: %v", err)
	}

	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatal(err)
	}
	foundBackup := false
	for _, e := range entries {
		if strings.HasPrefix(e.Name(), "doc.md.bak-") {
			foundBackup = true
		}
	}
	if !foundBackup {
		t.Fatalf("expected backup next to source, got entries: %+v", entries)
	}
}

func TestPersistFixesRejectsStalePatches(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "doc.md")
	if err := os.WriteFile(path, []byte("# Title\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	application := New(Config{
		MarkdownPath: path,
		WriteFixes:   "in-place",
	}, testAssets)

	// Build a snapshot from a stale source to simulate a race where the file
	// has changed since the patches were computed.
	staleSrc := []byte("Name | Score\nAlice | 10\nBob | 9\n")
	staleSnapshot := mustSnapshot(t, string(staleSrc))

	err := application.persistFixes(staleSrc, staleSnapshot)
	if err == nil {
		t.Fatal("expected stale-patch rejection")
	}
	data, _ := os.ReadFile(path)
	if string(data) != "# Title\n" {
		t.Errorf("rejected write should leave file untouched: %q", data)
	}
}

func TestReloadLoopPersistsFixesOnSave(t *testing.T) {
	path, _, _, changes := startReloadLoopTestWithFixes(
		t,
		"Name | Score\nAlice | 10\nBob | 9\n",
		"backup",
		"",
	)

	// The initial persist should have produced a backup.
	initial := filepath.Dir(path)
	entries, err := os.ReadDir(initial)
	if err != nil {
		t.Fatal(err)
	}
	foundInitial := false
	for _, e := range entries {
		if strings.HasPrefix(e.Name(), filepath.Base(path)+".bak-") {
			foundInitial = true
		}
	}
	if !foundInitial {
		t.Fatalf("expected initial backup in %s, got %+v", initial, entries)
	}

	// Now simulate a user edit that introduces another malformed table.
	if err := os.WriteFile(path, []byte("Name | Score\nAlice | 10\nBob | 9\n\nA | B\nC | D\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	changes <- watcher.Event{Path: path}

	// Give the reload loop a moment to run.
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		entries, _ := os.ReadDir(initial)
		count := 0
		for _, e := range entries {
			if strings.HasPrefix(e.Name(), filepath.Base(path)+".bak-") {
				count++
			}
		}
		if count >= 2 {
			return
		}
		time.Sleep(20 * time.Millisecond)
	}
	entries, _ = os.ReadDir(initial)
	names := make([]string, 0, len(entries))
	for _, e := range entries {
		names = append(names, e.Name())
	}
	t.Fatalf("expected at least 2 backups after second reload, got: %v", names)
}

func startReloadLoopTestWithFixes(t *testing.T, initial, mode, backupDir string) (string, *server.Server, *bufio.Reader, chan<- watcher.Event) {
	t.Helper()

	path := filepath.Join(t.TempDir(), "doc.md")
	if err := os.WriteFile(path, []byte(initial), 0o644); err != nil {
		t.Fatal(err)
	}

	application := New(Config{
		MarkdownPath: path,
		WriteFixes:   fixer.WriteMode(mode),
		BackupDir:    backupDir,
	}, testAssets)
	src, initialSnapshot, err := application.readAndSnapshot()
	if err != nil {
		t.Fatal(err)
	}
	if application.cfg.WriteFixes != fixer.WriteModeNever {
		if err := application.persistFixes(src, initialSnapshot); err != nil {
			t.Fatal(err)
		}
	}

	srv, err := server.New("127.0.0.1:0", testAssets, server.PageData{
		Title:   path,
		Content: template.HTML(initialSnapshot.HTML),
	})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = srv.Close() })
	srv.Start()

	resp, err := http.Get(srv.URL() + "events")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = resp.Body.Close() })

	reader := bufio.NewReader(resp.Body)
	readSSEBlock(t, reader)

	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)
	changes := make(chan watcher.Event, 1)
	watchErrs := make(chan error)
	go application.reloadLoop(ctx, srv, changes, watchErrs, initialSnapshot)

	return path, srv, reader, changes
}

func mustSnapshot(t *testing.T, source string) document.DocumentSnapshot {
	t.Helper()
	md := markdown.NewRenderer()
	snapshot, err := document.BuildSnapshot(md, []byte(source))
	if err != nil {
		t.Fatal(err)
	}
	return snapshot
}
