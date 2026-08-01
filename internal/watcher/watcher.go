// Package watcher provides debounced file watching for Markdown reloads.
package watcher

import (
	"context"
	"fmt"
	"path/filepath"
	"time"

	"github.com/fsnotify/fsnotify"
)

// DefaultDebounce is the fallback debounce duration for file reload events.
const DefaultDebounce = 150 * time.Millisecond

// Event reports that the watched file should be reloaded.
type Event struct {
	Path string
}

// Watcher watches a Markdown file's parent directory so atomic editor saves
// that delete, rename, or recreate the file are still observed.
type Watcher struct {
	path     string
	debounce time.Duration
	fsw      *fsnotify.Watcher
}

// New creates a debounced watcher for path.
func New(path string, debounce time.Duration) (*Watcher, error) {
	absPath, err := filepath.Abs(path)
	if err != nil {
		return nil, fmt.Errorf("resolve path: %w", err)
	}
	absPath = filepath.Clean(absPath)
	if debounce <= 0 {
		debounce = DefaultDebounce
	}

	fsw, err := fsnotify.NewWatcher()
	if err != nil {
		return nil, fmt.Errorf("create watcher: %w", err)
	}
	if err := fsw.Add(filepath.Dir(absPath)); err != nil {
		fsw.Close()
		return nil, fmt.Errorf("watch directory: %w", err)
	}

	return &Watcher{path: absPath, debounce: debounce, fsw: fsw}, nil
}

// Close releases the underlying OS watcher.
func (w *Watcher) Close() error {
	return w.fsw.Close()
}

// Watch starts forwarding debounced reload events until ctx is canceled or the
// underlying watcher is closed.
func (w *Watcher) Watch(ctx context.Context) (<-chan Event, <-chan error) {
	events := make(chan Event)
	errs := make(chan error, 1)

	go func() {
		defer close(events)
		defer close(errs)

		var timer *time.Timer
		var timerC <-chan time.Time
		pending := false

		drainTimer := func() {
			if timer == nil {
				return
			}
			if !timer.Stop() {
				select {
				case <-timer.C:
				default:
				}
			}
		}

		stopTimer := func() {
			drainTimer()
			timer = nil
			timerC = nil
		}

		resetTimer := func() {
			pending = true
			if timer == nil {
				timer = time.NewTimer(w.debounce)
				timerC = timer.C
				return
			}
			drainTimer()
			timer.Reset(w.debounce)
		}

		for {
			select {
			case <-ctx.Done():
				stopTimer()
				return
			case event, ok := <-w.fsw.Events:
				if !ok {
					stopTimer()
					return
				}
				if w.matches(event) {
					resetTimer()
				}
			case err, ok := <-w.fsw.Errors:
				if !ok {
					stopTimer()
					return
				}
				select {
				case errs <- err:
				default:
				}
			case <-timerC:
				if pending {
					pending = false
					select {
					case events <- Event{Path: w.path}:
					case <-ctx.Done():
						return
					}
				}
				timer = nil
				timerC = nil
			}
		}
	}()

	return events, errs
}

func (w *Watcher) matches(event fsnotify.Event) bool {
	if event.Op&(fsnotify.Write|fsnotify.Create|fsnotify.Remove|fsnotify.Rename) == 0 {
		return false
	}

	name, err := filepath.Abs(event.Name)
	if err != nil {
		name = event.Name
	}
	return filepath.Clean(name) == w.path
}
