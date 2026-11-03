package cli_test

import (
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/Hanson-Tsai/gcd/internal/fixture"
)

func TestCompactSmoke(t *testing.T) {
	remote := fixture.NewRemote(t)
	now := time.Now()
	remote.CommitAt(t, fixture.CommitSpec{Time: fixture.Ago(now, 90), Files: map[string]string{"a.txt": "1"}})
	remote.CommitAt(t, fixture.CommitSpec{Time: fixture.Ago(now, 60), Files: map[string]string{"a.txt": "2"}})
	root := t.TempDir()
	a := machine{home: filepath.Join(root, "a-home"), folder: filepath.Join(root, "a-folder")}
	a.gcd(t, "init", remote.URL, a.folder) // commits the repo config now
	a.gcd(t, "sync")

	for _, args := range [][]string{{"compact"}, {"compact", "--local", "--run"}} {
		if msg := a.gcdFails(t, args...); !strings.Contains(msg, "usage") {
			t.Errorf("gcd %v: stderr = %q, want usage", args, msg)
		}
	}

	out := a.gcd(t, "compact", "--local")
	if !strings.Contains(out, "2 commits squashed") || !strings.Contains(out, "1 commit kept") {
		t.Errorf("gcd compact --local = %q, want 2 squashed and 1 kept", out)
	}
	if n := len(remote.History(t)); n != 2 {
		t.Errorf("history has %d commits after compaction, want 2", n)
	}

	// --run takes the repo from the GitHub Actions environment by default.
	t.Setenv("GITHUB_SERVER_URL", fixture.FileURL(filepath.Dir(remote.Dir)))
	t.Setenv("GITHUB_REPOSITORY", strings.TrimSuffix(filepath.Base(remote.Dir), ".git"))
	if out := a.gcd(t, "compact", "--run"); !strings.Contains(out, "nothing to compact") {
		t.Errorf("gcd compact --run = %q, want nothing to compact", out)
	}
	if out := a.gcd(t, "sync"); !strings.Contains(out, "up to date") {
		t.Errorf("sync after compaction = %q, want up to date", out)
	}
}
