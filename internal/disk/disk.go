// Package disk coordinates syncing one Disk: a local folder bound to a git
// remote. It is Seam A: tests drive Open/Sync against file:// bare repos and
// observe only the folder, the remote and the returned Result.
//
// One Sync:
//
//	scan folder -> fetch remote -> reconcile(base, local, remote)
//	-> build tree + commit (parent = remote head) -> push
//	-> apply remote changes to the folder -> record new base tree
//
// The base is a tree hash (not a commit), so it survives history compaction.
package disk

import (
	"context"
	"errors"
	"fmt"
	"math/rand/v2"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/Hanson-Tsai/gcd/internal/apply"
	"github.com/Hanson-Tsai/gcd/internal/config"
	"github.com/Hanson-Tsai/gcd/internal/gitstore"
	"github.com/Hanson-Tsai/gcd/internal/reconcile"
	"github.com/Hanson-Tsai/gcd/internal/scan"
)

// Options configures a Disk.
type Options struct {
	Folder   string // the synced folder; must exist
	Remote   string // any git URL, including file://
	StateDir string // per-Disk local state and git-dir; must be outside Folder
	Host     string // machine name used as commit author; default: os.Hostname
	// Token is the HTTPS password for Remote (a GitHub PAT); empty for
	// file:// remotes. git receives it through its credential prompt only.
	Token string
}

// SyncOptions tunes a single Sync.
type SyncOptions struct {
	// Yes confirms a mass deletion: the sync runs even when it deletes more
	// files than the repo config's mass-delete thresholds allow.
	Yes bool
}

// ChangeKind classifies a change to one path.
type ChangeKind int

const (
	Added ChangeKind = iota + 1
	Modified
	Deleted
)

func (k ChangeKind) String() string {
	switch k {
	case Added:
		return "added"
	case Modified:
		return "modified"
	case Deleted:
		return "deleted"
	}
	return "unknown"
}

// Change is one path changed by a sync.
type Change struct {
	Path string
	Kind ChangeKind
}

// Result is the structured outcome of a Sync.
type Result struct {
	Uploaded   []Change // local changes pushed to the remote
	Downloaded []Change // remote changes written to the folder
	// Conflicts lists paths both sides changed differently. Each also shows
	// up as an added copy in Uploaded and a modified path in Downloaded.
	Conflicts []Conflict
	// Skipped lists local paths left for the next sync because they changed
	// after the scan (e.g. an editor was still writing them).
	Skipped []string
	// Rejected lists paths left alone because of the file rules: names
	// Windows cannot hold, paths differing only in case, files over the size
	// limit, symlinks. Ignored files are not listed. A rejected path is
	// neither uploaded, downloaded nor deleted on either side.
	Rejected []scan.Rejection
	Commit   string // commit pushed by this sync, "" if none
	BaseTree string // tree hash recorded as the new base ("" = nothing synced yet)
	// FormatTooNew: the repo format is newer than this gcd supports; the sync
	// stopped before changing anything (the error is a *config.FormatError).
	FormatTooNew bool
	// NeedsConfirm: the sync would delete PendingDeletes files (on either
	// side), more than the repo config allows without SyncOptions.Yes. It
	// stopped before pushing or touching the folder (error ErrMassDelete).
	NeedsConfirm   bool
	PendingDeletes int
}

// Conflict is a path both sides changed to different content. The remote
// version stays at Path; the local version was renamed to Copy, a conflict
// copy, and uploaded too.
type Conflict struct {
	Path string
	Copy string
}

// Disk is one opened Disk.
type Disk struct {
	folder   string
	stateDir string
	host     string
	remote   string
	token    string
	store    *gitstore.Store

	afterScan  func() // test hooks, see export_test.go
	beforePush func()
}

// Open prepares the Disk's state dir and git-dir (idempotent) and returns it.
// It does not contact the remote.
func Open(opts Options) (*Disk, error) {
	if opts.Folder == "" || opts.Remote == "" || opts.StateDir == "" {
		return nil, errors.New("disk: Folder, Remote and StateDir are required")
	}
	folder, err := filepath.Abs(opts.Folder)
	if err != nil {
		return nil, err
	}
	stateDir, err := filepath.Abs(opts.StateDir)
	if err != nil {
		return nil, err
	}
	if within(folder, stateDir) {
		return nil, fmt.Errorf("disk: state dir %s must be outside the synced folder %s", stateDir, folder)
	}
	if fi, err := os.Stat(folder); err != nil {
		return nil, err
	} else if !fi.IsDir() {
		return nil, fmt.Errorf("disk: %s is not a directory", folder)
	}
	host := opts.Host
	if host == "" {
		if host, err = DefaultHost(); err != nil {
			return nil, err
		}
	}
	store, err := gitstore.Open(context.Background(), GitDir(stateDir), opts.Remote)
	if err != nil {
		return nil, err
	}
	store.SetToken(opts.Token)
	return &Disk{folder: folder, stateDir: stateDir, host: host, remote: opts.Remote, token: opts.Token, store: store}, nil
}

