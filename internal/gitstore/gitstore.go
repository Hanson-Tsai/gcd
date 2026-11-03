// Package gitstore wraps the system git binary. git is used only to store and
// transfer objects and commits: no merge, rebase, pull, index-based work tree
// or checkout. The git-dir is a bare repository kept outside the synced folder.
//
// All content is handled as binary (hash-object --no-filters, cat-file blob),
// so line endings are never converted.
package gitstore

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"
)

// Branch is the single branch gcd syncs through.
const Branch = "main"

const (
	remoteName = "origin"
	remoteRef  = "refs/remotes/" + remoteName + "/" + Branch
	// peekRef is where Peek records the remote head, apart from remoteRef.
	peekRef = "refs/gcd/peek/" + Branch
	// RegularMode is the only file mode gcd writes: no exec bit, no symlinks.
	RegularMode = "100644"
)

// MinVersion is the oldest system git gcd supports.
var MinVersion = [3]int{2, 30, 0}

var (
	versionOnce sync.Once
	versionErr  error
)

// CheckVersion verifies that system git exists and is at least MinVersion.
// The result is computed once per process.
func CheckVersion() error {
	versionOnce.Do(func() {
		out, err := exec.Command("git", "version").Output()
		if err != nil {
			versionErr = fmt.Errorf("gcd needs system git %s or newer: %w", fmtVersion(MinVersion), err)
			return
		}
		v, err := ParseVersion(string(out))
		if err != nil {
			versionErr = err
			return
		}
		if less(v, MinVersion) {
			versionErr = fmt.Errorf("gcd needs git %s or newer, found %s", fmtVersion(MinVersion), fmtVersion(v))
		}
	})
	return versionErr
}

var versionRE = regexp.MustCompile(`git version (\d+)\.(\d+)(?:\.(\d+))?`)

// ParseVersion extracts major.minor.patch from `git version` output, e.g.
// "git version 2.45.1.windows.1" or "git version 2.39.3 (Apple Git-146)".
func ParseVersion(s string) ([3]int, error) {
	m := versionRE.FindStringSubmatch(s)
	if m == nil {
		return [3]int{}, fmt.Errorf("cannot parse git version from %q", strings.TrimSpace(s))
	}
	var v [3]int
	for i := 0; i < 3; i++ {
		if m[i+1] != "" {
			v[i], _ = strconv.Atoi(m[i+1])
		}
	}
	return v, nil
}

func less(a, b [3]int) bool {
	for i := range a {
		if a[i] != b[i] {
			return a[i] < b[i]
		}
	}
	return false
}

func fmtVersion(v [3]int) string { return fmt.Sprintf("%d.%d.%d", v[0], v[1], v[2]) }

// Entry is one file in a tree.
type Entry struct {
	Mode string // e.g. "100644"
	Hash string // blob hash
}

// Signature identifies the author/committer of a commit.
type Signature struct {
	Name  string
	Email string
	When  time.Time // zero means now
}

// Store is a bare git-dir bound to one remote.
type Store struct {
	GitDir string
	remote string // remote URL, for error messages
	token  string // HTTPS password for the remote; see SetToken
}

// Open prepares gitDir as a bare repository whose "origin" remote is
// remoteURL. It is idempotent: an existing git-dir is reused and its remote
// URL updated.
func Open(ctx context.Context, gitDir, remoteURL string) (*Store, error) {
	if err := CheckVersion(); err != nil {
		return nil, err
	}
	s := &Store{GitDir: gitDir, remote: remoteURL}
	if _, err := os.Stat(filepath.Join(gitDir, "HEAD")); errors.Is(err, os.ErrNotExist) {
		if err := os.MkdirAll(gitDir, 0o755); err != nil {
			return nil, err
		}
		if _, err := run(ctx, "", nil, nil, "init", "--quiet", "--bare", "--initial-branch="+Branch, gitDir); err != nil {
			return nil, err
		}
	} else if err != nil {
		return nil, err
	}
	for _, kv := range [][2]string{
		{"core.autocrlf", "false"},
		{"core.safecrlf", "false"},
		{"gc.auto", "0"},
	} {
		if _, err := s.git(ctx, nil, "config", kv[0], kv[1]); err != nil {
			return nil, err
		}
	}
	if _, err := s.git(ctx, nil, "config", "remote."+remoteName+".url", remoteURL); err != nil {
		return nil, err
	}
	return s, nil
}

// Fetch downloads the remote branch and returns its head commit, or "" when
// the remote has no commits yet. It moves the remote-tracking ref
// (refs/remotes/origin/main), the device's published view of the remote.
func (s *Store) Fetch(ctx context.Context) (string, error) {
	return s.fetchInto(ctx, remoteRef, "--prune")
}

