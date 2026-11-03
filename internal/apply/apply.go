// Package apply writes remote changes into the synced folder. Files are
// written to a temp file in the target directory and renamed into place, so an
// interrupted sync never leaves a half-written file. (os.Rename replaces an
// existing target on Windows too.)
//
// A Conflict keeps both versions: the local file is renamed to its conflict
// copy (Action.Copy) and the remote version is written at the original path.
//
// Re-checking the target right before replacement and cleaning up stale temp
// files is ticket #8.
package apply

import (
	"context"
	"fmt"
	"io"
	"os"
	"path/filepath"

	"github.com/Hanson-Tsai/gcd/internal/reconcile"
)

// TempPrefix starts the name of every temp file apply creates.
const TempPrefix = ".gcd-tmp-"

// BlobSource provides file content by hash (implemented by gitstore.Store).
type BlobSource interface {
	Blob(ctx context.Context, hash string) (io.ReadCloser, error)
}

// Apply performs the folder-side actions (Download, DeleteLocal, Conflict) of
// plan under root. Other ops are ignored.
func Apply(ctx context.Context, root string, plan []reconcile.Action, src BlobSource) error {
	for _, a := range plan {
		var err error
		switch a.Op {
		case reconcile.Download:
			err = download(ctx, root, a.Path, a.Hash, src)
		case reconcile.DeleteLocal:
			err = remove(root, a.Path)
		case reconcile.Conflict:
			if err = keepAsCopy(root, a.Path, a.Copy); err == nil {
				err = download(ctx, root, a.Path, a.Hash, src)
			}
		}
		if err != nil {
			return fmt.Errorf("%s %s: %w", a.Op, a.Path, err)
		}
	}
	return nil
}

func download(ctx context.Context, root, rel, hash string, src BlobSource) error {
	r, err := src.Blob(ctx, hash)
	if err != nil {
		return err
	}
	defer r.Close()
	return WriteFile(filepath.Join(root, filepath.FromSlash(rel)), r)
}

// WriteFile atomically replaces path with the content of r, creating parent
// directories as needed.
func WriteFile(path string, r io.Reader) error {
	dir := filepath.Dir(path)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return err
	}
	tmp, err := os.CreateTemp(dir, TempPrefix+"*")
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
	if err := os.Rename(tmpName, path); err != nil {
		return err
	}
	ok = true
	return nil
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
