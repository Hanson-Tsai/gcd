// Package compact rewrites a repo's history so it stops growing (Seam B).
//
// History older than the retention period is squashed into one snapshot
// commit; newer commits are recreated on top of it with their tree, author,
// committer, dates and message unchanged, only their parent replaced. The
// newest tree never changes, which is why Disks (whose base is a tree hash)
// keep syncing across a compaction without conflicts.
//
// The result is pushed with --force-with-lease set to the remote head seen
// when compaction started, so a push made meanwhile by any device makes the
// compaction fail instead of being overwritten.
package compact

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"strconv"
	"time"

	"github.com/Hanson-Tsai/gcd/internal/config"
	"github.com/Hanson-Tsai/gcd/internal/gitstore"
)

// ErrLeaseFailed means the remote branch changed while compacting. Nothing
// was overwritten; run compaction again later.
var ErrLeaseFailed = errors.New("the remote changed during compaction (lease failed); nothing was overwritten, try again later")

// Options selects the repo and the retention period.
type Options struct {
	// GitDir is a bare git-dir to work in; it is created if missing. A
	// Disk's own git-dir may be reused (gcd compact --local) so only new
	// objects are downloaded.
	GitDir string
	// Remote is the repo's git URL (https or file://).
	Remote string
	// Token is the HTTPS password for Remote (a GitHub PAT, or the Actions
	// GITHUB_TOKEN); empty for file:// remotes.
	Token string
	// RetentionDays overrides retention_days from the repo config when > 0.
	RetentionDays int
	// Now is the current time; zero means time.Now().
	Now time.Time

	beforePush func() // test hook, see export_test.go
}

// Result describes a compaction.
type Result struct {
	// Compacted is false when there was no history older than the
	// retention period (or the remote is empty): nothing was pushed.
	Compacted     bool
	RetentionDays int
	Cutoff        time.Time // commits authored before it are squashed
	OldHead       string    // remote head when compaction started
	NewHead       string    // remote head after compaction (OldHead if not compacted)
	NewRoot       string    // the snapshot commit ("" if not compacted)
	Squashed      int       // commits replaced by the snapshot
	Kept          int       // commits after the cut point, recreated on the snapshot
	LeaseFailed   bool      // the remote moved meanwhile; see ErrLeaseFailed
}

// Run compacts the history of the remote's main branch.
//
// The cut point is the last commit before the oldest commit whose author time
// is within the retention period. Commits from there on stay, even if clock
// skew gave a later one an older time; so every commit dated inside the
// retention period is kept. The snapshot gets the cut point's tree, author
// and committer (gcd restore treats the oldest commit's author time as the
// retention boundary). When the cut point is already the root, Run does
// nothing.
//
// A repo config with a newer format, or an invalid one, stops Run before any
// change. Only the first-parent chain is kept; gcd itself never makes merges.
func Run(ctx context.Context, opts Options) (Result, error) {
	now := opts.Now
	if now.IsZero() {
		now = time.Now()
	}
	store, err := gitstore.Open(ctx, opts.GitDir, opts.Remote)
	if err != nil {
		return Result{}, err
	}
	store.SetToken(opts.Token)
	head, err := store.Fetch(ctx)
	if err != nil {
		return Result{}, fmt.Errorf("fetch: %w", err)
	}
	res := Result{OldHead: head, NewHead: head}
	if head == "" {
		return res, nil
	}
	tree, err := store.TreeOf(ctx, head)
	if err != nil {
		return res, err
	}
	days, err := retentionDays(ctx, store, tree, opts.RetentionDays)
	if err != nil {
		return res, err
	}
	res.RetentionDays = days
	res.Cutoff = now.Add(-time.Duration(days) * 24 * time.Hour)

	hashes, err := store.FirstParents(ctx, head)
	if err != nil {
		return res, err
	}
	raws, err := store.ReadCommits(ctx, hashes)
	if err != nil {
		return res, err
	}
	commits := make([]*commit, len(raws))
	for i, raw := range raws {
		if commits[i], err = parseCommit(raw); err != nil {
			return res, fmt.Errorf("commit %s: %w", hashes[i], err)
		}
	}
	keepFrom := len(commits)
	for i, c := range commits {
		if !c.authorTime.Before(res.Cutoff) {
			keepFrom = i
			break
		}
	}
	cut := keepFrom - 1
	if cut <= 0 {
		return res, nil // nothing older than the retention period, or already compacted
	}

	rewritten := make([][]byte, 0, len(commits)-cut)
	rewritten = append(rewritten, commits[cut].snapshot(cut+1))
	parent := gitstore.CommitHash(rewritten[0])
	for _, c := range commits[cut+1:] {
		raw := c.withParent(parent)
		rewritten = append(rewritten, raw)
		parent = gitstore.CommitHash(raw)
	}
	written, err := store.WriteCommits(ctx, rewritten)
	if err != nil {
		return res, err
	}
	if written[len(written)-1] != parent {
		return res, fmt.Errorf("rewritten head is %s, expected %s (repo is not SHA-1?)", written[len(written)-1], parent)
	}
	newTree, err := store.TreeOf(ctx, parent)
	if err != nil {
		return res, err
	}
	if newTree != tree {
		return res, fmt.Errorf("internal error: compacted head has tree %s, want %s", newTree, tree)
	}

	if opts.beforePush != nil {
		opts.beforePush()
	}
	if err := store.ForcePush(ctx, parent, head); err != nil {
		if errors.Is(err, gitstore.ErrPushRejected) {
			res.LeaseFailed = true
			return res, fmt.Errorf("%w (%v)", ErrLeaseFailed, err)
		}
		return res, fmt.Errorf("push: %w", err)
	}
	res.Compacted = true
	res.NewHead = parent
	res.NewRoot = written[0]
	res.Squashed = cut + 1
	res.Kept = len(commits) - keepFrom
	return res, nil
}