// Peek is Fetch for read-only callers such as `gcd status`: it downloads the
// same objects but records the head only in a private ref, so the
// remote-tracking ref keeps meaning "the remote as of this device's last
// fetch for a sync" and anything comparing it with the remote still sees an
// update as new. It writes no FETCH_HEAD either, so it can run beside a Sync
// of the same git-dir.
func (s *Store) Peek(ctx context.Context) (string, error) {
	// --refmap= stops git from also updating remote-tracking refs
	// opportunistically from a configured fetch refspec.
	return s.fetchInto(ctx, peekRef, "--refmap=", "--no-write-fetch-head")
}

func (s *Store) fetchInto(ctx context.Context, ref string, flags ...string) (string, error) {
	args := append([]string{"fetch", "--quiet", "--no-tags"}, flags...)
	args = append(args, remoteName, "+refs/heads/"+Branch+":"+ref)
	if _, err := s.git(ctx, nil, args...); err != nil {
		// An empty remote has no refs/heads/main to fetch.
		if strings.Contains(err.Error(), "couldn't find remote ref") {
			return "", nil
		}
		return "", err
	}
	return s.resolve(ctx, ref)
}

func (s *Store) resolve(ctx context.Context, ref string) (string, error) {
	out, err := s.git(ctx, nil, "rev-parse", "--verify", "--quiet", ref)
	if err != nil {
		var ee *exec.ExitError
		if errors.As(err, &ee) && ee.ExitCode() == 1 {
			return "", nil
		}
		return "", err
	}
	return strings.TrimSpace(out), nil
}

// TreeOf returns the tree hash of commit.
func (s *Store) TreeOf(ctx context.Context, commit string) (string, error) {
	out, err := s.git(ctx, nil, "rev-parse", "--verify", commit+"^{tree}")
	return strings.TrimSpace(out), err
}

// ReadTree lists every blob in tree recursively as slash-separated path ->
// Entry. An empty tree hash returns an empty map.
func (s *Store) ReadTree(ctx context.Context, tree string) (map[string]Entry, error) {
	entries := map[string]Entry{}
	if tree == "" {
		return entries, nil
	}
	out, err := s.git(ctx, nil, "ls-tree", "-r", "-z", "--full-tree", tree)
	if err != nil {
		return nil, err
	}
	for _, rec := range strings.Split(out, "\x00") {
		if rec == "" {
			continue
		}
		meta, path, ok := strings.Cut(rec, "\t")
		f := strings.Fields(meta)
		if !ok || len(f) != 3 {
			return nil, fmt.Errorf("unexpected ls-tree record %q", rec)
		}
		if f[1] != "blob" {
			continue // submodules are not part of a Disk
		}
		entries[path] = Entry{Mode: f[0], Hash: f[2]}
	}
	return entries, nil
}

// WriteBlobs stores the given files as blobs (byte-for-byte, no filters) and
// returns their hashes in the same order.
func (s *Store) WriteBlobs(ctx context.Context, files []string) ([]string, error) {
	if len(files) == 0 {
		return nil, nil
	}
	in := strings.Join(files, "\n") + "\n"
	out, err := s.git(ctx, strings.NewReader(in), "hash-object", "-w", "--no-filters", "--stdin-paths")
	if err != nil {
		return nil, err
	}
	hashes := strings.Fields(out)
	if len(hashes) != len(files) {
		return nil, fmt.Errorf("hash-object returned %d hashes for %d files", len(hashes), len(files))
	}
	return hashes, nil
}

// WriteBlob stores content as a blob (no filters) and returns its hash.
func (s *Store) WriteBlob(ctx context.Context, content []byte) (string, error) {
	out, err := s.git(ctx, bytes.NewReader(content), "hash-object", "-w", "--no-filters", "--stdin")
	return strings.TrimSpace(out), err
}

// Blob opens the content of a blob. The caller must Close it.
func (s *Store) Blob(ctx context.Context, hash string) (io.ReadCloser, error) {
	out, err := s.git(ctx, nil, "cat-file", "blob", hash)
	if err != nil {
		return nil, err
	}
	return io.NopCloser(strings.NewReader(out)), nil
}

// WriteTree builds a (nested) tree object from slash-separated path -> Entry
// and returns its hash. It uses a throwaway index file inside the git-dir.
func (s *Store) WriteTree(ctx context.Context, entries map[string]Entry) (string, error) {
	paths := make([]string, 0, len(entries))
	for p := range entries {
		paths = append(paths, p)
	}
	sort.Strings(paths)
	var in bytes.Buffer
	for _, p := range paths {
		e := entries[p]
		fmt.Fprintf(&in, "%s %s\t%s\x00", e.Mode, e.Hash, p)
	}
	idx, err := os.CreateTemp(s.GitDir, "gcd-index-*")
	if err != nil {
		return "", err
	}
	idxPath := idx.Name()
	idx.Close()
	os.Remove(idxPath) // git wants to create the index itself
	defer os.Remove(idxPath)
	env := []string{"GIT_INDEX_FILE=" + idxPath}
	if _, err := s.gitEnv(ctx, env, &in, "update-index", "-z", "--index-info"); err != nil {
		return "", err
	}
	out, err := s.gitEnv(ctx, env, nil, "write-tree")
	return strings.TrimSpace(out), err
}

