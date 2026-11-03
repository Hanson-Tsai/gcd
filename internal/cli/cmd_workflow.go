package cli

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"github.com/Hanson-Tsai/gcd/internal/workflow"
)

func init() {
	register(command{
		name:    "workflow",
		usage:   "workflow upgrade [--disk <name>] [--version <x.y.z>]",
		summary: "pin the compaction workflow to this gcd release (or --version)",
		run:     runWorkflow,
	})
}

func runWorkflow(ctx context.Context, e *env, args []string) error {
	fs := newDiskFlags(e, "workflow")
	ver := fs.String("version", "", "pin this gcd release `x.y.z` instead of the running gcd's version")
	pos, err := parseInterspersed(fs, args)
	if err != nil {
		return err
	}
	if len(pos) != 1 || pos[0] != "upgrade" {
		return errUsage
	}
	pin, err := releasePin(ctx, strings.TrimPrefix(*ver, "v"))
	if err != nil {
		if errors.Is(err, workflow.ErrDevBuild) && *ver == "" {
			return fmt.Errorf("%w\nThis gcd is a development build; pass --version <x.y.z> to pin a released gcd", err)
		}
		return err
	}
	d, _, err := openCurrentDisk(e)
	if err != nil {
		return err
	}
	res, err := d.UpgradeWorkflow(ctx, pin)
	if err != nil {
		return err
	}
	switch {
	case !res.Changed:
		fmt.Fprintf(e.stdout, "the compaction workflow already runs gcd %s\n", pin.Version)
	case res.Previous.Pin.Version != "":
		fmt.Fprintf(e.stdout, "upgraded the compaction workflow %s: gcd %s -> %s\n", workflow.Path, res.Previous.Pin.Version, pin.Version)
	default:
		fmt.Fprintf(e.stdout, "wrote the compaction workflow %s (gcd %s)\n", workflow.Path, pin.Version)
	}
	return nil
}

// releasePin is the workflow pin for gcd release v, or for this gcd's own
// version when v is "". Development builds have no release:
// workflow.ErrDevBuild.
func releasePin(ctx context.Context, v string) (workflow.Pin, error) {
	if v == "" {
		v = buildVersion()
	}
	pin, err := releases.Pin(ctx, v)
	if errors.Is(err, workflow.ErrNoRelease) {
		err = fmt.Errorf("gcd %s: %w (is it a published release?)", v, err)
	}
	return pin, err
}
