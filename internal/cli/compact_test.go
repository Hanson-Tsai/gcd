package cli_test

import (
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/Hanson-Tsai/gcd/internal/auth"
	"github.com/Hanson-Tsai/gcd/internal/auth/authtest"
	"github.com/Hanson-Tsai/gcd/internal/cli"
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

// The Actions job: no registry, no keychain, repo from GITHUB_* and the
// token from GCD_TOKEN, over HTTPS (a fake GitHub serving a real repo).
func TestCompactRunAsInActions(t *testing.T) {
	fake := &authtest.GitHub{Tokens: map[string]authtest.Grant{
		goodToken: {Repos: []string{"alice/notes"}, ContentsWrite: true},
	}}
	remote := &fixture.Remote{Dir: fake.AddRepo(t, "alice/notes", true)}
	now := time.Now()
	remote.CommitAt(t, fixture.CommitSpec{Time: fixture.Ago(now, 90), Files: map[string]string{"a.txt": "1"}})
	remote.CommitAt(t, fixture.CommitSpec{Time: fixture.Ago(now, 60), Files: map[string]string{"a.txt": "2"}})
	remote.CommitAt(t, fixture.CommitSpec{Time: fixture.Ago(now, 1), Files: map[string]string{"a.txt": "3"}})
	gh := fake.Start(t)
	cli.UseFakeGitHub(t, gh, &authtest.Keyring{}, strings.NewReader(""))
	t.Setenv("GITHUB_SERVER_URL", gh.WebURL)
	t.Setenv("GITHUB_REPOSITORY", "alice/notes")
	m := machine{home: filepath.Join(t.TempDir(), "runner")}

	t.Setenv(auth.TokenEnv, "")
	if code, _, errOut := m.run(t, "compact", "--run"); code == 0 || !strings.Contains(errOut, auth.TokenEnv) {
		t.Errorf("compact --run without a token: exit %d, stderr %q; want an error naming %s", code, errOut, auth.TokenEnv)
	}

	t.Setenv(auth.TokenEnv, goodToken)
	code, out, errOut := m.run(t, "compact", "--run")
	if code != 0 || !strings.Contains(out, "2 commits squashed") {
		t.Fatalf("compact --run: exit %d, stdout %q, stderr %q; want 2 squashed", code, out, errOut)
	}
	if strings.Contains(out+errOut, goodToken) {
		t.Errorf("output leaks the token: %s%s", out, errOut)
	}
	if n := len(remote.History(t)); n != 2 {
		t.Errorf("history has %d commits after compaction, want 2", n)
	}
}
