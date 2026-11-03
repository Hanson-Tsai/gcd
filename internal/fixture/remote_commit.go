package fixture

import (
	"bytes"
	"context"
	"fmt"
	"os"
	"os/exec"
	"strings"
	"testing"

	"github.com/Hanson-Tsai/gcd/internal/gitstore"
)

// Commit writes files (slash-separated path -> content) on top of the
// remote's current main branch as if another client had pushed them, and
// returns the new commit hash. Paths not listed are kept unchanged. Use it to
// plant content gcd itself would not write, such as managed paths or a repo
// config from a newer gcd.
func (r *Remote) Commit(t testing.TB, files map[string]string, msg string) string {
	t.Helper()
	return r.commit(t, files, nil, msg)
}

// CommitMode is Commit for a single entry with an explicit tree mode, such as
// "120000" (symlink; content is the link target) or "100755" (executable).
func (r *Remote) CommitMode(t testing.TB, path, mode, content, msg string) string {
	t.Helper()
	return r.commit(t, map[string]string{path: content}, map[string]string{path: mode}, msg)
}

// Mode returns the tree mode of path at the remote's main branch.
func (r *Remote) Mode(t testing.TB, path string) string {
	t.Helper()
	out := r.gitEnv(t, nil, "", "ls-tree", "--full-tree", "refs/heads/main", "--", path)
	mode, _, _ := strings.Cut(out, " ")
	return mode
}

func (r *Remote) commit(t testing.TB, files, modes map[string]string, msg string) string {
	t.Helper()
	head := r.Head(t)
	tree := r.tree(t, head, files, modes, nil)
	args := []string{"commit-tree", tree, "-m", msg}
	if head != "" {
		args = append(args, "-p", head)
	}
	sig := []string{
		"GIT_AUTHOR_NAME=other", "GIT_AUTHOR_EMAIL=other@example.com",
		"GIT_COMMITTER_NAME=other", "GIT_COMMITTER_EMAIL=other@example.com",
	}
	commit := strings.TrimSpace(r.gitEnv(t, sig, "", args...))
	r.gitEnv(t, nil, "", "update-ref", "refs/heads/main", commit)
	return commit
}

// tree returns the tree of head with files (path -> content; mode from modes,
// default 100644) written and the del paths removed. Like gcd, it builds the
// tree without an index, which would refuse names tests plant on purpose
// (Git for Windows refuses "a:b.txt" in an index whatever its config).
func (r *Remote) tree(t testing.TB, head string, files, modes map[string]string, del []string) string {
	t.Helper()
	ctx := context.Background()
	s := &gitstore.Store{GitDir: r.Dir}
	entries := map[string]gitstore.Entry{}
	if head != "" {
		tree, err := s.TreeOf(ctx, head)
		if err == nil {
			entries, err = s.ReadTree(ctx, tree)
		}
		if err != nil {
			t.Fatalf("read tree of %s: %v", head, err)
		}
	}
	for p, content := range files {
		blob := strings.TrimSpace(r.gitEnv(t, nil, content, "hash-object", "-w", "--no-filters", "--stdin"))
		mode := modes[p]
		if mode == "" {
			mode = gitstore.RegularMode
		}
		entries[p] = gitstore.Entry{Mode: mode, Hash: blob}
	}
	for _, p := range del {
		delete(entries, p)
	}
	tree, err := s.WriteTree(ctx, entries)
	if err != nil {
		t.Fatalf("write tree: %v", err)
	}
	return tree
}

// gitEnv runs git against the bare remote with extra env and stdin.
func (r *Remote) gitEnv(t testing.TB, env []string, stdin string, args ...string) string {
	t.Helper()
	// Keep paths byte-exact: git on macOS would otherwise turn NFD names
	// into NFC ones, and tests need to plant both.
	pre := []string{"--git-dir=" + r.Dir, "-c", "core.precomposeunicode=false"}
	cmd := exec.Command("git", append(pre, args...)...)
	cmd.Env = append(os.Environ(), env...)
	cmd.Stdin = strings.NewReader(stdin)
	var stdout, stderr bytes.Buffer
	cmd.Stdout, cmd.Stderr = &stdout, &stderr
	if err := cmd.Run(); err != nil {
		t.Fatalf("git %v: %v", args, fmt.Errorf("%w: %s", err, strings.TrimSpace(stderr.String())))
	}
	return stdout.String()
}
