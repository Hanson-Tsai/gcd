package disk

import (
	"context"

	"github.com/Hanson-Tsai/gcd/internal/config"
)

// RemoteChanged reports whether the remote moved since this Disk last
// fetched or pushed it. It costs one ls-remote and changes nothing, so
// `gcd watch` calls it every poll interval and syncs only when it is true.
func (d *Disk) RemoteChanged(ctx context.Context) (bool, error) {
	head, err := d.store.RemoteHead(ctx)
	if err != nil {
		return false, err
	}
	known, err := d.store.KnownHead(ctx)
	if err != nil {
		return false, err
	}
	return head != known, nil
}

// KnownRepoConfig returns the repo config as of the last fetch, without
// contacting the remote: the defaults when nothing was fetched yet or the
// repo has no config. Errors are those of Sync's config check (a newer
// format, an invalid file).
func (d *Disk) KnownRepoConfig(ctx context.Context) (config.Config, error) {
	head, err := d.store.KnownHead(ctx)
	if err != nil || head == "" {
		return config.Default(), err
	}
	tree, err := d.store.TreeOf(ctx, head)
	if err != nil {
		return config.Default(), err
	}
	entries, err := d.store.ReadTree(ctx, tree)
	if err != nil {
		return config.Default(), err
	}
	return d.repoConfig(ctx, entries)
}
