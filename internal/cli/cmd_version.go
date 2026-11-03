package cli

import (
	"context"
	"fmt"
	"runtime"

	"github.com/Hanson-Tsai/gcd/internal/version"
)

func init() {
	register(command{
		name:       "version",
		usage:      "version",
		summary:    "print the gcd version",
		run:        runVersion,
		withoutGit: true,
	})
}

func runVersion(ctx context.Context, e *env, args []string) error {
	fs := newFlags(e, "version")
	if err := fs.Parse(args); err != nil {
		return err
	}
	if fs.NArg() != 0 {
		return errUsage
	}
	fmt.Fprintf(e.stdout, "gcd version %s %s/%s\n", version.Version(), runtime.GOOS, runtime.GOARCH)
	if c := version.Commit(); c != "" {
		fmt.Fprintf(e.stdout, "commit: %s\n", c)
	}
	if d := version.Date(); d != "" {
		fmt.Fprintf(e.stdout, "date:   %s\n", d)
	}
	return nil
}
