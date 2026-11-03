package cli_test

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/Hanson-Tsai/gcd/internal/auth"
)

// e2eRepo returns the real private GitHub repo for end-to-end tests, or skips.
// It runs only when triggered by hand (the E2E workflow or a developer), never
// in normal CI. GCD_TOKEN is set, so the OS keychain is never written.
func e2eRepo(t *testing.T) string {
	repo := os.Getenv("GCD_E2E_REPO")
	if repo == "" || os.Getenv(auth.TokenEnv) == "" {
		t.Skip("set GCD_E2E_REPO=<owner/repo> (a private throwaway repo) and GCD_TOKEN to run against real GitHub")
	}
	return repo
}

// TestE2EInitAndSyncAgainstGitHub: two machines bind the same real repo
// with a PAT and exchange a file over HTTPS; then an invalid token on any
// command says where to get a new one.
func TestE2EInitAndSyncAgainstGitHub(t *testing.T) {
	repo := e2eRepo(t)
	root := t.TempDir()
	a := machine{home: filepath.Join(root, "a-home"), folder: filepath.Join(root, "a-disk")}
	b := machine{home: filepath.Join(root, "b-home"), folder: filepath.Join(root, "b-disk")}

	a.gcd(t, "init", repo, a.folder)
	b.gcd(t, "init", repo, b.folder)

	name := fmt.Sprintf("e2e-%d.txt", time.Now().UnixNano())
	if err := os.WriteFile(filepath.Join(a.folder, name), []byte("hello from e2e"), 0o644); err != nil {
		t.Fatal(err)
	}
	a.gcd(t, "sync")
	b.gcd(t, "sync")
	if got, err := os.ReadFile(filepath.Join(b.folder, name)); err != nil || string(got) != "hello from e2e" {
		t.Fatalf("b/%s = %q, %v", name, got, err)
	}
	// Clean up the remote file.
	if err := os.Remove(filepath.Join(b.folder, name)); err != nil {
		t.Fatal(err)
	}
	b.gcd(t, "sync")

	t.Setenv(auth.TokenEnv, "github_pat_11AAAAAAA0invalidinvalidinvalid")
	code, _, errOut := a.run(t, "sync")
	if code == 0 {
		t.Fatal("sync with an invalid token succeeded")
	}
	for _, s := range []string{"personal-access-tokens/new", "gcd login " + repo} {
		if !strings.Contains(errOut, s) {
			t.Errorf("stderr does not mention %q:\n%s", s, errOut)
		}
	}
	if strings.Contains(errOut, "invalidinvalid") {
		t.Errorf("stderr leaks the token:\n%s", errOut)
	}
}
