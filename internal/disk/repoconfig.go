package disk

import (
	"context"
	"errors"
	"fmt"
	"io"
	"path"
	"strings"

	"github.com/Hanson-Tsai/gcd/internal/config"
	"github.com/Hanson-Tsai/gcd/internal/gitstore"
	"github.com/Hanson-Tsai/gcd/internal/scan"
	"github.com/Hanson-Tsai/gcd/internal/workflow"
)

// ErrRemoteChanged is returned when the remote moved between reading the repo
// config and committing a new one; nothing was changed.
var ErrRemoteChanged = errors.New("the remote changed meanwhile; nothing was committed, try again")

// RepoConfig fetches the remote and returns the raw repo config file (nil when
// the repo has none) and the remote head it was read from ("" for an empty
// remote). Pass that head to SetRepoConfig.
func (d *Disk) RepoConfig(ctx context.Context) (raw []byte, head string, err error) {
	head, _, entries, err := d.fetchTree(ctx, d.store.Fetch)
	if err != nil {
		return nil, "", err
	}
	raw, err = d.readBlobAt(ctx, entries, config.Path)
	return raw, head, err
}

// SetRepoConfig checks raw with config.ParseStrict and commits it as the repo
// config on top of head, the remote head RepoConfig returned. Invalid content
// is never committed. When the compaction workflow is installed and
// compact_schedule changed, the same commit re-renders the workflow (GitHub
// reads the schedule from the workflow file); that push needs the Workflows
// permission. If the remote moved since head it returns
// ErrRemoteChanged and changes nothing. It returns the new commit.
func (d *Disk) SetRepoConfig(ctx context.Context, raw []byte, head string) (string, error) {
	cfg, err := config.ParseStrict(raw)
	if err != nil {
		return "", err
	}
	files := map[string][]byte{config.Path: raw}
	wf, err := d.rescheduledWorkflow(ctx, head, cfg.CompactSchedule)
	if err != nil {
		return "", err
	}
	if wf != nil {
		files[workflow.Path] = wf
	}
	return d.commitFiles(ctx, head, files, "config from "+d.host)
}

// EnsureRepoConfig commits the default repo config when the repo has none
// (as `gcd init` does) and reports whether it wrote one. When the repo has a
// config it only checks that its format is not newer than this gcd's.
func (d *Disk) EnsureRepoConfig(ctx context.Context) (created bool, err error) {
	for attempt := 0; ; attempt++ {
		raw, head, err := d.RepoConfig(ctx)
		if err != nil {
			return false, err
		}
		if raw != nil {
			var fe *config.FormatError
			if _, err := config.Parse(raw); errors.As(err, &fe) {
				return false, err
			}
			return false, nil // an otherwise invalid config is reported by Sync
		}
		_, err = d.commitFile(ctx, head, config.Path, config.DefaultTOML(), "add repo config from "+d.host)
		if errors.Is(err, ErrRemoteChanged) && attempt < 2 {
			continue // another machine pushed first: look again
		}
		return err == nil, err
	}
}

// fetchFunc fetches the remote branch and returns its head ("" for an empty
// remote): gitstore.Store.Fetch for Sync and every command that writes,
// gitstore.Store.Peek for Status, which must not move the remote-tracking ref.
type fetchFunc func(context.Context) (string, error)

// fetchTree fetches the remote and returns its head, tree hash and tree
// entries ("" and none for an empty remote).
func (d *Disk) fetchTree(ctx context.Context, fetch fetchFunc) (head, tree string, entries map[string]gitstore.Entry, err error) {
	if head, err = fetch(ctx); err != nil {
		return "", "", nil, fmt.Errorf("fetch: %w", err)
	}
	if head != "" {
		if tree, err = d.store.TreeOf(ctx, head); err != nil {
			return "", "", nil, err
		}
	}
	entries, err = d.store.ReadTree(ctx, tree)
	return head, tree, entries, err
}