// CommitTree creates a commit of tree with an optional parent.
func (s *Store) CommitTree(ctx context.Context, tree, parent string, sig Signature, msg string) (string, error) {
	args := []string{"commit-tree", tree, "-m", msg}
	if parent != "" {
		args = append(args, "-p", parent)
	}
	when := sig.When
	if when.IsZero() {
		when = time.Now()
	}
	date := fmt.Sprintf("%d %s", when.Unix(), when.Format("-0700"))
	env := []string{
		"GIT_AUTHOR_NAME=" + sig.Name, "GIT_AUTHOR_EMAIL=" + sig.Email, "GIT_AUTHOR_DATE=" + date,
		"GIT_COMMITTER_NAME=" + sig.Name, "GIT_COMMITTER_EMAIL=" + sig.Email, "GIT_COMMITTER_DATE=" + date,
	}
	out, err := s.gitEnv(ctx, env, nil, args...)
	return strings.TrimSpace(out), err
}

// ErrPushRejected means the remote branch moved since the last Fetch (the
// push was not a fast-forward, or lost a race for the ref lock). The caller
// should fetch, rebuild its commit on the new head and push again.
var ErrPushRejected = errors.New("push rejected: remote branch changed")

// Push publishes commit as the remote branch. It is a plain (fast-forward
// only) push, so a concurrent update on the remote makes it fail with an
// error wrapping ErrPushRejected.
func (s *Store) Push(ctx context.Context, commit string) error {
	_, err := s.git(ctx, nil, "push", "--quiet", remoteName, commit+":refs/heads/"+Branch)
	if err != nil {
		if pushRaced(err) {
			return fmt.Errorf("%w: %v", ErrPushRejected, err)
		}
		return err
	}
	// Record what we pushed as the known remote head.
	_, err = s.git(ctx, nil, "update-ref", remoteRef, commit)
	return err
}

// pushRaced reports whether a failed push lost to a concurrent update of the
// remote branch, as opposed to failing for good (auth, network, hooks).
func pushRaced(err error) bool {
	var ge *Error
	if !errors.As(err, &ge) {
		return false
	}
	switch {
	case strings.Contains(ge.Stderr, "[rejected]"):
		return true // fetch first, non-fast-forward, stale info
	case strings.Contains(ge.Stderr, "[remote rejected]"):
		// Two receive-packs updating the same ref at once (seen with
		// file:// remotes and on GitHub under contention).
		for _, s := range []string{"lock", "failed to update ref", "incorrect old value"} {
			if strings.Contains(ge.Stderr, s) {
				return true
			}
		}
	}
	return false
}

func (s *Store) git(ctx context.Context, stdin io.Reader, args ...string) (string, error) {
	return s.gitEnv(ctx, nil, stdin, args...)
}

// gitEnv runs git on the git-dir with the remote's credentials (see auth.go).
func (s *Store) gitEnv(ctx context.Context, env []string, stdin io.Reader, args ...string) (string, error) {
	cred, err := s.credEnv()
	if err != nil {
		return "", err
	}
	out, err := run(ctx, s.GitDir, append(cred, env...), stdin, append(credArgs, args...)...)
	if err != nil {
		return "", s.classify(err)
	}
	return out, nil
}

// run executes system git with a fixed C locale (stable stderr) and no
// interactive prompts. gitDir may be empty for commands that need none.
func run(ctx context.Context, gitDir string, env []string, stdin io.Reader, args ...string) (string, error) {
	full := args
	if gitDir != "" {
		full = append([]string{"--git-dir=" + gitDir}, args...)
	}
	cmd := exec.CommandContext(ctx, "git", full...)
	cmd.Env = append(os.Environ(), "GIT_TERMINAL_PROMPT=0", "LC_ALL=C", "LANGUAGE=C")
	cmd.Env = append(cmd.Env, env...)
	cmd.Stdin = stdin
	var stdout, stderr bytes.Buffer
	cmd.Stdout, cmd.Stderr = &stdout, &stderr
	if err := cmd.Run(); err != nil {
		return "", &Error{Args: args, Stderr: strings.TrimSpace(stderr.String()), Err: err}
	}
	return stdout.String(), nil
}

// Error is a failed git invocation.
type Error struct {
	Args   []string
	Stderr string
	Err    error
}

func (e *Error) Error() string {
	sub := ""
	for i := 0; i < len(e.Args); i++ {
		if e.Args[i] == "-c" {
			i++ // skip "-c name=value"
			continue
		}
		sub = e.Args[i]
		break
	}
	if e.Stderr != "" {
		return fmt.Sprintf("git %s: %v: %s", sub, e.Err, e.Stderr)
	}
	return fmt.Sprintf("git %s: %v", sub, e.Err)
}

func (e *Error) Unwrap() error { return e.Err }
