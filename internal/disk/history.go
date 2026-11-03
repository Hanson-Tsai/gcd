package disk

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path"
	"path/filepath"
	"regexp"
	"strings"
	"time"

	"github.com/Hanson-Tsai/gcd/internal/apply"
	"github.com/Hanson-Tsai/gcd/internal/config"
	"github.com/Hanson-Tsai/gcd/internal/gitstore"
	"github.com/Hanson-Tsai/gcd/internal/scan"
)

// LogEntry is one sync commit in the Disk's history.
type LogEntry struct {
	Commit  string
	Time    time.Time
	Host    string // machine that made the commit
	Subject string
	Changes []Change // files the commit changed (only those under the Log path, if any)
}

// Log fetches the remote and returns its history, newest first. A non-empty
// path (slash-separated, relative to the folder; a file or a directory) keeps
// only the commits that touched it. History older than the retention period
// has been compacted away and is not listed. Managed paths (the repo config
// and the workflow) never show: they are left out of Changes, and a commit
// that changed nothing else is not listed.
func (d *Disk) Log(ctx context.Context, path string) ([]LogEntry, error) {
	commits, err := d.history(ctx, path)
	if err != nil {
		return nil, err
	}
	var log []LogEntry
	for _, c := range commits {
		e := LogEntry{Commit: c.Hash, Time: c.Time, Host: c.Author, Subject: c.Subject}
		for _, fc := range c.Changes {
			if !config.IsManaged(fc.Path) {
				e.Changes = append(e.Changes, Change{Path: fc.Path, Kind: changeKind(fc.Status)})
			}
		}
		if len(e.Changes) == 0 && len(c.Changes) > 0 {
			continue
		}
		log = append(log, e)
	}
	return log, nil
}

// history fetches the remote and lists its history (see gitstore.History).
func (d *Disk) history(ctx context.Context, path string) ([]gitstore.CommitInfo, error) {
	head, err := d.store.Fetch(ctx)
	if err != nil {
		return nil, fmt.Errorf("fetch: %w", err)
	}
	return d.store.History(ctx, head, path)
}

// Errors returned by Restore. They are wrapped with details; test with
// errors.Is.
var (
	// ErrBeforeRetention: the requested time is older than the oldest
	// commit still in history (older history has been compacted away).
	ErrBeforeRetention = errors.New("earlier than the oldest version kept")
	// ErrUnknownCommit: the requested commit is not in the Disk's history.
	ErrUnknownCommit = errors.New("commit not found in the disk's history")
	// ErrPathNotFound: the path does not exist at the requested point.
	ErrPathNotFound = errors.New("path not found in history")
	// ErrExists: a different file already exists at the path in the folder.
	ErrExists = errors.New("file already exists in the folder")
	// ErrNameNotAllowed: the path breaks the file-name policy (see package
	// scan), so it could not sync from the folder.
	ErrNameNotAllowed = errors.New("name not allowed")
)

// At selects a point in history. At most one field is set; the zero At means
// "the newest version that has the path" (for a deleted file: the version
// just before it was deleted).
type At struct {
	Time   time.Time // the newest commit at or before Time
	Commit string    // a full or abbreviated commit hash
}

// timeLayouts are the accepted --at time formats. Layouts without a zone are
// read in the caller's location; a bare date means its first second.
var timeLayouts = []string{
	time.RFC3339,
	"2006-01-02T15:04:05",
	"2006-01-02T15:04",
	"2006-01-02 15:04:05",
	"2006-01-02 15:04",
	"2006-01-02",
}

var commitRE = regexp.MustCompile(`^[0-9a-fA-F]{4,64}$`)

// ParseAt reads a user-given point in history: a time in one of the
// timeLayouts (zone-less times in loc) or a full or abbreviated (>= 4 hex
// digits) commit hash.
func ParseAt(s string, loc *time.Location) (At, error) {
	s = strings.TrimSpace(s)
	for _, l := range timeLayouts {
		if t, err := time.ParseInLocation(l, s, loc); err == nil {
			return At{Time: t}, nil
		}
	}
	if commitRE.MatchString(s) {
		return At{Commit: strings.ToLower(s)}, nil
	}
	return At{}, fmt.Errorf("invalid --at %q: want a commit hash or a time like 2006-01-02, \"2006-01-02 15:04\" or 2006-01-02T15:04:05Z", s)
}

// RestoreOptions tunes Restore.
type RestoreOptions struct {
	At    At
	Force bool // overwrite a different local file at the path
}

// RestoreResult reports what Restore wrote.
type RestoreResult struct {
	Path      string
	Commit    string    // commit the version was taken from
	Time      time.Time // that commit's time
	Unchanged bool      // the folder already had this exact version
}