// DefaultHost is the short machine name: os.Hostname up to the first dot.
func DefaultHost() (string, error) {
	h, err := os.Hostname()
	if err != nil {
		return "", err
	}
	h, _, _ = strings.Cut(h, ".")
	return h, nil
}

// Folder returns the absolute synced folder path.
func (d *Disk) Folder() string { return d.folder }

// maxPushAttempts bounds how often one Sync goes back to fetch -> reconcile
// -> push when other devices keep pushing in between (spec sync step 6).
const maxPushAttempts = 5

// ErrRemoteChanging is returned when every push attempt was rejected because
// other devices kept updating the remote. The folder and the base are
// unchanged; running Sync again later is safe.
var ErrRemoteChanging = errors.New("remote kept changing during sync; gave up pushing")

// Sync runs one full sync. See the package doc for the steps. It returns
// ErrAlreadySyncing at once when another Sync of the same Disk is running.
func (d *Disk) Sync(ctx context.Context, opts SyncOptions) (Result, error) {
	unlock, err := d.lock()
	if err != nil {
		return Result{}, err
	}
	defer unlock()

	st, err := loadState(d.stateDir)
	if err != nil {
		return Result{}, err
	}
	if err := apply.RemoveTemp(d.folder); err != nil {
		return Result{}, err
	}
	local, err := d.scanFolder(ctx, d.store.Fetch)
	if err != nil {
		return Result{FormatTooNew: errors.Is(err, config.ErrFormatTooNew)}, err
	}

	var r round
	for n := 1; ; n++ {
		r, err = d.round(ctx, st, local, opts)
		if err == nil {
			break
		}
		if !errors.Is(err, gitstore.ErrPushRejected) {
			return r.res, err
		}
		if n == maxPushAttempts {
			return Result{}, fmt.Errorf("%w after %d attempts: %v", ErrRemoteChanging, n, err)
		}
		if err := backoff(ctx, n); err != nil {
			return Result{}, err
		}
	}
	res := r.res

	out, err := apply.Apply(ctx, d.folder, r.plan, d.store, apply.Options{
		Changed:  func(rel string) (bool, error) { return local.Changed(d.folder, rel) },
		CopyName: r.copyNamer(d.folder),
	})
	if err != nil {
		return res, err
	}
	for _, c := range out.Conflicts {
		res.Conflicts = append(res.Conflicts, Conflict{Path: c.Path, Copy: c.Copy})
	}
	if len(out.Kept) > 0 {
		res.Downloaded = without(res.Downloaded, out.Kept)
		res.Skipped = append(res.Skipped, out.Kept...)
		sort.Strings(res.Skipped)
	}

	st.BaseTree = r.baseTree
	st.LastSync = time.Now().UTC()
	if err := saveState(d.stateDir, st); err != nil {
		return res, err
	}
	res.BaseTree = r.baseTree
	return res, nil
}

// Status reports what Sync would do right now without doing it: the uploads,
// downloads, conflicts (with the copy name Sync would pick today), skipped and
// rejected paths, and whether the sync would stop for a mass delete
// (NeedsConfirm, PendingDeletes; Status itself returns no ErrMassDelete).
// Commit and BaseTree stay empty. A newer repo format fails as in Sync.
//
// Status runs Sync's own planning (scan, fetch, file rules, reconcile, mass
// delete check, re-check of files about to be uploaded), so the two cannot
// disagree about rules. It changes neither the folder, the remote, the base
// nor the remote-tracking ref Sync publishes: it fetches with
// gitstore.Store.Peek, which only adds objects to the git-dir and moves a
// private ref, and it writes no blobs, trees or commits.
//
// Status does not take the sync lock. It writes nothing a Sync reads, so it
// can safely run while `gcd watch` or `gcd sync` holds the lock, and a Sync
// may start while it runs. Its result is a snapshot of that moment; during a
// running sync it may show changes that sync is about to finish.
func (d *Disk) Status(ctx context.Context) (Result, error) {
	st, err := loadState(d.stateDir)
	if err != nil {
		return Result{}, err
	}
	local, err := d.scanFolder(ctx, d.store.Peek)
	if err != nil {
		return Result{FormatTooNew: errors.Is(err, config.ErrFormatTooNew)}, err
	}
	r, err := d.plan(ctx, d.store.Peek, st, local)
	if err != nil {
		return r.res, err
	}
	if r.massDelete != nil {
		r.needsConfirm()
	}
	r.res.summarize(r.plan)
	return r.res, nil
}

