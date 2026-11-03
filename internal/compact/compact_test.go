package compact_test

import (
	"context"
	"errors"
	"path/filepath"
	"testing"
	"time"

	"github.com/Hanson-Tsai/gcd/internal/compact"
	"github.com/Hanson-Tsai/gcd/internal/config"
	"github.com/Hanson-Tsai/gcd/internal/fixture"
)

// now is the fixed "current time" of these tests; the zone checks that
// compaction keeps each commit's own offset.
var now = time.Date(2026, 11, 3, 12, 0, 0, 0, time.UTC)

func run(t *testing.T, remote *fixture.Remote, retention int) compact.Result {
	t.Helper()
	res, err := compact.Run(context.Background(), compact.Options{
		GitDir:        filepath.Join(t.TempDir(), "git"),
		Remote:        remote.URL,
		RetentionDays: retention,
		Now:           now,
	})
	if err != nil {
		t.Fatalf("compact: %v", err)
	}
	return res
}

func TestCompactSquashesHistoryBeforeRetention(t *testing.T) {
	remote := fixture.NewRemote(t)
	taipei := time.FixedZone("", 8*3600)
	remote.CommitAt(t, fixture.CommitSpec{Time: fixture.Ago(now, 90), Host: "mbp", Files: map[string]string{"a.txt": "a1", "old.txt": "gone soon"}})
	remote.CommitAt(t, fixture.CommitSpec{Time: fixture.Ago(now, 60), Host: "linux", Files: map[string]string{"a.txt": "a2"}, Delete: []string{"old.txt"}})
	cut := remote.CommitAt(t, fixture.CommitSpec{Time: fixture.Ago(now, 40).In(taipei), Host: "win", Files: map[string]string{"b.txt": "b1"}})
	remote.CommitAt(t, fixture.CommitSpec{Time: fixture.Ago(now, 20).In(taipei), Host: "mbp", Files: map[string]string{"a.txt": "a3"}, Message: "sync from mbp\n\nsecond paragraph\n"})
	remote.CommitAt(t, fixture.CommitSpec{Time: fixture.Ago(now, 1), Host: "linux", Delete: []string{"b.txt"}, Files: map[string]string{"c/d.txt": "d"}})
	before := remote.History(t)
	cutCommit := before[2]
	if cutCommit.Hash != cut {
		t.Fatalf("fixture: history[2] = %s, want the cut point %s", cutCommit.Hash, cut)
	}

	res := run(t, remote, 30)

	after := remote.History(t)
	if !res.Compacted || res.Squashed != 3 || res.Kept != 2 {
		t.Fatalf("result = %+v, want compacted with 3 squashed and 2 kept", res)
	}
	if len(after) != 3 {
		t.Fatalf("history after = %d commits, want root + 2 kept", len(after))
	}
	if after[0].Tree != before[0].Tree {
		t.Errorf("newest tree changed: %s -> %s", before[0].Tree, after[0].Tree)
	}
	if res.OldHead != before[0].Hash || res.NewHead != after[0].Hash || remote.Head(t) != res.NewHead {
		t.Errorf("result heads = %s -> %s, remote %s; want %s -> %s", res.OldHead, res.NewHead, remote.Head(t), before[0].Hash, after[0].Hash)
	}

	root := after[2]
	if res.NewRoot != root.Hash || len(root.Parents) != 0 {
		t.Errorf("root %s (parents %v), result root %s; want a parentless root", root.Hash, root.Parents, res.NewRoot)
	}
	if root.Tree != cutCommit.Tree {
		t.Errorf("root tree = %s, want the cut point's tree %s", root.Tree, cutCommit.Tree)
	}
	// gcd restore's retention boundary is the oldest commit's author time.
	if !root.AuthorTime.Equal(cutCommit.AuthorTime) || root.AuthorZone != "+0800" || root.Author != cutCommit.Author {
		t.Errorf("root author = %s %v %s, want the cut point's %s %v +0800",
			root.Author, root.AuthorTime, root.AuthorZone, cutCommit.Author, cutCommit.AuthorTime)
	}

	for i := 0; i < 2; i++ {
		b, a := before[i], after[i]
		if a.Tree != b.Tree || a.Author != b.Author || !a.AuthorTime.Equal(b.AuthorTime) || a.AuthorZone != b.AuthorZone ||
			a.Committer != b.Committer || !a.CommitterTime.Equal(b.CommitterTime) || a.Message != b.Message {
			t.Errorf("kept commit %d changed:\n before %+v\n after  %+v", i, b, a)
		}
		if len(a.Parents) != 1 || a.Parents[0] != after[i+1].Hash {
			t.Errorf("kept commit %d parents = %v, want [%s]", i, a.Parents, after[i+1].Hash)
		}
	}
	remote.AssertFiles(t, map[string]string{"a.txt": "a3", "c/d.txt": "d"})
}

