// Package apply writes remote changes into the synced folder. Files are
// written to a temp file in the target directory and renamed into place, so an
// interrupted sync never leaves a half-written file. (os.Rename replaces an
// existing target on Windows too.) Temp files an interrupted sync left behind
// are removed by RemoveTemp at the start of the next one.
//
// A Conflict keeps both versions: the local file is renamed to its conflict
// copy (Action.Copy) and the remote version is written at the original path.
//
// Right before replacing or deleting a local file, Apply asks Options.Changed
// whether the user edited it after the scan. If so the edit is never lost: a
// download moves the local file aside as a conflict copy first, and a delete
// leaves the file in place for the next sync.
package apply

import (
	"context"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"time"

	"github.com/Hanson-Tsai/gcd/internal/reconcile"
	"github.com/Hanson-Tsai/gcd/internal/scan"
)

// BlobSource provides file content by hash (implemented by gitstore.Store).
type BlobSource interface {
	Blob(ctx context.Context, hash string) (io.ReadCloser, error)
}

// Options supplies Apply's checks against edits made after the scan.
type Options struct {
	// Changed reports whether the local file at rel changed since the scan.
	// Nil means never.
	Changed func(rel string) (bool, error)
	// CopyName returns a free conflict-copy path for rel. Required when
	// Changed may report true.
	CopyName func(rel string) string
}

// Outcome reports where Apply deviated from the plan to protect local edits.
type Outcome struct {
	// Conflicts are downloads that found the local file edited after the
	// scan: the local file was moved to Copy, the remote version is at Path.
	Conflicts []Conflict
	// Kept lists paths a DeleteLocal left in place because the local file
	// was edited after the scan.
	Kept []string
}

// Conflict is one local file moved aside to a conflict copy.
type Conflict struct {
	Path string
	Copy string
}

// Apply performs the folder-side actions (Download, DeleteLocal, Conflict) of
// plan under root. Other ops are ignored.
func Apply(ctx context.Context, root string, plan []reconcile.Action, src BlobSource, opts Options) (Outcome, error) {
	var out Outcome
	changed := func(rel string) (bool, error) {
		if opts.Changed == nil {
			return false, nil
		}
		return opts.Changed(rel)
	}
	for _, a := range plan {
		var err error
		switch a.Op {
		case reconcile.Download:
			err = download(ctx, root, a.Path, a.Hash, src, func() error {
				ch, err := changed(a.Path)
				if err != nil || !ch {
					return err
				}
				cp := opts.CopyName(a.Path)
				moved, err := keepAsCopy(root, a.Path, cp)
				if moved {
					out.Conflicts = append(out.Conflicts, Conflict{Path: a.Path, Copy: cp})
				}
				return err
			})
		case reconcile.DeleteLocal:
			var ch bool
			if ch, err = changed(a.Path); err == nil {
				if ch && exists(filepath.Join(root, filepath.FromSlash(a.Path))) {
					out.Kept = append(out.Kept, a.Path)
				} else {
					err = remove(root, a.Path)
				}
			}
		case reconcile.Conflict:
			if _, err = keepAsCopy(root, a.Path, a.Copy); err == nil {
				err = download(ctx, root, a.Path, a.Hash, src, nil)
			}
		}
		if err != nil {
			return out, fmt.Errorf("%s %s: %w", a.Op, a.Path, err)
		}
	}
	return out, nil
}

// download writes blob hash to rel. beforeReplace, if set, runs once the
// content is fully in the temp file, right before it is renamed over rel.
func download(ctx context.Context, root, rel, hash string, src BlobSource, beforeReplace func() error) error {
	r, err := src.Blob(ctx, hash)
	if err != nil {
		return err
	}
	defer r.Close()
	return writeFile(filepath.Join(root, filepath.FromSlash(rel)), r, beforeReplace)
}

// WriteFile atomically replaces path with the content of r, creating parent
// directories as needed.
func WriteFile(path string, r io.Reader) error {
	return writeFile(path, r, nil)
}

func writeFile(path string, r io.Reader, beforeReplace func() error) error {
	dir := filepath.Dir(path)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return err
	}
	tmp, err := os.CreateTemp(dir, scan.TempPrefix+"*")
	if err != nil {
		return err
	}
	tmpName := tmp.Name()
	ok := false
	defer func() {
		if !ok {
			tmp.Close()
			os.Remove(tmpName)
		}
	}()
	if _, err := io.Copy(tmp, r); err != nil {
		return err
	}
	if err := tmp.Sync(); err != nil {
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	if err := os.Chmod(tmpName, 0o644); err != nil {
		return err
	}
	if beforeReplace != nil {
		if err := beforeReplace(); err != nil {
			return err
		}
	}
	if err := rename(tmpName, path); err != nil {
		return err
	}
	ok = true
	return nil
}

// rename is os.Rename, which replaces an existing target on every platform
// (MoveFileEx with MOVEFILE_REPLACE_EXISTING on Windows). On Windows it
// retries for a short while, because another process (an editor, indexer or
// virus scanner) holding the target open makes the rename fail briefly.
func rename(from, to string) error {
	err := os.Rename(from, to)
	for i := 0; err != nil && runtime.GOOS == "windows" && i < 5; i++ {
		time.Sleep(100 * time.Millisecond)
		err = os.Rename(from, to)
	}
	return err
}

// RemoveTemp deletes every temp file (scan.TempPrefix*) below root: leftovers of
// an interrupted sync. Call it only while holding the Disk's sync lock, so
// that no temp file still being written is removed.
func RemoveTemp(root string) error {
	return filepath.WalkDir(root, func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.Type().IsRegular() && strings.HasPrefix(d.Name(), scan.TempPrefix) {
			if err := os.Remove(p); err != nil && !errors.Is(err, fs.ErrNotExist) {
				return err
			}
		}
		return nil
	})
}

func exists(path string) bool {
	_, err := os.Lstat(path)
	return err == nil
}

// remove deletes a file and then any directories it leaves empty, up to root.
func remove(root, rel string) error {
	p := filepath.Join(root, filepath.FromSlash(rel))
	if err := os.Remove(p); err != nil && !os.IsNotExist(err) {
		return err
	}
	cleanRoot := filepath.Clean(root)
	for dir := filepath.Dir(p); dir != cleanRoot && len(dir) > len(cleanRoot); dir = filepath.Dir(dir) {
		if os.Remove(dir) != nil {
			break // not empty (or gone): stop climbing
		}
	}
	return nil
}
