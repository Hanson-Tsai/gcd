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
	"strings"
	"time"

	"github.com/Hanson-Tsai/gcd/internal/apply"
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
}

// SyncOptions tunes a single Sync. Later tickets add fields (e.g. Yes for
// confirming mass deletions, #7).
type SyncOptions struct{}

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
	Commit     string   // commit pushed by this sync, "" if none
	BaseTree   string   // tree hash recorded as the new base ("" = nothing synced yet)
}

// ErrConflict is returned when both sides changed a path differently.
// Conflict copies are ticket #4; until then Sync refuses before changing
// anything.
var ErrConflict = errors.New("both sides changed the same file; conflict handling not implemented yet")

// Disk is one opened Disk.
type Disk struct {
	folder   string
	stateDir string
	host     string
	store    *gitstore.Store

	beforePush func() // test hook, see export_test.go
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
	store, err := gitstore.Open(context.Background(), filepath.Join(stateDir, "git"), opts.Remote)
	if err != nil {
		return nil, err
	}
	return &Disk{folder: folder, stateDir: stateDir, host: host, store: store}, nil
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
func (d *Disk) Sync(ctx context.Context, _ SyncOptions) (Result, error) {
	unlock, err := d.lock()
	if err != nil {
		return Result{}, err
	}
	defer unlock()

	st, err := loadState(d.stateDir)
	if err != nil {
		return Result{}, err
	}
	local, err := scan.Scan(d.folder)
	if err != nil {
		return Result{}, fmt.Errorf("scan: %w", err)
	}

	var r round
	for n := 1; ; n++ {
		r, err = d.round(ctx, st, local)
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

	if err := apply.Apply(ctx, d.folder, r.plan, d.store); err != nil {
		return res, err
	}

	st.BaseTree = r.newTree
	st.LastSync = time.Now().UTC()
	if err := saveState(d.stateDir, st); err != nil {
		return res, err
	}
	res.BaseTree = r.newTree
	return res, nil
}

// round is the outcome of one fetch -> reconcile -> commit -> push attempt.
type round struct {
	res     Result
	plan    []reconcile.Action
	newTree string // the tree now at the remote head
}

// round fetches the remote, reconciles it with base and local and, when there
// is something to upload, commits on top of the remote head and pushes. A push
// that lost to another device fails with gitstore.ErrPushRejected; at that
// point nothing outside the git-dir has changed, so the caller can retry.
func (d *Disk) round(ctx context.Context, st state, local scan.Result) (round, error) {
	var r round
	res := &r.res
	remoteHead, err := d.store.Fetch(ctx)
	if err != nil {
		return r, fmt.Errorf("fetch: %w", err)
	}
	remoteTree := ""
	if remoteHead != "" {
		if remoteTree, err = d.store.TreeOf(ctx, remoteHead); err != nil {
			return r, err
		}
	}
	remote, err := d.store.ReadTree(ctx, remoteTree)
	if err != nil {
		return r, err
	}
	base, err := d.store.ReadTree(ctx, st.BaseTree)
	if err != nil {
		return r, fmt.Errorf("read base tree: %w", err)
	}

	plan := reconcile.Reconcile(treeHashes(base), localHashes(local), treeHashes(remote))
	var uploadPaths []string
	for _, a := range plan {
		switch a.Op {
		case reconcile.Conflict:
			return r, fmt.Errorf("%w: %s", ErrConflict, a.Path)
		case reconcile.Upload, reconcile.DeleteRemote:
			res.Uploaded = append(res.Uploaded, Change{Path: a.Path, Kind: kindOf(a.Remote, a.Local)})
			if a.Op == reconcile.Upload {
				uploadPaths = append(uploadPaths, a.Path)
			}
		case reconcile.Download, reconcile.DeleteLocal:
			res.Downloaded = append(res.Downloaded, Change{Path: a.Path, Kind: kindOf(a.Local, a.Remote)})
		}
	}

	newTree := remoteTree
	if len(res.Uploaded) > 0 {
		newTree, res.Commit, err = d.commit(ctx, remote, remoteHead, plan, uploadPaths, res.Uploaded)
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
	r.plan, r.newTree = plan, newTree
	return r, nil
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

// commit writes the uploaded blobs, builds the new tree on top of the remote
// tree and commits it with the remote head as parent.
func (d *Disk) commit(ctx context.Context, remote map[string]gitstore.Entry, parent string,
	plan []reconcile.Action, uploadPaths []string, changes []Change) (tree, commit string, err error) {
	abs := make([]string, len(uploadPaths))
	for i, p := range uploadPaths {
		abs[i] = filepath.Join(d.folder, filepath.FromSlash(p))
	}
	hashes, err := d.store.WriteBlobs(ctx, abs)
	if err != nil {
		return "", "", err
	}
	entries := make(map[string]gitstore.Entry, len(remote)+len(uploadPaths))
	for p, e := range remote {
		entries[p] = e
	}
	for i, p := range uploadPaths {
		entries[p] = gitstore.Entry{Mode: gitstore.RegularMode, Hash: hashes[i]}
	}
	for _, a := range plan {
		if a.Op == reconcile.DeleteRemote {
			delete(entries, a.Path)
		}
	}
	if tree, err = d.store.WriteTree(ctx, entries); err != nil {
		return "", "", err
	}
	sig := gitstore.Signature{Name: d.host, Email: "gcd@" + d.host}
	commit, err = d.store.CommitTree(ctx, tree, parent, sig, CommitMessage(d.host, changes))
	return tree, commit, err
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

func treeHashes(m map[string]gitstore.Entry) reconcile.Files {
	f := make(reconcile.Files, len(m))
	for p, e := range m {
		f[p] = e.Hash
	}
	return f
}

func localHashes(r scan.Result) reconcile.Files {
	f := make(reconcile.Files, len(r.Files))
	for p, e := range r.Files {
		f[p] = e.Hash
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