// scanFolder reads the scan rules from the remote (fetched with fetch) and scans
// the folder with them.
func (d *Disk) scanFolder(ctx context.Context, fetch fetchFunc) (scan.Result, error) {
	rules, err := d.scanRules(ctx, fetch)
	if err != nil {
		return scan.Result{}, err
	}
	local, err := scan.Scan(d.folder, rules)
	if err != nil {
		return scan.Result{}, fmt.Errorf("scan: %w", err)
	}
	if d.afterScan != nil {
		d.afterScan()
	}
	return local, nil
}

// round is the outcome of one fetch -> reconcile -> commit -> push attempt.
type round struct {
	res  Result
	plan []reconcile.Action
	// baseTree is the new base: the remote head's tree, except that skipped
	// paths keep their old base entry.
	baseTree string
	taken    map[string]bool // lower-cased paths on either side plus conflict copies
	host     string
	now      time.Time

	// Filled by plan for the rest of the round.
	remoteHead, remoteTree string
	remote, base           map[string]gitstore.Entry
	excluded               []string // paths the file rules leave alone (sides.excluded)
	// massDelete is the ErrMassDelete error when plan deletes more files
	// than the repo config allows without confirmation; pendingDeletes is
	// how many.
	massDelete     error
	pendingDeletes int
}

// needsConfirm records in the result that the sync needs SyncOptions.Yes.
func (r *round) needsConfirm() {
	r.res.NeedsConfirm, r.res.PendingDeletes = true, r.pendingDeletes
}

// plan is the read-only part of a round, shared by Sync and Status: it
// fetches the remote with fetch, applies the repo config and the file rules,
// reconciles base, local and remote, names conflict copies, drops uploads
// whose file changed since the scan (res.Skipped) and checks the mass-delete
// limits. It writes nothing outside the git-dir's fetched objects and
// the ref fetch moves.
func (d *Disk) plan(ctx context.Context, fetch fetchFunc, st state, local scan.Result) (round, error) {
	var r round
	res := &r.res
	var err error
	if r.remoteHead, r.remoteTree, r.remote, err = d.fetchTree(ctx, fetch); err != nil {
		return r, err
	}
	cfg, err := d.repoConfig(ctx, r.remote)
	if err != nil {
		res.FormatTooNew = errors.Is(err, config.ErrFormatTooNew)
		return r, err
	}
	if r.base, err = d.store.ReadTree(ctx, st.BaseTree); err != nil {
		return r, fmt.Errorf("read base tree: %w", err)
	}

	sides := applyRules(local, r.base, r.remote)
	res.Rejected, r.excluded = sides.rejected, sides.excluded
	r.plan = reconcile.Reconcile(sides.base, sides.local, sides.remote)
	r.host, r.now = d.host, time.Now()
	r.taken = takenPaths(localHashes(local), treeHashes(r.remote))
	r.nameConflictCopies(r.plan)

	// Re-check every file about to be uploaded: one changed since the scan
	// sits out this round, and so does not count as a deletion below.
	if r.plan, res.Skipped, err = d.dropChanged(r.plan, local); err != nil {
		return r, err
	}
	r.pendingDeletes, r.massDelete = checkMassDelete(r.plan, sides, cfg)
	return r, nil
}

// summarize lists plan's actions as Conflicts, Uploaded and Downloaded.
func (res *Result) summarize(plan []reconcile.Action) {
	for _, a := range plan {
		switch a.Op {
		case reconcile.Conflict:
			res.Conflicts = append(res.Conflicts, Conflict{Path: a.Path, Copy: a.Copy})
			res.Uploaded = append(res.Uploaded, Change{Path: a.Copy, Kind: Added})
			res.Downloaded = append(res.Downloaded, Change{Path: a.Path, Kind: Modified})
		case reconcile.Upload, reconcile.DeleteRemote:
			res.Uploaded = append(res.Uploaded, Change{Path: a.Path, Kind: kindOf(a.Remote, a.Local)})
		case reconcile.Download, reconcile.DeleteLocal:
			res.Downloaded = append(res.Downloaded, Change{Path: a.Path, Kind: kindOf(a.Local, a.Remote)})
		}
	}
}

