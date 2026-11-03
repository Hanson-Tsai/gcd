package cli

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"runtime"
	"strings"

	"github.com/Hanson-Tsai/gcd/internal/config"
	"github.com/Hanson-Tsai/gcd/internal/disk"
)

func init() {
	register(command{
		name:    "config",
		usage:   "config edit",
		summary: "edit the repo config in $EDITOR, check it and commit it",
		run:     runConfig,
	})
}

func runConfig(ctx context.Context, e *env, args []string) error {
	fs := newFlags(e, "config")
	if err := fs.Parse(args); err != nil {
		return err
	}
	if fs.NArg() != 1 || fs.Arg(0) != "edit" {
		return errUsage
	}
	d, _, err := openCurrentDisk()
	if err != nil {
		return err
	}
	return editRepoConfig(ctx, e, d)
}

// editRepoConfig opens the repo config in the user's editor and commits the
// result only if it parses strictly. A rejected edit is kept in its temp file
// so the user can fix it and retry.
func editRepoConfig(ctx context.Context, e *env, d *disk.Disk) error {
	current, head, err := d.RepoConfig(ctx)
	if err != nil {
		return err
	}
	original := current
	if current == nil {
		original = config.DefaultTOML()
	} else if _, err := config.Parse(current); errors.Is(err, config.ErrFormatTooNew) {
		return err // an older gcd must not rewrite a newer repo's config
	}

	tmp, err := os.CreateTemp("", "gcd-config-*.toml")
	if err != nil {
		return err
	}
	tmpName := tmp.Name()
	_, err = tmp.Write(original)
	if cerr := tmp.Close(); err == nil {
		err = cerr
	}
	keep := false
	defer func() {
		if !keep {
			os.Remove(tmpName)
		}
	}()
	if err != nil {
		return err
	}

	if err := runEditor(e, tmpName); err != nil {
		return err
	}
	edited, err := os.ReadFile(tmpName)
	if err != nil {
		return err
	}
	if current != nil && bytes.Equal(edited, current) {
		fmt.Fprintln(e.stdout, "no changes")
		return nil
	}
	if _, err := config.ParseStrict(edited); err != nil {
		keep = true
		return fmt.Errorf("invalid repo config, not committed:\n%w\nyour edit is kept in %s", err, tmpName)
	}
	commit, err := d.SetRepoConfig(ctx, edited, head)
	if err != nil {
		keep = true
		return fmt.Errorf("%w\nyour edit is kept in %s", err, tmpName)
	}
	fmt.Fprintf(e.stdout, "committed repo config (%s); other machines pick it up on their next sync\n", short(commit))
	return nil
}

// runEditor runs $EDITOR (else $VISUAL, else a platform default) on file and
// waits for it. $EDITOR may carry arguments ("code --wait"); a value that
// names an existing file is used as-is, so paths with spaces work.
func runEditor(e *env, file string) error {
	ed := os.Getenv("EDITOR")
	if ed == "" {
		ed = os.Getenv("VISUAL")
	}
	if ed == "" {
		ed = "vi"
		if runtime.GOOS == "windows" {
			ed = "notepad"
		}
	}
	argv := []string{ed}
	if _, err := os.Stat(ed); err != nil {
		argv = strings.Fields(ed)
	}
	cmd := exec.Command(argv[0], append(argv[1:], file)...)
	cmd.Stdin, cmd.Stdout, cmd.Stderr = os.Stdin, e.stdout, e.stderr
	if err := cmd.Run(); err != nil {
		return fmt.Errorf("editor %q: %w", ed, err)
	}
	return nil
}

func short(hash string) string {
	if len(hash) > 7 {
		return hash[:7]
	}
	return hash
}