// commitFile commits content at path on top of parent (the remote head, ""
// for an empty remote), keeping every other path, and pushes it.
func (d *Disk) commitFile(ctx context.Context, parent, path string, content []byte, msg string) (string, error) {
	return d.commitFiles(ctx, parent, map[string][]byte{path: content}, msg)
}

// commitFiles is commitFile for several paths (path -> content) in one
// commit.
func (d *Disk) commitFiles(ctx context.Context, parent string, files map[string][]byte, msg string) (string, error) {
	entries := map[string]gitstore.Entry{}
	if parent != "" {
		tree, err := d.store.TreeOf(ctx, parent)
		if err != nil {
			return "", err
		}
		if entries, err = d.store.ReadTree(ctx, tree); err != nil {
			return "", err
		}
	}
	for path, content := range files {
		blob, err := d.store.WriteBlob(ctx, content)
		if err != nil {
			return "", err
		}
		entries[path] = gitstore.Entry{Mode: gitstore.RegularMode, Hash: blob}
	}
	tree, err := d.store.WriteTree(ctx, entries)
	if err != nil {
		return "", err
	}
	sig := gitstore.Signature{Name: d.host, Email: "gcd@" + d.host}
	commit, err := d.store.CommitTree(ctx, tree, parent, sig, msg)
	if err != nil {
		return "", err
	}
	if err := d.store.Push(ctx, commit); err != nil {
		if errors.Is(err, gitstore.ErrPushRejected) {
			return "", ErrRemoteChanged
		}
		return "", fmt.Errorf("push: %w", err)
	}
	return commit, nil
}

// scanRules fetches the remote and returns the rules for scanning the folder:
// the repo config's ignore rules and size limit, plus the remote's .gcdignore
// files (used where the folder has none yet, so a machine joining with
// existing files does not upload what the shared rules ignore). Sync calls it
// before scanning; each sync round then checks the format again after its
// own fetch.
func (d *Disk) scanRules(ctx context.Context, fetch fetchFunc) (scan.Rules, error) {
	_, _, tree, err := d.fetchTree(ctx, fetch)
	if err != nil {
		return scan.Rules{}, err
	}
	cfg, err := d.repoConfig(ctx, tree)
	if err != nil {
		return scan.Rules{}, err
	}
	rules := scan.Rules{Ignore: cfg.Ignore, MaxFileSize: cfg.MaxFileSize, IgnoreFiles: map[string][]byte{}}
	for p := range tree {
		dir, name := path.Split(p)
		if name != scan.IgnoreFile || config.IsManaged(p) {
			continue
		}
		if rules.IgnoreFiles[strings.TrimSuffix(dir, "/")], err = d.readBlobAt(ctx, tree, p); err != nil {
			return scan.Rules{}, err
		}
	}
	return rules, nil
}

// repoConfig parses the repo config found in tree (path -> entry), or returns
// the defaults when the repo has none. A newer repo format comes back as a
// *config.FormatError (errors.Is config.ErrFormatTooNew).
func (d *Disk) repoConfig(ctx context.Context, tree map[string]gitstore.Entry) (config.Config, error) {
	raw, err := d.readBlobAt(ctx, tree, config.Path)
	if err != nil || raw == nil {
		return config.Default(), err
	}
	cfg, err := config.Parse(raw)
	var fe *config.FormatError
	if err != nil && !errors.As(err, &fe) {
		err = fmt.Errorf("repo config %s is invalid (fix it with `gcd config edit`): %w", config.Path, err)
	}
	return cfg, err
}

// readBlobAt returns the content of path in tree, or nil when it is absent.
func (d *Disk) readBlobAt(ctx context.Context, tree map[string]gitstore.Entry, path string) ([]byte, error) {
	e, ok := tree[path]
	if !ok {
		return nil, nil
	}
	r, err := d.store.Blob(ctx, e.Hash)
	if err != nil {
		return nil, err
	}
	defer r.Close()
	return io.ReadAll(r)
}
