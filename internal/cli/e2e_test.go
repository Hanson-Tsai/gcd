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

// TestE2EWorkflowUpgradeAndDispatch pins the compaction workflow in the real
// repo to a published gcd release and starts it. It does not wait for the
// run: check the printed run page (docs/manual-verification.md, #15).
// Needs GCD_E2E_GCD_VERSION, a published release (e.g. 0.1.0), because test
// binaries are development builds.
func TestE2EWorkflowUpgradeAndDispatch(t *testing.T) {
	repo := e2eRepo(t)
	ver := os.Getenv("GCD_E2E_GCD_VERSION")
	if ver == "" {
		t.Skip("set GCD_E2E_GCD_VERSION=<x.y.z> (a published gcd release) to pin the workflow")
	}
	root := t.TempDir()
	a := machine{home: filepath.Join(root, "a-home"), folder: filepath.Join(root, "a-disk")}
	a.gcd(t, "init", repo, a.folder)

	if out := a.gcd(t, "workflow", "upgrade", "--version", ver); !strings.Contains(out, ver) {
		t.Errorf("workflow upgrade = %q, want version %s", out, ver)
	}
	out := a.gcd(t, "compact")
	if !strings.Contains(out, "https://github.com/"+repo+"/actions/") {
		t.Errorf("gcd compact = %q, want a link to the run on github.com", out)
	}
	t.Log(out)
}
