package fixer

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestApplyEmpty(t *testing.T) {
	src := []byte("hello world")
	out := Apply(src, nil)
	if string(out) != string(src) {
		t.Fatalf("expected unchanged output, got %q", out)
	}
}

func TestApplySinglePatch(t *testing.T) {
	src := []byte("hello world")
	patches := []SourcePatch{
		{StartByte: 6, EndByte: 11, OldText: "world", NewText: "Go"},
	}
	out := Apply(src, patches)
	if string(out) != "hello Go" {
		t.Fatalf("expected %q, got %q", "hello Go", out)
	}
}

func TestApplyMultiplePatches(t *testing.T) {
	src := []byte("aaa bbb ccc")
	patches := []SourcePatch{
		{StartByte: 0, EndByte: 3, OldText: "aaa", NewText: "AAA"},
		{StartByte: 4, EndByte: 7, OldText: "bbb", NewText: "BBB"},
		{StartByte: 8, EndByte: 11, OldText: "ccc", NewText: "CCC"},
	}
	out := Apply(src, patches)
	if string(out) != "AAA BBB CCC" {
		t.Fatalf("expected %q, got %q", "AAA BBB CCC", out)
	}
}

func TestApplyInsertion(t *testing.T) {
	src := []byte("hello world")
	patches := []SourcePatch{
		{StartByte: 5, EndByte: 5, OldText: "", NewText: " dear"},
	}
	out := Apply(src, patches)
	if string(out) != "hello dear world" {
		t.Fatalf("expected %q, got %q", "hello dear world", out)
	}
}

func TestApplyDeletion(t *testing.T) {
	src := []byte("hello dear world")
	patches := []SourcePatch{
		{StartByte: 6, EndByte: 11, OldText: "dear ", NewText: ""},
	}
	out := Apply(src, patches)
	if string(out) != "hello world" {
		t.Fatalf("expected %q, got %q", "hello world", out)
	}
}

func TestSortPatchesUnordered(t *testing.T) {
	patches := []SourcePatch{
		{StartByte: 8, EndByte: 11, OldText: "ccc"},
		{StartByte: 0, EndByte: 3, OldText: "aaa"},
		{StartByte: 4, EndByte: 7, OldText: "bbb"},
	}
	sorted := SortPatches(patches)
	want := []int{0, 4, 8}
	for i, p := range sorted {
		if p.StartByte != want[i] {
			t.Errorf("sorted[%d].StartByte = %d, want %d", i, p.StartByte, want[i])
		}
	}
}

func TestWriteFixesNeverModeIsNoOp(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "doc.md")
	if err := os.WriteFile(path, []byte("hello\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	result, err := WriteFixes(path, WriteModeNever, "", []SourcePatch{
		{StartByte: 0, EndByte: 5, OldText: "hello", NewText: "world"},
	})
	if err != nil {
		t.Fatal(err)
	}
	if result.PatchCount != 0 || result.BackupPath != "" {
		t.Errorf("never mode should be a no-op, got %+v", result)
	}
	data, _ := os.ReadFile(path)
	if string(data) != "hello\n" {
		t.Errorf("never mode modified file: %q", data)
	}
}

func TestWriteFixesInPlace(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "doc.md")
	if err := os.WriteFile(path, []byte("hello world\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	patches := []SourcePatch{
		{StartByte: 6, EndByte: 11, OldText: "world", NewText: "Go", Source: SourceHeuristicTable},
	}
	result, err := WriteFixes(path, WriteModeInPlace, "", patches)
	if err != nil {
		t.Fatal(err)
	}
	if result.PatchCount != 1 {
		t.Errorf("expected PatchCount=1, got %d", result.PatchCount)
	}
	if result.BackupPath != "" {
		t.Errorf("in-place should not create a backup, got %q", result.BackupPath)
	}
	data, _ := os.ReadFile(path)
	if string(data) != "hello Go\n" {
		t.Errorf("unexpected file content: %q", data)
	}
}

