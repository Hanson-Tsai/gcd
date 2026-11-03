package cli

import (
	"context"
	"fmt"
	"os"

	"github.com/Hanson-Tsai/gcd/internal/auth"
)

func init() {
	register(command{
		name:    "login",
		usage:   "login <owner/repo>",
		summary: "save a new GitHub token for a repo in the OS keychain",
		run:     runLogin,
	})
}

// runLogin replaces the keychain token of a repo, e.g. after it expired.
// It always asks, verifies the new token like init does, then saves it.
func runLogin(ctx context.Context, e *env, args []string) error {
	fs := newFlags(e, "login")
	if err := fs.Parse(args); err != nil {
		return err
	}
	if fs.NArg() != 1 {
		return errUsage
	}
	r, err := auth.ParseRepo(fs.Arg(0))
	if err != nil {
		return err
	}
	tok, err := auth.PromptToken(stdin, e.stderr, r)
	if err != nil {
		return err
	}
	optional, err := github.Check(ctx, r, tok)
	if err != nil {
		return err
	}
	if err := tokens.Store(r, tok); err != nil {
		return err
	}
	fmt.Fprintf(e.stdout, "saved the token for %s in the OS keychain\n", r)
	warnOptional(e, r, optional)
	if os.Getenv(auth.TokenEnv) != "" {
		fmt.Fprintf(e.stderr, "note: %s is set and takes precedence over the keychain\n", auth.TokenEnv)
	}
	return nil
}
