package cli

import (
	"context"
	"fmt"
	"time"

	"github.com/Hanson-Tsai/gcd/internal/disk"
)

func init() {
	register(command{
		name:    "restore",
		usage:   "restore <path> [--at <when>]",
		summary: "write an earlier version of a file back into the folder",
		run:     runRestore,
	})
}

func runRestore(ctx context.Context, e *env, args []string) error {
	fs := newDiskFlags(e, "restore")
	at := fs.String("at", "", "time (2006-01-02, \"2006-01-02 15:04\", RFC 3339) or commit to restore from; default: the newest version, even if deleted")
	force := fs.Bool("force", false, "overwrite a different file at the path")
	pos, err := parseInterspersed(fs, args)
	if err != nil {
		return err
	}
	if len(pos) != 1 {
		return errUsage
	}
	var opts disk.RestoreOptions
	opts.Force = *force
	if *at != "" {
		if opts.At, err = disk.ParseAt(*at, time.Local); err != nil {
			return err
		}
	}
	d, _, err := openCurrentDisk(e)
	if err != nil {
		return err
	}
	rel, err := folderPath(d.Folder(), pos[0])
	if err != nil {
		return err
	}
	res, err := d.Restore(ctx, rel, opts)
	if err != nil {
		return err
	}
	from := fmt.Sprintf("%.7s (%s)", res.Commit, res.Time.Local().Format(timeFormat))
	if res.Unchanged {
		fmt.Fprintf(e.stdout, "%s already matches the version from %s\n", res.Path, from)
		return nil
	}
	fmt.Fprintf(e.stdout, "restored %s from %s\nrun `gcd sync` to upload it\n", res.Path, from)
	return nil
}
