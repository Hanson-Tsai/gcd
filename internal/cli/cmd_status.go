package cli

import (
	"context"
	"fmt"
)

func init() {
	register(command{
		name:    "status",
		usage:   "status [--disk <name>]",
		summary: "show what the next sync would upload, download and leave alone",
		run:     runStatus,
	})
}

// runStatus prints disk.Status. It fetches the remote but changes neither the
// folder, the remote nor the base, and it does not wait for a running sync.
func runStatus(ctx context.Context, e *env, args []string) error {
	fs := newDiskFlags(e, "status")
	if err := fs.Parse(args); err != nil {
		return err
	}
	if fs.NArg() != 0 {
		return errUsage
	}
	d, _, err := openCurrentDisk(e)
	if err != nil {
		return err
	}
	res, err := d.Status(ctx)
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
		fmt.Fprintf(e.stdout, "conflict %s: changed on both sides; the local version would be kept as %s\n", c.Path, c.Copy)
	}
	for _, p := range res.Skipped {
		fmt.Fprintf(e.stdout, "changing %s: being written right now; a sync would wait for it\n", p)
	}
	for _, r := range res.Rejected {
		fmt.Fprintf(e.stdout, "not synced %s\n", r)
	}
	if len(res.Uploaded)+len(res.Downloaded) == 0 {
		fmt.Fprintln(e.stdout, "up to date")
	} else {
		fmt.Fprintf(e.stdout, "pending: %d to upload, %d to download\n", len(res.Uploaded), len(res.Downloaded))
	}
	if res.NeedsConfirm {
		fmt.Fprintf(e.stdout, "the next sync deletes %d files, more than the repo config allows without confirmation;\n"+
			"if these deletions are intended, run `gcd sync --yes`\n", res.PendingDeletes)
	}
	return nil
}