func TestCompactDoesNothingWithoutHistoryBeforeRetention(t *testing.T) {
	t.Run("empty remote", func(t *testing.T) {
		remote := fixture.NewRemote(t)
		if res := run(t, remote, 30); res.Compacted || remote.Head(t) != "" {
			t.Fatalf("result = %+v, remote head %q; want nothing done", res, remote.Head(t))
		}
	})
	t.Run("all commits within retention", func(t *testing.T) {
		remote := fixture.NewRemote(t)
		remote.CommitAt(t, fixture.CommitSpec{Time: fixture.Ago(now, 29), Files: map[string]string{"a": "1"}})
		head := remote.CommitAt(t, fixture.CommitSpec{Time: fixture.Ago(now, 2), Files: map[string]string{"a": "2"}})
		if res := run(t, remote, 30); res.Compacted || res.NewHead != head || remote.Head(t) != head {
			t.Fatalf("result = %+v, remote head %s; want nothing done at %s", res, remote.Head(t), head)
		}
	})
	t.Run("only the root is older", func(t *testing.T) {
		remote := fixture.NewRemote(t)
		remote.CommitAt(t, fixture.CommitSpec{Time: fixture.Ago(now, 100), Files: map[string]string{"a": "1"}})
		head := remote.CommitAt(t, fixture.CommitSpec{Time: fixture.Ago(now, 2), Files: map[string]string{"a": "2"}})
		if res := run(t, remote, 30); res.Compacted || remote.Head(t) != head {
			t.Fatalf("result = %+v; want nothing done", res)
		}
	})
	t.Run("second run after compacting", func(t *testing.T) {
		remote := fixture.NewRemote(t)
		remote.CommitAt(t, fixture.CommitSpec{Time: fixture.Ago(now, 100), Files: map[string]string{"a": "1"}})
		remote.CommitAt(t, fixture.CommitSpec{Time: fixture.Ago(now, 50), Files: map[string]string{"a": "2"}})
		remote.CommitAt(t, fixture.CommitSpec{Time: fixture.Ago(now, 2), Files: map[string]string{"a": "3"}})
		first := run(t, remote, 30)
		if !first.Compacted {
			t.Fatalf("first run = %+v, want compacted", first)
		}
		if second := run(t, remote, 30); second.Compacted || remote.Head(t) != first.NewHead {
			t.Fatalf("second run = %+v, remote head %s; want nothing done at %s", second, remote.Head(t), first.NewHead)
		}
	})
}

func TestCompactAllHistoryOlderThanRetentionLeavesOneSnapshot(t *testing.T) {
	remote := fixture.NewRemote(t)
	remote.CommitAt(t, fixture.CommitSpec{Time: fixture.Ago(now, 100), Files: map[string]string{"a": "1", "b": "1"}})
	remote.CommitAt(t, fixture.CommitSpec{Time: fixture.Ago(now, 90), Files: map[string]string{"a": "2"}, Delete: []string{"b"}})
	before := remote.History(t)

	res := run(t, remote, 30)

	after := remote.History(t)
	if !res.Compacted || res.Squashed != 2 || res.Kept != 0 || len(after) != 1 {
		t.Fatalf("result = %+v, history %d commits; want one snapshot", res, len(after))
	}
	if after[0].Tree != before[0].Tree || !after[0].AuthorTime.Equal(before[0].AuthorTime) || len(after[0].Parents) != 0 {
		t.Errorf("snapshot = %+v, want the old head's tree and time with no parent", after[0])
	}
	remote.AssertFiles(t, map[string]string{"a": "2"})
}

