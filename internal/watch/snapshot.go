package watch

import (
	"errors"
	"io/fs"
	"maps"
	"path/filepath"
	"strings"

	"github.com/Hanson-Tsai/gcd/internal/disk"
	"github.com/Hanson-Tsai/gcd/internal/scan"
)

// stamp is what a cheap stat says about one file.
type stamp struct {
	size  int64
	mtime int64 // UnixNano
}

// unsynced marks a path the last sync left out; it equals no real stamp, so
// the folder keeps counting as changed until a sync takes the path in.
var unsynced = stamp{size: -1}

// snapshot is a stat-only view of a folder: slash-separated relative path ->
// stamp of every regular file, like scan.Scan but without hashing.
type snapshot map[string]stamp

// takeSnapshot walks folder. Comparing two snapshots tells whether anything
// changed in between, which lets watch skip syncs that would find nothing to
// upload and catch changes the file watcher missed.
func takeSnapshot(folder string) (snapshot, error) {
	s := snapshot{}
	err := filepath.WalkDir(folder, func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			if errors.Is(err, fs.ErrNotExist) && p != folder {
				return nil // removed while walking
			}
			return err
		}
		if !d.Type().IsRegular() || strings.HasPrefix(d.Name(), scan.TempPrefix) {
			return nil
		}
		info, err := d.Info()
		if errors.Is(err, fs.ErrNotExist) {
			return nil
		} else if err != nil {
			return err
		}
		rel, err := filepath.Rel(folder, p)
		if err != nil {
			return err
		}
		s[filepath.ToSlash(rel)] = stamp{size: info.Size(), mtime: info.ModTime().UnixNano()}
		return nil
	})
	return s, err
}

// settle returns the folder state a successful sync accounts for, given
// snapshots taken right before and right after it. Files the sync wrote
// (downloads, local deletes, conflict copies it uploaded) take their state
// from after; every other file keeps its state from before, so an edit made
// while the sync ran still shows up as a change. Paths the sync skipped are
// marked unsynced.
func settle(before, after snapshot, res disk.Result) snapshot {
	s := maps.Clone(before)
	written := map[string]bool{}
	for _, c := range res.Downloaded {
		written[c.Path] = true
	}
	uploaded := map[string]bool{}
	for _, c := range res.Uploaded {
		uploaded[c.Path] = true
	}
	for _, c := range res.Conflicts {
		// A copy made while applying downloads is not uploaded yet; it stays
		// a local change for the next sync.
		if uploaded[c.Copy] {
			written[c.Copy] = true
		}
	}
	for p := range written {
		if st, ok := after[p]; ok {
			s[p] = st
		} else {
			delete(s, p)
		}
	}
	for _, p := range res.Skipped {
		s[p] = unsynced
	}
	return s
}