func TestWriteFixesBackup(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "doc.md")
	if err := os.WriteFile(path, []byte("hello world\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	patches := []SourcePatch{
		{StartByte: 6, EndByte: 11, OldText: "world", NewText: "Go", Source: SourceHeuristicTable},
	}
	result, err := WriteFixes(path, WriteModeBackup, "", patches)
	if err != nil {
		t.Fatal(err)
	}
	if result.BackupPath == "" {
		t.Fatal("expected backup path, got empty string")
	}
	if !strings.HasPrefix(filepath.Base(result.BackupPath), "doc.md.bak-") {
		t.Errorf("unexpected backup name: %q", result.BackupPath)
	}
	backup, err := os.ReadFile(result.BackupPath)
	if err != nil {
		t.Fatal(err)
	}
	if string(backup) != "hello world\n" {
		t.Errorf("backup should preserve original: %q", backup)
	}
	data, _ := os.ReadFile(path)
	if string(data) != "hello Go\n" {
		t.Errorf("unexpected file content after backup: %q", data)
	}
}

func TestWriteFixesBackupWithExplicitDir(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "doc.md")
	backupDir := filepath.Join(dir, "backups")
	if err := os.MkdirAll(backupDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte("alpha\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	patches := []SourcePatch{
		{StartByte: 0, EndByte: 5, OldText: "alpha", NewText: "beta", Source: SourceHeuristicTable},
	}
	result, err := WriteFixes(path, WriteModeBackup, backupDir, patches)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.HasPrefix(result.BackupPath, backupDir) {
		t.Errorf("backup %q should be inside %q", result.BackupPath, backupDir)
	}
}

func TestWriteFixesRejectsStalePatches(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "doc.md")
	if err := os.WriteFile(path, []byte("hello\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	patches := []SourcePatch{
		{StartByte: 0, EndByte: 5, OldText: "world", NewText: "Go"},
	}
	_, err := WriteFixes(path, WriteModeInPlace, "", patches)
	if err == nil {
		t.Fatal("expected stale patch error")
	}
	if !strings.Contains(err.Error(), "does not match") {
		t.Errorf("expected mismatch error, got %v", err)
	}
	data, _ := os.ReadFile(path)
	if string(data) != "hello\n" {
		t.Errorf("file should not be modified on rejection: %q", data)
	}
}

func TestWriteFixesAtomicRename(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "doc.md")
	if err := os.WriteFile(path, []byte("alpha\nbeta\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	patches := []SourcePatch{
		{StartByte: 0, EndByte: 11, OldText: "alpha\nbeta\n", NewText: "gamma\ndelta\nepsilon\n", Source: SourceHeuristicTable},
	}
	if _, err := WriteFixes(path, WriteModeInPlace, "", patches); err != nil {
		t.Fatal(err)
	}
	data, _ := os.ReadFile(path)
	if string(data) != "gamma\ndelta\nepsilon\n" {
		t.Errorf("unexpected file content: %q", data)
	}
	// No temp file should remain in the directory.
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatal(err)
	}
	for _, e := range entries {
		if strings.Contains(e.Name(), ".yamdview.tmp-") || strings.Contains(e.Name(), ".tmp-") {
			t.Errorf("temp file should be cleaned up, found %q", e.Name())
		}
	}
}

func TestWriteFixesBackupCollision(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "doc.md")
	if err := os.WriteFile(path, []byte("alpha\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	// Force a collision on the timestamped name.
	stamp := time.Now().UTC().Format("20060102-150405")
	if err := os.WriteFile(filepath.Join(dir, "doc.md.bak-"+stamp), []byte("existing"), 0o644); err != nil {
		t.Fatal(err)
	}
	patches := []SourcePatch{
		{StartByte: 0, EndByte: 5, OldText: "alpha", NewText: "beta", Source: SourceHeuristicTable},
	}
	result, err := WriteFixes(path, WriteModeBackup, "", patches)
	if err != nil {
		t.Fatal(err)
	}
	if result.BackupPath == filepath.Join(dir, "doc.md.bak-"+stamp) {
		t.Errorf("backup should not overwrite existing file: %q", result.BackupPath)
	}
	originalBackup, _ := os.ReadFile(filepath.Join(dir, "doc.md.bak-"+stamp))
	if string(originalBackup) != "existing" {
		t.Errorf("existing backup file should be preserved: %q", originalBackup)
	}
}
