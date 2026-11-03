package cli

import (
	"context"
	"fmt"

	"github.com/Hanson-Tsai/gcd/internal/disk"
)

func init() {
	register(command{
		name:    "sync",
		usage:   "sync [--disk <name>] [--yes] [--json]",
		summary: "upload local changes and download remote changes",
		run:     runSync,
		json:    true,
	})
}

func runSync(ctx context.Context, e *env, args []string) error {
	fs := newDiskFlags(e, "sync")
	yes := fs.Bool("yes", false, "go ahead even if the sync deletes more files than the repo config allows")
	jsonFlag(fs, e)
	if err := fs.Parse(args); err != nil {
		return err
	}
	if fs.NArg() != 0 {
		return errUsage
	}
	d, ent, err := openCurrentDisk(e)
	if err != nil {
		return err
	}
	res, err := d.Sync(ctx, disk.SyncOptions{Yes: *yes})
	if res.NeedsConfirm {
		return &confirmError{
			err:     fmt.Errorf("%w\nif these deletions are intended, run `gcd sync --yes`", err),
			pending: res.PendingDeletes,
		}
	}
	if err != nil {
		return err
	}
	if e.json {
		return writeJSON(e.stdout, syncedJSON(ent.Name, res))
	}
	for _, c := range res.Uploaded {
		fmt.Fprintf(e.stdout, "up    %s %s\n", sign(c.Kind), c.Path)
	}
	for _, c := range res.Downloaded {
		fmt.Fprintf(e.stdout, "down  %s %s\n", sign(c.Kind), c.Path)
	}
	for _, c := range res.Conflicts {
		fmt.Fprintf(e.stdout, "conflict %s: local version kept as %s\n", c.Path, c.Copy)
	}
	for _, p := range res.Skipped {
		fmt.Fprintf(e.stdout, "skipped %s: changed during sync, next sync picks it up\n", p)
	}
	for _, r := range res.Rejected {
		fmt.Fprintf(e.stdout, "not synced %s\n", r)
	}
	if len(res.Uploaded)+len(res.Downloaded) == 0 {
		fmt.Fprintln(e.stdout, "up to date")
	} else {
		fmt.Fprintf(e.stdout, "synced: %d up, %d down\n", len(res.Uploaded), len(res.Downloaded))
	}
	return nil
}

func sign(k disk.ChangeKind) string {
	switch k {
	case disk.Added:
		return "+"
	case disk.Deleted:
		return "-"
	}
	return "~"
}
