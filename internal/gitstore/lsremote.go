package gitstore

import (
	"context"
	"errors"
	"os/exec"
	"strings"
)

// RemoteHead asks the remote for its branch head with one ls-remote, without
// downloading anything. It returns "" when the remote has no commits yet.
func (s *Store) RemoteHead(ctx context.Context) (string, error) {
	out, err := s.git(ctx, nil, "ls-remote", "--exit-code", remoteName, "refs/heads/"+Branch)
	if err != nil {
		var ee *exec.ExitError
		if errors.As(err, &ee) && ee.ExitCode() == 2 {
			return "", nil // --exit-code: no such ref, the remote is empty
		}
		return "", err
	}
	head, _, _ := strings.Cut(strings.TrimSpace(out), "\t")
	return head, nil
}

// KnownHead returns the remote head as of the last Fetch or Push ("" when
// there was none). It reads only the local git-dir.
func (s *Store) KnownHead(ctx context.Context) (string, error) {
	return s.resolve(ctx, remoteRef)
}
