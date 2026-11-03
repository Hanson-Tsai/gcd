package fixture

import (
	"strconv"
	"strings"
	"testing"
	"time"
)

// CommitSpec describes a commit planted on the remote with CommitAt. It is
// the Seam B fixture: tests build a history whose commit times they choose.
type CommitSpec struct {
	Time    time.Time         // author and committer time (keeps its zone offset)
	Host    string            // author and committer name; email <host>@gcd.invalid; default "other"
	Files   map[string]string // slash-separated path -> content, added or replaced
	Delete  []string          // paths removed
	Message string            // default "sync from <host>"
}

// CommitAt commits spec on top of the remote's main branch, as another client
// at spec.Time would have, and returns the new commit hash.
func (r *Remote) CommitAt(t testing.TB, spec CommitSpec) string {
	t.Helper()
	if spec.Host == "" {
		spec.Host = "other"
	}
	if spec.Message == "" {
		spec.Message = "sync from " + spec.Host
	}
	head := r.Head(t)
	tree := r.tree(t, head, spec.Files, nil, spec.Delete)
	args := []string{"commit-tree", tree}
	if head != "" {
		args = append(args, "-p", head)
	}
	when := spec.Time
	if when.IsZero() {
		when = time.Now()
	}
	// "@<unix> <zone>" is the only raw date form git 2.30 accepts for any time.
	date := "@" + strconv.FormatInt(when.Unix(), 10) + " " + when.Format("-0700")
	sig := []string{
		"GIT_AUTHOR_NAME=" + spec.Host, "GIT_AUTHOR_EMAIL=" + spec.Host + "@gcd.invalid", "GIT_AUTHOR_DATE=" + date,
		"GIT_COMMITTER_NAME=" + spec.Host, "GIT_COMMITTER_EMAIL=" + spec.Host + "@gcd.invalid", "GIT_COMMITTER_DATE=" + date,
	}
	commit := strings.TrimSpace(r.gitEnv(t, sig, spec.Message, args...))
	r.gitEnv(t, nil, "", "update-ref", "refs/heads/main", commit)
	return commit
}

// HistoryCommit is one commit of the remote's main branch with the fields
// compaction must keep.
type HistoryCommit struct {
	Hash          string
	Tree          string
	Parents       []string
	Author        string // "name <email>"
	AuthorTime    time.Time
	AuthorZone    string // e.g. "+0800"
	Committer     string // "name <email>"
	CommitterTime time.Time
	Message       string // full raw message
}

// History returns the remote's main-branch commits (first parent), newest
// first. An empty remote yields nil.
func (r *Remote) History(t testing.TB) []HistoryCommit {
	t.Helper()
	if r.Head(t) == "" {
		return nil
	}
	out := git(t, r.Dir, "log", "-z", "--first-parent", "--date=raw",
		"--format=%H%x1f%T%x1f%P%x1f%an <%ae>%x1f%ad%x1f%cn <%ce>%x1f%ct%x1f%B", "refs/heads/main")
	var commits []HistoryCommit
	for _, rec := range strings.Split(out, "\x00") {
		if rec == "" {
			continue
		}
		f := strings.SplitN(rec, "\x1f", 8)
		if len(f) != 8 {
			t.Fatalf("unexpected git log record %q", rec)
		}
		ad := strings.Fields(f[4])
		c := HistoryCommit{
			Hash: f[0], Tree: f[1], Parents: strings.Fields(f[2]),
			Author: f[3], AuthorTime: unix(t, ad[0]), AuthorZone: ad[1],
			Committer: f[5], CommitterTime: unix(t, f[6]), Message: f[7],
		}
		commits = append(commits, c)
	}
	return commits
}

func unix(t testing.TB, s string) time.Time {
	t.Helper()
	n, err := strconv.ParseInt(s, 10, 64)
	if err != nil {
		t.Fatalf("bad unix time %q", s)
	}
	return time.Unix(n, 0)
}

// Ago returns now minus days whole days; a shorthand for commit times.
func Ago(now time.Time, days int) time.Time {
	return now.Add(-time.Duration(days) * 24 * time.Hour)
}
