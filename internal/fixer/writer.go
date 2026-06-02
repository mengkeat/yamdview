package fixer

import (
	"bytes"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"
)

// Apply produces the new file content by applying validated patches in order.
// Patches must be sorted by StartByte and non-overlapping. Use SortPatches
// before calling if the input order is unknown.
func Apply(src []byte, patches []SourcePatch) []byte {
	if len(patches) == 0 {
		out := make([]byte, len(src))
		copy(out, src)
		return out
	}
	sorted := SortPatches(patches)
	var out bytes.Buffer
	cursor := 0
	for _, p := range sorted {
		if p.StartByte < cursor {
			// Skip overlapping patches to preserve well-formedness.
			continue
		}
		out.Write(src[cursor:p.StartByte])
		out.WriteString(p.NewText)
		cursor = p.EndByte
	}
	out.Write(src[cursor:])
	return out.Bytes()
}

// SortPatches returns a copy of the patches sorted by StartByte. Stable for
// ties: the relative order of patches sharing an offset is preserved.
func SortPatches(patches []SourcePatch) []SourcePatch {
	out := make([]SourcePatch, len(patches))
	copy(out, patches)
	sort.SliceStable(out, func(i, j int) bool {
		if out[i].StartByte != out[j].StartByte {
			return out[i].StartByte < out[j].StartByte
		}
		return out[i].EndByte < out[j].EndByte
	})
	return out
}

// WriteResult describes the outcome of a single WriteFixes operation.
type WriteResult struct {
	BackupPath string // empty when no backup was created
	WrittenAt  time.Time
	PatchCount int
}

// WriteFixes validates the patches against the current contents of path and,
// depending on the mode, applies them in place or after creating a backup.
// When mode is WriteModeNever, no file is written and the original content is
// returned via a no-op result.
func WriteFixes(path string, mode WriteMode, backupDir string, patches []SourcePatch) (WriteResult, error) {
	if mode == WriteModeNever {
		return WriteResult{}, nil
	}
	if len(patches) == 0 {
		return WriteResult{}, nil
	}

	original, err := os.ReadFile(path)
	if err != nil {
		return WriteResult{}, fmt.Errorf("read %s: %w", path, err)
	}
	if err := ValidatePatches(original, patches); err != nil {
		return WriteResult{}, fmt.Errorf("validate patches: %w", err)
	}

	updated := Apply(original, patches)
	if bytes.Equal(original, updated) {
		return WriteResult{}, nil
	}

	now := time.Now()
	result := WriteResult{WrittenAt: now, PatchCount: len(patches)}

	if mode == WriteModeBackup {
		backup, err := writeBackup(path, backupDir, original, now)
		if err != nil {
			return WriteResult{}, fmt.Errorf("write backup: %w", err)
		}
		result.BackupPath = backup
	}

	if err := atomicWrite(path, updated); err != nil {
		return WriteResult{}, fmt.Errorf("atomic write: %w", err)
	}
	return result, nil
}

func writeBackup(path, backupDir string, contents []byte, now time.Time) (string, error) {
	dir := backupDir
	if dir == "" {
		dir = filepath.Dir(path)
	}
	stamp := now.UTC().Format("20060102-150405")
	base := filepath.Base(path)
	backupPath := filepath.Join(dir, base+".bak-"+stamp)
	if _, err := os.Stat(backupPath); err == nil {
		// Add a counter suffix when the timestamp collides (rapid re-saves).
		for i := 2; ; i++ {
			candidate := filepath.Join(dir, fmt.Sprintf("%s.bak-%s-%d", base, stamp, i))
			if _, err := os.Stat(candidate); err != nil {
				backupPath = candidate
				break
			}
		}
	} else if !os.IsNotExist(err) {
		return "", err
	}
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return "", err
	}
	if err := writeFileAtomic(backupPath, contents, 0o644); err != nil {
		return "", err
	}
	return backupPath, nil
}

// atomicWrite writes contents to a temp file in the same directory as path
// and then renames it over the original. The rename is atomic on POSIX
// filesystems when the source and destination are on the same mount.
func atomicWrite(path string, contents []byte) error {
	dir := filepath.Dir(path)
	tmp, err := os.CreateTemp(dir, filepath.Base(path)+".yamdview.tmp-")
	if err != nil {
		return err
	}
	tmpName := tmp.Name()
	cleanup := func() {
		_ = os.Remove(tmpName)
	}
	if _, err := io.Copy(tmp, strings.NewReader(string(contents))); err != nil {
		_ = tmp.Close()
		cleanup()
		return err
	}
	if err := tmp.Sync(); err != nil {
		_ = tmp.Close()
		cleanup()
		return err
	}
	if err := tmp.Close(); err != nil {
		cleanup()
		return err
	}
	if err := os.Rename(tmpName, path); err != nil {
		cleanup()
		return err
	}
	return nil
}

// writeFileAtomic is a small wrapper used for backup files. It tries to keep
// the atomicity guarantee even for backups, so a partially-written backup
// does not overwrite an existing one.
func writeFileAtomic(path string, contents []byte, perm os.FileMode) error {
	dir := filepath.Dir(path)
	tmp, err := os.CreateTemp(dir, filepath.Base(path)+".tmp-")
	if err != nil {
		return err
	}
	tmpName := tmp.Name()
	if _, err := tmp.Write(contents); err != nil {
		_ = tmp.Close()
		_ = os.Remove(tmpName)
		return err
	}
	if err := tmp.Sync(); err != nil {
		_ = tmp.Close()
		_ = os.Remove(tmpName)
		return err
	}
	if err := tmp.Close(); err != nil {
		_ = os.Remove(tmpName)
		return err
	}
	if err := os.Chmod(tmpName, perm); err != nil {
		_ = os.Remove(tmpName)
		return err
	}
	if err := os.Rename(tmpName, path); err != nil {
		_ = os.Remove(tmpName)
		return err
	}
	return nil
}
