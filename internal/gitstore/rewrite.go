package gitstore

import (
	"bufio"
	"context"
	"fmt"
	"io"
	"strconv"
	"strings"
)

// Raw object access for history rewriting (compaction). Commits are read and
// written as raw objects, so every header (author, committer, dates with
// their zone offsets, encoding) and the message survive byte for byte.

// FirstParents lists the first-parent chain of commit, oldest first.
func (s *Store) FirstParents(ctx context.Context, commit string) ([]string, error) {
	out, err := s.git(ctx, nil, "rev-list", "--first-parent", "--reverse", commit, "--")
	if err != nil {
		return nil, err
	}
	return strings.Fields(out), nil
}

// ReadCommits returns the raw objects of the given commits, in order, using
// one cat-file process.
func (s *Store) ReadCommits(ctx context.Context, hashes []string) ([][]byte, error) {
	if len(hashes) == 0 {
		return nil, nil
	}
	out, err := s.git(ctx, strings.NewReader(strings.Join(hashes, "\n")+"\n"), "cat-file", "--batch")
	if err != nil {
		return nil, err
	}
	r := bufio.NewReader(strings.NewReader(out))
	raws := make([][]byte, len(hashes))
	for i, h := range hashes {
		header, err := r.ReadString('\n')
		if err != nil {
			return nil, fmt.Errorf("cat-file: reading %s: %w", h, err)
		}
		f := strings.Fields(header)
		if len(f) != 3 || f[1] != "commit" {
			return nil, fmt.Errorf("cat-file: %s is not a commit: %q", h, strings.TrimSpace(header))
		}
		size, err := strconv.Atoi(f[2])
		if err != nil {
			return nil, fmt.Errorf("cat-file: bad header %q", strings.TrimSpace(header))
		}
		raws[i] = make([]byte, size+1) // object plus the trailing newline
		if _, err := io.ReadFull(r, raws[i]); err != nil {
			return nil, fmt.Errorf("cat-file: reading %s: %w", h, err)
		}
		raws[i] = raws[i][:size]
	}
	return raws, nil
}

// CommitHash computes the hash git gives a raw commit object (SHA-1, the
// object format of GitHub repos), without writing it.
func CommitHash(raw []byte) string { return objectHash("commit", raw) }

// WriteCommits stores raw commit objects in one hash-object process and
// returns their hashes in order. git validates each object as it writes it.
func (s *Store) WriteCommits(ctx context.Context, raws [][]byte) ([]string, error) {
	return s.writeObjects(ctx, "commit", raws)
}

// cQuote quotes a path the way --stdin-paths unquotes it, so Windows
// backslashes and unusual characters pass through intact.
func cQuote(p string) string {
	r := strings.NewReplacer(`\`, `\\`, `"`, `\"`, "\n", `\n`)
	return `"` + r.Replace(p) + `"`
}

// ForcePush replaces the remote branch with commit, but only while the
// remote branch still points at expect (--force-with-lease). If it moved, the
// push is refused and the error wraps ErrPushRejected; the remote is left as
// it was.
func (s *Store) ForcePush(ctx context.Context, commit, expect string) error {
	_, err := s.git(ctx, nil, "push", "--quiet",
		"--force-with-lease=refs/heads/"+Branch+":"+expect,
		remoteName, commit+":refs/heads/"+Branch)
	if err != nil {
		if pushRaced(err) {
			return fmt.Errorf("%w: %v", ErrPushRejected, err)
		}
		return err
	}
	_, err = s.git(ctx, nil, "update-ref", remoteRef, commit)
	return err
}
