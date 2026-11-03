package cli

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"

	"github.com/Hanson-Tsai/gcd/internal/auth"
	"github.com/Hanson-Tsai/gcd/internal/compact"
	"github.com/Hanson-Tsai/gcd/internal/workflow"
)

func init() {
	register(command{
		name:    "compact",
		usage:   "compact [--disk <name>] | --local [--disk <name>] | --run [--repo <git-url>]",
		summary: "squash old history into one snapshot (on GitHub Actions, or --local)",
		run:     runCompact,
	})
}

// runCompact runs history compaction. Both modes run compact.Run:
//
//   - --local compacts the current Disk's repo from this machine, reusing the
//     Disk's git-dir so only new objects are downloaded.
//   - --run is the entry point of the GitHub Actions workflow (#15). It works
//     in a temporary git-dir and takes the repo from --repo, else from the
//     Actions variables GITHUB_SERVER_URL and GITHUB_REPOSITORY. The token
//     comes from GCD_TOKEN like for every command (auth, #13).
//
// Without a mode, gcd compact starts the compaction workflow on GitHub
// Actions (workflow_dispatch) and prints the run's page.
func runCompact(ctx context.Context, e *env, args []string) error {
	fs := newDiskFlags(e, "compact")
	local := fs.Bool("local", false, "compact the current disk's repo from this machine")
	runMode := fs.Bool("run", false, "compact a repo in a temporary git-dir (the GitHub Actions entry point)")
	repo := fs.String("repo", "", "with --run: the repo's git `url` (default: from GITHUB_SERVER_URL and GITHUB_REPOSITORY)")
	if err := fs.Parse(args); err != nil {
		return err
	}
	if fs.NArg() != 0 || (*local && *runMode) || (!*runMode && *repo != "") || (*runMode && e.disk != "") {
		return errUsage
	}
	if !*local && !*runMode {
		return dispatchCompact(ctx, e)
	}

	var opts compact.Options
	if *local {
		_, ent, err := openCurrentDisk(e)
		if err != nil {
			return err
		}
		opts = compact.Options{GitDir: filepath.Join(ent.StateDir, "git"), Remote: ent.Remote}
	} else {
		// In Actions, GITHUB_SERVER_URL is https://github.com, so the
		// token comes from GCD_TOKEN (the workflow sets it to github.token).
		url := *repo
		if url == "" {
			server, name := os.Getenv("GITHUB_SERVER_URL"), os.Getenv("GITHUB_REPOSITORY")
			if server == "" || name == "" {
				return fmt.Errorf("--run needs --repo <git-url> outside GitHub Actions")
			}
			url = server + "/" + name + ".git"
		}
		tmp, err := os.MkdirTemp("", "gcd-compact-*")
		if err != nil {
			return err
		}
		defer os.RemoveAll(tmp)
		opts = compact.Options{GitDir: filepath.Join(tmp, "git"), Remote: url}
	}
	token, err := remoteToken(opts.Remote)
	if err != nil {
		return err
	}
	opts.Token = token

	res, err := compact.Run(ctx, opts)
	if err != nil {
		return err
	}
	if res.OldHead == "" {
		fmt.Fprintln(e.stdout, "nothing to compact: the repo has no history yet")
		return nil
	}
	before := res.Cutoff.Local().Format("2006-01-02")
	if !res.Compacted {
		fmt.Fprintf(e.stdout, "nothing to compact: no history older than %d days (before %s)\n", res.RetentionDays, before)
		return nil
	}
	fmt.Fprintf(e.stdout, "compacted history older than %d days (before %s): %s squashed into snapshot %.7s, %s kept\n",
		res.RetentionDays, before, commits(res.Squashed), res.NewRoot, commits(res.Kept))
	return nil
}

func commits(n int) string {
	if n == 1 {
		return "1 commit"
	}
	return fmt.Sprintf("%d commits", n)
}

// dispatchCompact starts the current Disk's compaction workflow.
func dispatchCompact(ctx context.Context, e *env) error {
	_, ent, err := openCurrentDisk(e)
	if err != nil {
		return err
	}
	repo, ok := github.RepoOf(ent.Remote)
	if !ok {
		return fmt.Errorf("disk %q is not on GitHub (%s), so it has no Actions workflow; run `gcd compact --local`",
			ent.Name, ent.Remote)
	}
	token, err := remoteToken(ent.Remote)
	if err != nil {
		return err
	}
	url, err := github.DispatchWorkflow(ctx, repo, token, workflow.FileName, "main")
	if errors.Is(err, auth.ErrWorkflowNotFound) {
		return fmt.Errorf("%w\nInstall the workflow with `gcd workflow upgrade`, or run `gcd compact --local`", err)
	}
	if err != nil {
		return err
	}
	fmt.Fprintf(e.stdout, "started history compaction on GitHub Actions: %s\n", url)
	return nil
}
