package gitstore

import (
	"context"
	"fmt"
	"strconv"
	"strings"
	"time"
)

// FileChange is one path touched by a commit, compared with its first parent
// (or with the empty tree for a root commit).
type FileChange struct {
	Status byte // 'A' added, 'M' modified, 'D' deleted, 'T' type changed
	Path   string
}

// CommitInfo describes one commit in the history of the remote branch.
type CommitInfo struct {
	Hash    string
	Tree    string
	Author  string    // author name (gcd writes the host name)
	Time    time.Time // author time; compaction keeps it
	Subject string
	Changes []FileChange
}

// History lists the first-parent history of commit, newest first, with the
// files each commit changed. A non-empty path limits the history to commits
// that touched path (a file, or a directory prefix) and limits Changes to it.
// path is matched literally (no glob magic). An empty commit yields nil.
func (s *Store) History(ctx context.Context, commit, path string) ([]CommitInfo, error) {
	if commit == "" {
		return nil, nil
	}
	args := []string{"log", "-z", "--first-parent", "--no-renames", "--name-status", "--root",
		"--format=%x01%H%x1f%T%x1f%an%x1f%at%x1f%s", commit, "--"}
	if path != "" {
		args = append(args, path)
	}
	out, err := s.gitEnv(ctx, []string{"GIT_LITERAL_PATHSPECS=1"}, nil, args...)
	if err != nil {
		return nil, err
	}
	var commits []CommitInfo
	for _, chunk := range strings.Split(out, "\x01") {
		if chunk == "" {
			continue
		}
		c, err := parseLogChunk(chunk)
		if err != nil {
			return nil, err
		}
		commits = append(commits, c)
	}
	return commits, nil
}

// parseLogChunk parses one commit of `git log -z --name-status` output: the
// formatted header, then NUL-separated status/path pairs.
func parseLogChunk(chunk string) (CommitInfo, error) {
	end := strings.IndexAny(chunk, "\n\x00")
	if end < 0 {
		end = len(chunk)
	}
	f := strings.Split(chunk[:end], "\x1f")
	if len(f) != 5 {
		return CommitInfo{}, fmt.Errorf("unexpected git log header %q", chunk[:end])
	}
	secs, err := strconv.ParseInt(f[3], 10, 64)
	if err != nil {
		return CommitInfo{}, fmt.Errorf("unexpected git log time %q", f[3])
	}
	c := CommitInfo{Hash: f[0], Tree: f[1], Author: f[2], Time: time.Unix(secs, 0), Subject: f[4]}
	rest := strings.TrimLeft(chunk[end:], "\n\x00")
	var toks []string
	for _, t := range strings.Split(rest, "\x00") {
		if t != "" {
			toks = append(toks, t)
		}
	}
	if len(toks)%2 != 0 {
		return CommitInfo{}, fmt.Errorf("unexpected git log changes for %s: %q", c.Hash, rest)
	}
	for i := 0; i < len(toks); i += 2 {
		status := strings.TrimSpace(toks[i])
		if status == "" {
			return CommitInfo{}, fmt.Errorf("unexpected git log change status in %s", c.Hash)
		}
		c.Changes = append(c.Changes, FileChange{Status: status[0], Path: toks[i+1]})
	}
	return c, nil
}

// ResolveCommit expands a (possibly abbreviated) commit hash to the full
// hash. It returns "" without error when no such commit exists.
func (s *Store) ResolveCommit(ctx context.Context, rev string) (string, error) {
	if rev == "" || strings.HasPrefix(rev, "-") {
		return "", nil
	}
	return s.resolve(ctx, rev+"^{commit}")
}