// retentionDays returns override when set, else retention_days from the repo
// config in tree (the default when the repo has none). The config is checked
// either way: a newer repo format may give the history a meaning this gcd
// does not know, so compaction refuses to touch it.
func retentionDays(ctx context.Context, store *gitstore.Store, tree string, override int) (int, error) {
	entries, err := store.ReadTree(ctx, tree)
	if err != nil {
		return 0, err
	}
	cfg := config.Default()
	if e, ok := entries[config.Path]; ok {
		r, err := store.Blob(ctx, e.Hash)
		if err != nil {
			return 0, err
		}
		raw, err := io.ReadAll(r)
		r.Close()
		if err != nil {
			return 0, err
		}
		if cfg, err = config.Parse(raw); err != nil {
			return 0, fmt.Errorf("repo config %s: %w", config.Path, err)
		}
	}
	if override > 0 {
		return override, nil
	}
	return cfg.RetentionDays, nil
}

// commit is a parsed raw commit object.
type commit struct {
	headers    []header // in object order
	message    []byte
	authorTime time.Time
}

type header struct {
	key   string
	value []byte // without the key; continuation lines included verbatim
}

func parseCommit(raw []byte) (*commit, error) {
	head, msg, ok := bytes.Cut(raw, []byte("\n\n"))
	if !ok {
		head, msg = bytes.TrimSuffix(raw, []byte("\n")), nil
	}
	c := &commit{message: msg}
	for _, line := range bytes.Split(head, []byte("\n")) {
		if len(line) > 0 && line[0] == ' ' && len(c.headers) > 0 {
			h := &c.headers[len(c.headers)-1]
			h.value = append(append(h.value, '\n'), line...)
			continue
		}
		k, v, _ := bytes.Cut(line, []byte(" "))
		c.headers = append(c.headers, header{key: string(k), value: append([]byte(nil), v...)})
	}
	author := c.get("author")
	if author == nil || c.get("tree") == nil || c.get("committer") == nil {
		return nil, errors.New("missing tree, author or committer")
	}
	t, err := identTime(author)
	if err != nil {
		return nil, err
	}
	c.authorTime = t
	return c, nil
}

func (c *commit) get(key string) []byte {
	for _, h := range c.headers {
		if h.key == key {
			return h.value
		}
	}
	return nil
}

// identTime reads the time of an ident "Name <email> 1700000000 +0800".
func identTime(ident []byte) (time.Time, error) {
	i := bytes.LastIndexByte(ident, '>')
	f := bytes.Fields(ident[i+1:])
	if i < 0 || len(f) != 2 {
		return time.Time{}, fmt.Errorf("bad ident %q", ident)
	}
	secs, err := strconv.ParseInt(string(f[0]), 10, 64)
	if err != nil {
		return time.Time{}, fmt.Errorf("bad ident time %q", ident)
	}
	return time.Unix(secs, 0), nil
}

// withParent returns the commit's raw object with parent as its only parent.
// Everything else is kept verbatim, except signatures, which the new object
// would invalidate.
func (c *commit) withParent(parent string) []byte {
	var b bytes.Buffer
	for _, h := range c.headers {
		switch h.key {
		case "parent", "gpgsig", "gpgsig-sha256", "mergetag":
			continue
		}
		writeHeader(&b, h.key, h.value)
		if h.key == "tree" {
			writeHeader(&b, "parent", []byte(parent))
		}
	}
	b.WriteByte('\n')
	b.Write(c.message)
	return b.Bytes()
}

// snapshot returns the root commit that replaces the first n commits, the
// last of which is c: c's tree, author and committer, no parent. It depends
// only on the squashed history, so compacting the same history twice gives
// the same commits.
func (c *commit) snapshot(n int) []byte {
	var b bytes.Buffer
	for _, key := range []string{"tree", "author", "committer"} {
		writeHeader(&b, key, c.get(key))
	}
	fmt.Fprintf(&b, "\nsnapshot of history up to %s\n\nsquashed %d commits older than the retention period (gcd compact)\n",
		c.authorTime.UTC().Format("2006-01-02 15:04:05 UTC"), n)
	return b.Bytes()
}

func writeHeader(b *bytes.Buffer, key string, value []byte) {
	b.WriteString(key)
	b.WriteByte(' ')
	b.Write(value)
	b.WriteByte('\n')
}