// Restore writes the version of one file at opts.At into the folder. It
// changes only the folder: the next Sync uploads the file like any local
// edit. path is slash-separated and relative to the folder. A different local
// file at path is not overwritten unless opts.Force is set.
//
// Like Sync, it takes the sync lock (ErrAlreadySyncing when a Sync runs),
// refuses a repo whose format is newer than this gcd supports
// (config.ErrFormatTooNew), and refuses a path the file-name policy rejects
// (ErrNameNotAllowed).
func (d *Disk) Restore(ctx context.Context, path string, opts RestoreOptions) (RestoreResult, error) {
	var res RestoreResult
	rel, err := cleanRel(path)
	if err != nil {
		return res, err
	}
	if config.IsManaged(rel) {
		return res, fmt.Errorf("%s is managed by gcd and never placed in the folder", rel)
	}
	if rej := scan.CheckPath(rel); rej != nil {
		return res, fmt.Errorf("%w: cannot restore %s: %s", ErrNameNotAllowed, rel, rej.Detail)
	}
	unlock, err := d.lock()
	if err != nil {
		return res, err
	}
	defer unlock()
	c, err := d.pickCommit(ctx, rel, opts.At)
	if err != nil {
		return res, err
	}
	// pickCommit fetched: check the repo format at the fetched head.
	if _, err := d.KnownRepoConfig(ctx); err != nil {
		return res, err
	}
	entries, err := d.store.ReadTree(ctx, c.Tree)
	if err != nil {
		return res, err
	}
	e, ok := entries[rel]
	if !ok {
		if isDir(entries, rel) {
			return res, fmt.Errorf("%s is a folder at %s; restore its files one at a time", rel, short(c.Hash))
		}
		return res, fmt.Errorf("%w: %s did not exist at %s (%s)", ErrPathNotFound, rel, short(c.Hash), c.Time.Format(time.RFC3339))
	}
	res = RestoreResult{Path: rel, Commit: c.Hash, Time: c.Time}

	target := filepath.Join(d.folder, filepath.FromSlash(rel))
	if fi, err := os.Lstat(target); err == nil {
		if !fi.Mode().IsRegular() {
			return res, fmt.Errorf("%s exists in the folder and is not a regular file", rel)
		}
		if h, err := scan.HashFile(target); err == nil && h == e.Hash {
			res.Unchanged = true
			return res, nil
		}
		if !opts.Force {
			return res, fmt.Errorf("%w: %s differs from the version at %s; use --force to overwrite it", ErrExists, rel, short(c.Hash))
		}
	} else if !errors.Is(err, os.ErrNotExist) {
		return res, err
	}

	r, err := d.store.Blob(ctx, e.Hash)
	if err != nil {
		return res, err
	}
	defer r.Close()
	return res, apply.WriteFile(target, r)
}

// pickCommit fetches the remote and selects the commit at, as described on
// At, for restoring rel.
func (d *Disk) pickCommit(ctx context.Context, rel string, at At) (gitstore.CommitInfo, error) {
	none := gitstore.CommitInfo{}
	if at.Commit == "" && at.Time.IsZero() {
		commits, err := d.history(ctx, rel)
		if err != nil {
			return none, err
		}
		for _, c := range commits {
			entries, err := d.store.ReadTree(ctx, c.Tree)
			if err != nil {
				return none, err
			}
			if _, ok := entries[rel]; ok {
				return c, nil
			}
		}
		return none, fmt.Errorf("%w: %s", ErrPathNotFound, rel)
	}

	commits, err := d.history(ctx, "")
	if err != nil {
		return none, err
	}
	if len(commits) == 0 {
		return none, fmt.Errorf("%w: the disk has no history yet", ErrPathNotFound)
	}
	if at.Commit != "" {
		full, err := d.store.ResolveCommit(ctx, at.Commit)
		if err != nil {
			return none, err
		}
		for _, c := range commits {
			if full != "" && c.Hash == full {
				return c, nil
			}
		}
		return none, fmt.Errorf("%w: %s", ErrUnknownCommit, at.Commit)
	}
	oldest := commits[len(commits)-1]
	if at.Time.Before(oldest.Time) {
		return none, fmt.Errorf("%w: %s is before %s", ErrBeforeRetention,
			at.Time.Format(time.RFC3339), oldest.Time.Format(time.RFC3339))
	}
	for _, c := range commits {
		if !c.Time.After(at.Time) {
			return c, nil
		}
	}
	return oldest, nil // clock skew between machines: fall back to the oldest
}

// cleanRel validates a slash-separated path relative to the folder.
func cleanRel(p string) (string, error) {
	c := path.Clean(p)
	if p == "" || c == "." || path.IsAbs(c) || c == ".." || strings.HasPrefix(c, "../") || filepath.IsAbs(p) {
		return "", fmt.Errorf("invalid path %q: want a file path inside the disk folder", p)
	}
	return c, nil
}

func isDir(entries map[string]gitstore.Entry, dir string) bool {
	for p := range entries {
		if strings.HasPrefix(p, dir+"/") {
			return true
		}
	}
	return false
}

func short(hash string) string {
	if len(hash) > 7 {
		return hash[:7]
	}
	return hash
}

func changeKind(status byte) ChangeKind {
	switch status {
	case 'A':
		return Added
	case 'D':
		return Deleted
	}
	return Modified
}