// round plans (see plan), stops for a mass delete without opts.Yes and, when
// there is something to upload, commits on top of the remote head and pushes.
// A push that lost to another device fails with gitstore.ErrPushRejected; at
// that point nothing outside the git-dir has changed, so the caller can retry.
func (d *Disk) round(ctx context.Context, st state, local scan.Result, opts SyncOptions) (round, error) {
	r, err := d.plan(ctx, d.store.Fetch, st, local)
	if err != nil {
		return r, err
	}
	if r.massDelete != nil && !opts.Yes {
		r.needsConfirm()
		return r, r.massDelete
	}
	res := &r.res

	// A file whose content no longer matches the scanned hash once written
	// as a blob sits out this round too.
	blobs, plan, skipped, err := d.writeBlobs(ctx, r.plan)
	if err != nil {
		return r, err
	}
	res.Skipped = append(res.Skipped, skipped...)
	sort.Strings(res.Skipped)
	res.summarize(plan)

	entries, newTree := r.remote, r.remoteTree
	if len(res.Uploaded) > 0 {
		entries, newTree, res.Commit, err = d.commit(ctx, r.remote, r.remoteHead, plan, blobs, res.Uploaded)
		if err != nil {
			return r, err
		}
		if d.beforePush != nil {
			d.beforePush()
		}
		if err := d.store.Push(ctx, res.Commit); err != nil {
			return r, fmt.Errorf("push: %w", err)
		}
	}
	r.plan, r.baseTree = plan, newTree
	if frozen := append(res.Skipped[:len(res.Skipped):len(res.Skipped)], r.excluded...); len(frozen) > 0 {
		// A skipped or excluded path keeps its old base entry, so the next
		// sync sees the same three-way situation again instead of mistaking
		// the remote version for the base.
		kept := make(map[string]gitstore.Entry, len(entries))
		for p, e := range entries {
			kept[p] = e
		}
		changed := false
		for _, p := range frozen {
			e, inBase := r.base[p]
			if k, ok := kept[p]; ok == inBase && k == e {
				continue
			}
			changed = true
			if inBase {
				kept[p] = e
			} else {
				delete(kept, p)
			}
		}
		if changed {
			if r.baseTree, err = d.store.WriteTree(ctx, kept); err != nil {
				return r, err
			}
		}
	}
	return r, nil
}

// dropChanged removes from plan every upload-side action (Upload,
// DeleteRemote, Conflict) whose local file changed since the scan, and
// returns those paths.
func (d *Disk) dropChanged(plan []reconcile.Action, local scan.Result) ([]reconcile.Action, []string, error) {
	var kept []reconcile.Action
	var skipped []string
	for _, a := range plan {
		switch a.Op {
		case reconcile.Upload, reconcile.DeleteRemote, reconcile.Conflict:
			changed, err := local.Changed(d.folder, a.Path)
			if err != nil {
				return nil, nil, err
			}
			if changed {
				skipped = append(skipped, a.Path)
				continue
			}
		}
		kept = append(kept, a)
	}
	return kept, skipped, nil
}

// writeBlobs stores the local file of every Upload and Conflict as a blob and
// returns tree path -> blob hash (a Conflict's local file goes to its Copy).
// An action whose blob differs from the scanned hash (the file was rewritten
// in place keeping its size and mtime) is dropped from plan, and its path is
// returned as skipped.
func (d *Disk) writeBlobs(ctx context.Context, plan []reconcile.Action) (blobs map[string]string, kept []reconcile.Action, skipped []string, err error) {
	var ups []reconcile.Action
	var abs []string
	for _, a := range plan {
		if a.Op == reconcile.Upload || a.Op == reconcile.Conflict {
			ups = append(ups, a)
			abs = append(abs, filepath.Join(d.folder, filepath.FromSlash(a.Path)))
		}
	}
	hashes, err := d.store.WriteBlobs(ctx, abs)
	if err != nil {
		return nil, nil, nil, err
	}
	blobs = map[string]string{}
	drop := map[string]bool{}
	for i, a := range ups {
		if hashes[i] != a.Local {
			drop[a.Path] = true
			skipped = append(skipped, a.Path)
			continue
		}
		dst := a.Path
		if a.Op == reconcile.Conflict {
			dst = a.Copy
		}
		blobs[dst] = hashes[i]
	}
	for _, a := range plan {
		if !drop[a.Path] {
			kept = append(kept, a)
		}
	}
	return blobs, kept, skipped, nil
}

// backoff waits a short random time before push attempt n+1, so devices that
// keep colliding drift apart.
func backoff(ctx context.Context, n int) error {
	t := time.NewTimer(rand.N(time.Duration(n) * 100 * time.Millisecond))
	defer t.Stop()
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-t.C:
		return nil
	}
}

