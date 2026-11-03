package cli

import (
	"context"
	"fmt"

	"github.com/Hanson-Tsai/gcd/internal/disk"
)

func init() {
	register(command{
		name:    "sync",
		usage:   "sync",
		summary: "upload local changes and download remote changes",
		run:     runSync,
	})
}

func runSync(ctx context.Context, e *env, args []string) error {
	fs := newFlags(e, "sync")
	if err := fs.Parse(args); err != nil {
		return err
	}
	if fs.NArg() != 0 {
		return errUsage
	}
	d, _, err := openCurrentDisk()
	if err != nil {
		return err
	}
	res, err := d.Sync(ctx, disk.SyncOptions{})
	if err != nil {
		return err
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
