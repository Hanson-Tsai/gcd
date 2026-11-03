package cli_test

import (
	"bytes"
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/Hanson-Tsai/gcd/internal/auth"
	"github.com/Hanson-Tsai/gcd/internal/auth/authtest"
	"github.com/Hanson-Tsai/gcd/internal/cli"
	"github.com/Hanson-Tsai/gcd/internal/registry"
)

const goodToken = "github_pat_11AAgood"

// githubWorld is an offline GitHub with private repo alice/notes, a fully
// permitted token goodToken, and an empty keychain.
type githubWorld struct {
	gh      *authtest.GitHub
	keyring *authtest.Keyring
	m       machine
}

func newGitHubWorld(t *testing.T, stdin string) *githubWorld {
	t.Helper()
	t.Setenv(auth.TokenEnv, "")
	gh := &authtest.GitHub{Tokens: map[string]authtest.Grant{
		goodToken:             {Repos: []string{"alice/notes", "alice/blog"}, ContentsWrite: true, ActionsWrite: true},
		"github_pat_readonly": {Repos: []string{"alice/notes"}},
	}}
	gh.AddRepo(t, "alice/notes", true)
	gh.AddRepo(t, "alice/blog", false)
	w := &githubWorld{gh: gh, keyring: &authtest.Keyring{}}
	cli.UseFakeGitHub(t, gh.Start(t), w.keyring, strings.NewReader(stdin))
	root := t.TempDir()
	w.m = machine{home: filepath.Join(root, "home"), folder: filepath.Join(root, "notes")}
	return w
}

// run executes gcd and returns exit code, stdout and stderr.
func (m machine) run(t *testing.T, args ...string) (int, string, string) {
	t.Helper()
	t.Setenv(registry.HomeEnv, m.home)
	var out, errOut bytes.Buffer
	code := cli.Run(context.Background(), args, &out, &errOut)
	return code, out.String(), errOut.String()
}

func registryFile(t *testing.T, m machine) string {
	b, err := os.ReadFile(filepath.Join(m.home, "registry.json"))
	if os.IsNotExist(err) {
		return ""
	}
	if err != nil {
		t.Fatal(err)
	}
	return string(b)
}

func TestInitOwnerRepoSavesPromptedTokenInKeychain(t *testing.T) {
	w := newGitHubWorld(t, goodToken+"\n")
	code, out, errOut := w.m.run(t, "init", "alice/notes", w.m.folder)
	if code != 0 {
		t.Fatalf("init: exit %d: %s", code, errOut)
	}
	if w.keyring.Len() != 1 {
		t.Errorf("keychain has %d entries, want 1", w.keyring.Len())
	}
	reg := registryFile(t, w.m)
	if !strings.Contains(reg, "/alice/notes.git") {
		t.Errorf("registry does not record the HTTPS remote:\n%s", reg)
	}
	for name, s := range map[string]string{"stdout": out, "stderr": errOut, "registry": reg} {
		if strings.Contains(s, goodToken) {
			t.Errorf("%s leaks the token: %s", name, s)
		}
	}

	// The saved token then drives git for sync, with nothing on stdin.
	if err := os.WriteFile(filepath.Join(w.m.folder, "a.txt"), []byte("hi"), 0o644); err != nil {
		t.Fatal(err)
	}
	if code, _, errOut := w.m.run(t, "sync"); code != 0 {
		t.Fatalf("sync: exit %d: %s", code, errOut)
	}
}

func TestInitWithEnvironmentTokenLeavesKeychainAlone(t *testing.T) {
	w := newGitHubWorld(t, "")
	t.Setenv(auth.TokenEnv, goodToken)
	if code, _, errOut := w.m.run(t, "init", "alice/notes", w.m.folder); code != 0 {
		t.Fatalf("init: exit %d: %s", code, errOut)
	}
	if w.keyring.Len() != 0 {
		t.Errorf("keychain has %d entries, want 0", w.keyring.Len())
	}
}

func TestInitRefusals(t *testing.T) {
	for _, tc := range []struct {
		name, repo, token string
		want              []string
	}{
		{"public repo", "alice/blog", goodToken, []string{"public"}},
		{"missing repo", "alice/nope", goodToken, []string{"not exist", "https://github.com/new"}},
		{"missing permissions", "alice/notes", "github_pat_readonly", []string{auth.PermContents, auth.PermActions}},
		{"expired token", "alice/notes", "github_pat_expired", []string{"expired", "personal-access-tokens", "gcd login alice/notes"}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			w := newGitHubWorld(t, tc.token+"\n")
			code, _, errOut := w.m.run(t, "init", tc.repo, w.m.folder)
			if code == 0 {
				t.Fatal("init succeeded")
			}
			for _, s := range tc.want {
				if !strings.Contains(errOut, s) {
					t.Errorf("stderr does not mention %q:\n%s", s, errOut)
				}
			}
			if w.keyring.Len() != 0 {
				t.Error("refused token was saved in the keychain")
			}
			if reg := registryFile(t, w.m); strings.Contains(reg, tc.repo) {
				t.Errorf("refused disk was registered:\n%s", reg)
			}
		})
	}
}

func TestExpiredTokenOnSyncSaysWhereToReplaceIt(t *testing.T) {
	w := newGitHubWorld(t, goodToken+"\n")
	if code, _, errOut := w.m.run(t, "init", "alice/notes", w.m.folder); code != 0 {
		t.Fatalf("init: exit %d: %s", code, errOut)
	}
	w.gh.Revoke(goodToken)

	code, _, errOut := w.m.run(t, "sync")
	if code == 0 {
		t.Fatal("sync with a revoked token succeeded")
	}
	for _, s := range []string{"alice/notes", "personal-access-tokens/new", "gcd login alice/notes", auth.TokenEnv} {
		if !strings.Contains(errOut, s) {
			t.Errorf("stderr does not mention %q:\n%s", s, errOut)
		}
	}
	if strings.Contains(errOut, goodToken) {
		t.Errorf("stderr leaks the token:\n%s", errOut)
	}
}

func TestLoginReplacesKeychainToken(t *testing.T) {
	w := newGitHubWorld(t, goodToken+"\n")
	if err := (auth.Tokens{Keyring: w.keyring, Getenv: os.Getenv}).Store(auth.Repo{Owner: "alice", Name: "notes"}, "github_pat_old"); err != nil {
		t.Fatal(err)
	}
	if code, _, errOut := w.m.run(t, "login", "alice/notes"); code != 0 {
		t.Fatalf("login: exit %d: %s", code, errOut)
	}
	tok, err := (auth.Tokens{Keyring: w.keyring, Getenv: os.Getenv}).Lookup(auth.Repo{Owner: "alice", Name: "notes"})
	if err != nil || tok != goodToken {
		t.Fatalf("keychain token = %q, %v; want the new one", tok, err)
	}
}
