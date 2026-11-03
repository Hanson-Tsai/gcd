package watch

import (
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strings"

	"github.com/fsnotify/fsnotify"

	"github.com/Hanson-Tsai/gcd/internal/scan"
)

// folderWatcher watches every directory below a folder. fsnotify is not
// recursive, so it adds each directory, and new ones as they appear.
//
// It is only a hint that something changed: the worker compares stat
// snapshots before syncing and rescans every poll interval, so a missed
// event (watch limits, buffer overflow) costs latency, never correctness.
type folderWatcher struct {
	w *fsnotify.Watcher // nil when file watching is unavailable
}

// newFolderWatcher starts watching folder. A non-nil error means watching
// is unavailable or partial; the returned watcher is still usable.
func newFolderWatcher(folder string) (*folderWatcher, error) {
	w, err := fsnotify.NewWatcher()
	if err != nil {
		return &folderWatcher{}, fmt.Errorf("file watching unavailable: %w", err)
	}
	fw := &folderWatcher{w: w}
	return fw, fw.addTree(folder)
}

func (fw *folderWatcher) events() <-chan fsnotify.Event {
	if fw.w == nil {
		return nil
	}
	return fw.w.Events
}

func (fw *folderWatcher) errors() <-chan error {
	if fw.w == nil {
		return nil
	}
	return fw.w.Errors
}

func (fw *folderWatcher) close() {
	if fw.w != nil {
		_ = fw.w.Close()
	}
}

// addTree watches dir and every directory below it. It keeps going past
// failures (e.g. the OS watch limit) and returns the first one.
func (fw *folderWatcher) addTree(dir string) error {
	var first error
	_ = filepath.WalkDir(dir, func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			if !errors.Is(err, fs.ErrNotExist) && first == nil {
				first = err
			}
			return nil
		}
		if !d.IsDir() {
			return nil
		}
		if err := fw.w.Add(p); err != nil && !errors.Is(err, fs.ErrNotExist) && first == nil {
			first = fmt.Errorf("file watching limited, changes may be noticed only every poll interval: %w", err)
		}
		return nil
	})
	return first
}

// handle processes one event and reports whether it is a change worth a
// sync. A new directory gets watched too.
func (fw *folderWatcher) handle(ev fsnotify.Event) (relevant bool, err error) {
	if ev.Op == fsnotify.Chmod || strings.HasPrefix(filepath.Base(ev.Name), scan.TempPrefix) {
		return false, nil // permission noise; gcd's own temp files
	}
	if ev.Has(fsnotify.Create) {
		if fi, statErr := os.Lstat(ev.Name); statErr == nil && fi.IsDir() {
			err = fw.addTree(ev.Name)
		}
	}
	return true, err
}
