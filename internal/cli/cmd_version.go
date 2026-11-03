package cli

import (
	"context"
	"fmt"
	"runtime"

	"github.com/Hanson-Tsai/gcd/internal/version"
	"github.com/Hanson-Tsai/gcd/internal/workflow"
)

// versionJSON is the object of `version --json`.
type versionJSON struct {
	Version string `json:"version"` // without "v"; "dev" or a Go pseudo-version for development builds
	OS      string `json:"os"`
	Arch    string `json:"arch"`
	// Dev: a development build, not a published release (version checks
	// should let it pass).
	Dev bool `json:"dev"`
}

func init() {
	register(command{
		name:       "version",
		usage:      "version [--json]",
		summary:    "print the gcd version",
		run:        runVersion,
		withoutGit: true,
		json:       true,
	})
}

func runVersion(ctx context.Context, e *env, args []string) error {
	fs := newFlags(e, "version")
	jsonFlag(fs, e)
	if err := fs.Parse(args); err != nil {
		return err
	}
	if fs.NArg() != 0 {
		return errUsage
	}
	v := buildVersion()
	if e.json {
		return writeJSON(e.stdout, versionJSON{Version: v, OS: runtime.GOOS, Arch: runtime.GOARCH, Dev: !workflow.IsRelease(v)})
	}
	fmt.Fprintf(e.stdout, "gcd version %s %s/%s\n", v, runtime.GOOS, runtime.GOARCH)
	if c := version.Commit(); c != "" {
		fmt.Fprintf(e.stdout, "commit: %s\n", c)
	}
	if d := version.Date(); d != "" {
		fmt.Fprintf(e.stdout, "date:   %s\n", d)
	}
	return nil
}
