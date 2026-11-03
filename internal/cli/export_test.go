package cli

import (
	"io"
	"os"
	"testing"

	"github.com/Hanson-Tsai/gcd/internal/auth"
)

// UseFakeGitHub points the CLI at a fake GitHub, an in-memory keychain and
// the given stdin until the test ends. GCD_TOKEN is still read from the
// environment.
func UseFakeGitHub(t testing.TB, gh auth.GitHub, kr auth.Keyring, in io.Reader) {
	oldGH, oldTokens, oldIn := github, tokens, stdin
	github, tokens, stdin = gh, auth.Tokens{Keyring: kr, Getenv: os.Getenv}, in
	t.Cleanup(func() { github, tokens, stdin = oldGH, oldTokens, oldIn })
}
