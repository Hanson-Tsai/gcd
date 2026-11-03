package cli

import (
	"errors"
	"fmt"
	"io"
	"os"
	"strings"

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

// warnOptional warns that the token for r lacks the optional permissions
// auth.GitHub.Check found missing.
func warnOptional(e *env, r auth.Repo, optional []string) {
	if len(optional) == 0 {
		return
	}
	fmt.Fprintf(e.stderr, "warning: the token is missing permissions on %s: %s.\n"+
		"Syncing works, but `gcd compact` cannot start compaction on GitHub until the token has it\n"+
		"(edit it at https://%s/settings/personal-access-tokens); scheduled compaction and\n"+
		"`gcd compact --local` do not need it\n",
		r, strings.Join(optional, ", "), auth.Host)
}

// workflowsRefused reports a push GitHub refused for lack of Workflows
// write like the permissions auth.GitHub.Check finds missing, together with
// the optional ones Check found (also missing).
func workflowsRefused(r auth.Repo, optional []string) error {
	return &auth.MissingPermissionsError{Repo: r, Missing: append([]string{auth.PermWorkflows}, optional...)}
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
	var we *gitstore.WorkflowsPermissionError
	if errors.As(err, &we) {
		if os.Getenv("GITHUB_ACTIONS") == "true" {
			// gcd compact --run inside the workflow, pushing with github.token.
			return fmt.Errorf("%w\nGitHub refused rewritten commits that touch %s: the Actions token cannot have\n"+
				"the %s permission. Create a fine-grained token for this repo with %s and %s,\n"+
				"add it as the repository secret %s (Settings > Secrets and variables > Actions);\n"+
				"the workflow then pushes with it. Or run `gcd compact --local` on one of your machines",
				err, workflow.Path, auth.PermWorkflows, auth.PermContents, auth.PermWorkflows, workflow.SecretName)
		}
		if r, ok := github.RepoOf(we.Remote); ok {
			return workflowsRefused(r, nil)
		}
	}
	return err
}