func TestCompactKeepsEveryCommitDatedInsideRetention(t *testing.T) {
	// A machine with a fast clock committed "in retention" before one with a
	// slow clock: history from the first in-retention commit on is kept.
	remote := fixture.NewRemote(t)
	remote.CommitAt(t, fixture.CommitSpec{Time: fixture.Ago(now, 100), Files: map[string]string{"a": "1"}})
	remote.CommitAt(t, fixture.CommitSpec{Time: fixture.Ago(now, 80), Files: map[string]string{"a": "2"}})
	remote.CommitAt(t, fixture.CommitSpec{Time: fixture.Ago(now, 10), Host: "fast", Files: map[string]string{"a": "3"}})
	remote.CommitAt(t, fixture.CommitSpec{Time: fixture.Ago(now, 40), Host: "slow", Files: map[string]string{"a": "4"}})
	remote.CommitAt(t, fixture.CommitSpec{Time: fixture.Ago(now, 5), Files: map[string]string{"a": "5"}})

	res := run(t, remote, 30)

	after := remote.History(t)
	if !res.Compacted || res.Squashed != 2 || res.Kept != 3 || len(after) != 4 {
		t.Fatalf("result = %+v, history %d commits; want 2 squashed, 3 kept", res, len(after))
	}
	if after[2].Author != "fast <fast@gcd.invalid>" || after[1].Author != "slow <slow@gcd.invalid>" {
		t.Errorf("kept authors = %q, %q; want fast then slow", after[2].Author, after[1].Author)
	}
}

func TestCompactReadsRetentionFromRepoConfig(t *testing.T) {
	remote := fixture.NewRemote(t)
	remote.CommitAt(t, fixture.CommitSpec{Time: fixture.Ago(now, 30), Files: map[string]string{".gcd/config.toml": "retention_days = 7\n", "a": "1"}})
	remote.CommitAt(t, fixture.CommitSpec{Time: fixture.Ago(now, 20), Files: map[string]string{"a": "2"}})
	remote.CommitAt(t, fixture.CommitSpec{Time: fixture.Ago(now, 3), Files: map[string]string{"a": "3"}})

	res := run(t, remote, 0)

	if !res.Compacted || res.RetentionDays != 7 || res.Squashed != 2 || res.Kept != 1 {
		t.Fatalf("result = %+v, want retention 7 days: 2 squashed, 1 kept", res)
	}
	if want := fixture.Ago(now, 7); !res.Cutoff.Equal(want) {
		t.Errorf("cutoff = %v, want %v", res.Cutoff, want)
	}
}

func TestCompactRefusesNewerRepoFormat(t *testing.T) {
	remote := fixture.NewRemote(t)
	remote.CommitAt(t, fixture.CommitSpec{Time: fixture.Ago(now, 100), Files: map[string]string{"a": "1"}})
	head := remote.CommitAt(t, fixture.CommitSpec{Time: fixture.Ago(now, 90), Files: map[string]string{".gcd/config.toml": "format = 99\n"}})

	_, err := compact.Run(context.Background(), compact.Options{
		GitDir: filepath.Join(t.TempDir(), "git"), Remote: remote.URL, RetentionDays: 30, Now: now,
	})
	if !errors.Is(err, config.ErrFormatTooNew) {
		t.Fatalf("err = %v, want ErrFormatTooNew", err)
	}
	if remote.Head(t) != head {
		t.Errorf("remote head moved to %s", remote.Head(t))
	}
}

func TestCompactFailsWithoutOverwritingAConcurrentPush(t *testing.T) {
	remote := fixture.NewRemote(t)
	remote.CommitAt(t, fixture.CommitSpec{Time: fixture.Ago(now, 100), Files: map[string]string{"a": "1"}})
	remote.CommitAt(t, fixture.CommitSpec{Time: fixture.Ago(now, 50), Files: map[string]string{"a": "2"}})
	remote.CommitAt(t, fixture.CommitSpec{Time: fixture.Ago(now, 2), Files: map[string]string{"a": "3"}})
	var pushed string
	opts := compact.WithBeforePush(compact.Options{
		GitDir: filepath.Join(t.TempDir(), "git"), Remote: remote.URL, RetentionDays: 30, Now: now,
	}, func() {
		pushed = remote.CommitAt(t, fixture.CommitSpec{Time: now, Host: "laptop", Files: map[string]string{"new.txt": "meanwhile"}})
	})

	res, err := compact.Run(context.Background(), opts)

	if !errors.Is(err, compact.ErrLeaseFailed) || !res.LeaseFailed || res.Compacted {
		t.Fatalf("result = %+v, err = %v; want a lease failure", res, err)
	}
	if remote.Head(t) != pushed {
		t.Fatalf("remote head = %s, want the concurrent push %s kept", remote.Head(t), pushed)
	}
	if n := len(remote.History(t)); n != 4 {
		t.Errorf("history has %d commits, want the 4 original ones", n)
	}
}
