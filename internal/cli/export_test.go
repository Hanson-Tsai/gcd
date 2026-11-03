package cli

import (
	"io"
	"os"
	"testing"

	"github.com/Hanson-Tsai/gcd/internal/auth"
	"github.com/Hanson-Tsai/gcd/internal/workflow"
)

// UseFakeGitHub points the CLI at a fake GitHub, an in-memory keychain and
// the given stdin until the test ends. GCD_TOKEN is still read from the
// environment.
func UseFakeGitHub(t testing.TB, gh auth.GitHub, kr auth.Keyring, in io.Reader) {
	oldGH, oldTokens, oldIn := github, tokens, stdin
	github, tokens, stdin = gh, auth.Tokens{Keyring: kr, Getenv: os.Getenv}, in
	t.Cleanup(func() { github, tokens, stdin = oldGH, oldTokens, oldIn })
}

// UseReleases makes the CLI find gcd releases (checksums.txt) at rel and
// believe it is gcd version v until the test ends.
func UseReleases(t testing.TB, rel workflow.Releases, v string) {
	oldRel, oldVersion := releases, buildVersion
	releases, buildVersion = rel, func() string { return v }
	t.Cleanup(func() { releases, buildVersion = oldRel, oldVersion })
}