// takenPaths is the lower-cased set of every path on either side.
func takenPaths(local, remote reconcile.Files) map[string]bool {
	taken := map[string]bool{}
	for _, m := range []reconcile.Files{local, remote} {
		for p := range m {
			taken[strings.ToLower(p)] = true
		}
	}
	return taken
}

// nameConflictCopies sets Copy on every Conflict action to a free conflict
// copy name: not a path on either side nor another copy. Names are compared
// case-insensitively so copies never collide on macOS or Windows.
func (r *round) nameConflictCopies(plan []reconcile.Action) {
	isTaken := func(p string) bool { return r.taken[strings.ToLower(p)] }
	for i := range plan {
		if plan[i].Op != reconcile.Conflict {
			continue
		}
		plan[i].Copy = apply.CopyName(plan[i].Path, r.host, r.now, isTaken)
		r.taken[strings.ToLower(plan[i].Copy)] = true
	}
}

// copyNamer names conflict copies made while applying (a download that finds
// the local file edited after the scan). Besides the rules of
// nameConflictCopies, the name must also be free in the folder right now.
func (r *round) copyNamer(folder string) func(rel string) string {
	isTaken := func(p string) bool {
		if r.taken[strings.ToLower(p)] {
			return true
		}
		_, err := os.Lstat(filepath.Join(folder, filepath.FromSlash(p)))
		return err == nil
	}
	return func(rel string) string {
		c := apply.CopyName(rel, r.host, r.now, isTaken)
		r.taken[strings.ToLower(c)] = true
		return c
	}
}

// without returns changes minus those at the given paths.
func without(changes []Change, paths []string) []Change {
	drop := map[string]bool{}
	for _, p := range paths {
		drop[p] = true
	}
	var kept []Change
	for _, c := range changes {
		if !drop[c.Path] {
			kept = append(kept, c)
		}
	}
	return kept
}

// commit builds the new tree (the remote tree plus the written blobs, minus
// remote deletes) and commits it with the remote head as parent. A Conflict
// uploads the local file at Path under its Copy name; the remote version
// stays at Path.
func (d *Disk) commit(ctx context.Context, remote map[string]gitstore.Entry, parent string,
	plan []reconcile.Action, blobs map[string]string, changes []Change) (entries map[string]gitstore.Entry, tree, commit string, err error) {
	entries = make(map[string]gitstore.Entry, len(remote)+len(blobs))
	for p, e := range remote {
		entries[p] = e
	}
	for p, h := range blobs {
		entries[p] = gitstore.Entry{Mode: gitstore.RegularMode, Hash: h}
	}
	for _, a := range plan {
		if a.Op == reconcile.DeleteRemote {
			delete(entries, a.Path)
		}
	}
	if tree, err = d.store.WriteTree(ctx, entries); err != nil {
		return nil, "", "", err
	}
	sig := gitstore.Signature{Name: d.host, Email: "gcd@" + d.host}
	commit, err = d.store.CommitTree(ctx, tree, parent, sig, CommitMessage(d.host, changes))
	return entries, tree, commit, err
}

// CommitMessage formats "sync from <host>: +added ~modified -deleted".
func CommitMessage(host string, changes []Change) string {
	var n [4]int
	for _, c := range changes {
		n[c.Kind]++
	}
	return fmt.Sprintf("sync from %s: +%d ~%d -%d", host, n[Added], n[Modified], n[Deleted])
}

// kindOf classifies the change from hash `from` to hash `to` ("" = absent).
func kindOf(from, to string) ChangeKind {
	switch {
	case from == "":
		return Added
	case to == "":
		return Deleted
	}
	return Modified
}

// treeHashes and localHashes build the reconcile inputs. Managed paths
// (config.IsManaged) are left out on every side, so they are never written to
// the folder, never uploaded from it and never deleted for being absent; the
// commit keeps them because it starts from the remote tree.
func treeHashes(m map[string]gitstore.Entry) reconcile.Files {
	f := make(reconcile.Files, len(m))
	for p, e := range m {
		if !config.IsManaged(p) {
			f[p] = e.Hash
		}
	}
	return f
}

func localHashes(r scan.Result) reconcile.Files {
	f := make(reconcile.Files, len(r.Files))
	for p, e := range r.Files {
		if !config.IsManaged(p) {
			f[p] = e.Hash
		}
	}
	return f
}

// within reports whether path is dir or below it.
func within(dir, path string) bool {
	rel, err := filepath.Rel(dir, path)
	if err != nil {
		return false
	}
	return rel == "." || (rel != ".." && !strings.HasPrefix(rel, ".."+string(filepath.Separator)))
}
