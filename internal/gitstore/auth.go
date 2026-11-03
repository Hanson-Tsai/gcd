package gitstore

import (
	"errors"
	"fmt"
	"os"
	"strings"
)

// How the token reaches git (see docs/architecture.md, "Credentials"):
//
//   - every git process gets `-c credential.helper=`, which empties the
//     helper list, so the user's osxkeychain/manager helpers neither answer
//     with a stale credential nor store gcd's token;
//   - with no helper left, git asks GIT_ASKPASS, which is the gcd executable
//     itself; ServeAskpass answers from GCD_ASKPASS_TOKEN, an environment
//     variable set only on the git child process.
//
// The token is never in argv, the remote URL, the git-dir config or an error.
const (
	askpassEnv      = "GCD_ASKPASS"
	askpassTokenEnv = "GCD_ASKPASS_TOKEN"
	// TokenUser is the HTTPS username sent with a token; GitHub only checks
	// the password.
	TokenUser = "x-access-token"
)

var (
	// ErrAuth means the remote rejected the credentials: no token, an
	// invalid, expired or revoked one, or one without access to the repo.
	ErrAuth = errors.New("authentication failed")
	// ErrWorkflowsPermission means a push touching .github/workflows/ was
	// refused because the token lacks the Workflows write permission.
	ErrWorkflowsPermission = errors.New("token lacks Workflows write permission")
)

// AuthError is a git failure caused by credentials. It matches ErrAuth.
type AuthError struct {
	Remote string // the remote URL (never contains the token)
	Err    error  // the git failure, token scrubbed
}

func (e *AuthError) Error() string {
	return fmt.Sprintf("%s rejected the credentials: %v", e.Remote, e.Err)
}
func (e *AuthError) Unwrap() error        { return e.Err }
func (e *AuthError) Is(target error) bool { return target == ErrAuth }

// WorkflowsPermissionError is a push refused because it touches
// .github/workflows/ and the token lacks Workflows write. It matches
// ErrWorkflowsPermission.
type WorkflowsPermissionError struct {
	Remote string // the remote URL (never contains the token)
	Err    error  // the git failure, token scrubbed
}

func (e *WorkflowsPermissionError) Error() string {
	return fmt.Sprintf("%v: %v", ErrWorkflowsPermission, e.Err)
}
func (e *WorkflowsPermissionError) Unwrap() error        { return e.Err }
func (e *WorkflowsPermissionError) Is(target error) bool { return target == ErrWorkflowsPermission }

// SetToken makes git authenticate to the remote with token (as the HTTPS
// password). An empty token means no credentials.
func (s *Store) SetToken(token string) { s.token = token }

// ServeAskpass handles the case where this process was started by git as
// GIT_ASKPASS: it prints the answer to the prompt in os.Args[1] and returns
// true, and the caller must exit right away. Otherwise it returns false.
// main (and the gitstore tests' TestMain) call it first thing.
func ServeAskpass() bool {
	if os.Getenv(askpassEnv) != "1" {
		return false
	}
	prompt := ""
	if len(os.Args) > 1 {
		prompt = os.Args[1]
	}
	if strings.HasPrefix(prompt, "Username") {
		fmt.Println(TokenUser)
	} else {
		fmt.Println(os.Getenv(askpassTokenEnv))
	}
	return true
}

// credArgs come before the git subcommand: drop every configured credential
// helper.
var credArgs = []string{"-c", "credential.helper="}

// credEnv points git's askpass at this executable when there is a token, and
// at nothing otherwise (an inherited GIT_ASKPASS, e.g. from an IDE, must not
// pop up a dialog).
func (s *Store) credEnv() ([]string, error) {
	if s.token == "" {
		return []string{"GIT_ASKPASS=", "SSH_ASKPASS="}, nil
	}
	exe, err := os.Executable()
	if err != nil {
		return nil, fmt.Errorf("locate gcd executable for git credentials: %w", err)
	}
	return []string{
		"GIT_ASKPASS=" + exe,
		askpassEnv + "=1",
		askpassTokenEnv + "=" + s.token,
	}, nil
}

var authFailures = []string{
	"Authentication failed",
	"could not read Username",
	"could not read Password",
	"Invalid username or token",
	"The requested URL returned error: 401",
	"The requested URL returned error: 403",
	"Permission to ", // "remote: Permission to o/r.git denied to user."
}

// classify scrubs the token from a git failure and tags credential problems.
func (s *Store) classify(err error) error {
	var ge *Error
	if !errors.As(err, &ge) {
		return err
	}
	if s.token != "" {
		ge.Stderr = strings.ReplaceAll(ge.Stderr, s.token, "***")
	}
	// "refusing to allow a Personal Access Token (or: a GitHub App, as the
	// Actions token is) to create or update workflow ..."
	if strings.Contains(ge.Stderr, "refusing to allow a ") && strings.Contains(ge.Stderr, "to create or update workflow") {
		return &WorkflowsPermissionError{Remote: s.remote, Err: err}
	}
	for _, p := range authFailures {
		if strings.Contains(ge.Stderr, p) {
			return &AuthError{Remote: s.remote, Err: err}
		}
	}
	return err
}
