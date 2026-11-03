package cli

import (
	"errors"
	"fmt"
	"io"
	"os"

	"github.com/Hanson-Tsai/gcd/internal/auth"
	"github.com/Hanson-Tsai/gcd/internal/gitstore"
	"github.com/Hanson-Tsai/gcd/internal/version"
	"github.com/Hanson-Tsai/gcd/internal/workflow"
)

// Seams for tests (see export_test.go).
var (
	github auth.GitHub // zero value: api.github.com and github.com
	tokens             = auth.DefaultTokens()
	stdin  io.Reader   = os.Stdin

	releases     workflow.Releases // zero value: gcd's GitHub releases
	buildVersion = version.Version
)

// remoteToken is the token for a registered remote: "" for non-GitHub
// (file://) remotes.
func remoteToken(remote string) (string, error) {
	r, ok := github.RepoOf(remote)
	if !ok {
		return "", nil
	}
	return tokens.Lookup(r)
}

// obtainToken returns the token for r ($GCD_TOKEN, the keychain, or else
// asked for on stdin) and whether it was typed in (and so still needs
// saving).
func obtainToken(e *env, r auth.Repo) (tok string, typed bool, err error) {
	tok, err = tokens.Lookup(r)
	if err == nil || !errors.Is(err, auth.ErrNoToken) {
		return tok, false, err
	}
	tok, err = auth.PromptToken(stdin, e.stderr, r)
	return tok, err == nil, err
}

// explain adds what to do next to errors caused by GitHub credentials.
func explain(err error) error {
	var ae *gitstore.AuthError
	if errors.As(err, &ae) {
		if r, ok := github.RepoOf(ae.Remote); ok {
			return fmt.Errorf("%w\nGitHub rejected the token for %s: it is expired, revoked or lacks access.\n%s",
				err, r, auth.TokenHelp(r))
		}
	}
	if errors.Is(err, gitstore.ErrWorkflowsPermission) {
		if os.Getenv("GITHUB_ACTIONS") == "true" {
			// gcd compact --run inside the workflow, pushing with github.token.
			return fmt.Errorf("%w\nGitHub refused rewritten commits that touch %s: the Actions token cannot have\n"+
				"the %s permission. Create a fine-grained token for this repo with %s and %s,\n"+
				"add it as the repository secret %s (Settings > Secrets and variables > Actions);\n"+
				"the workflow then pushes with it. Or run `gcd compact --local` on one of your machines",
				err, workflow.Path, auth.PermWorkflows, auth.PermContents, auth.PermWorkflows, workflow.SecretName)
		}
		return fmt.Errorf("%w\nThe token is missing permission: %s. Edit it at https://github.com/settings/personal-access-tokens",
			err, auth.PermWorkflows)
	}
	return err
}
