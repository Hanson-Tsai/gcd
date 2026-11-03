package cli_test

import (
	"crypto/sha256"
	"encoding/hex"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/Hanson-Tsai/gcd/internal/auth"
	"github.com/Hanson-Tsai/gcd/internal/auth/authtest"
	"github.com/Hanson-Tsai/gcd/internal/cli"
	"github.com/Hanson-Tsai/gcd/internal/config"
	"github.com/Hanson-Tsai/gcd/internal/fixture"
	"github.com/Hanson-Tsai/gcd/internal/workflow"
)

const (
	noWorkflowsToken = "github_pat_noworkflows"
	noActionsToken   = "github_pat_noactions"
	bareToken        = "github_pat_contentsonly" // neither Workflows nor Actions
)

// releaseSum is the fake SHA-256 of release v's linux/amd64 archive.
func releaseSum(v string) string {
	h := sha256.Sum256([]byte("gcd " + v))
	return hex.EncodeToString(h[:])
}

// fakeReleases serves checksums.txt for the given gcd release versions.
func fakeReleases(t *testing.T, versions ...string) workflow.Releases {
	t.Helper()
	sums := map[string]string{}
	for _, v := range versions {
		sums["/releases/download/v"+v+"/checksums.txt"] = strings.Repeat("0", 64) + "  gcd_" + v + "_darwin_arm64.tar.gz\n" +
			releaseSum(v) + "  gcd_" + v + "_linux_amd64.tar.gz\n"
	}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		s, ok := sums[r.URL.Path]
		if !ok {
			http.NotFound(w, r)
			return
		}
		w.Write([]byte(s))
	}))
	t.Cleanup(srv.Close)
	return workflow.Releases{BaseURL: srv.URL + "/releases/download", HTTP: srv.Client()}
}

func (w *githubWorld) remote() *fixture.Remote {
	return &fixture.Remote{Dir: filepath.Join(w.gh.GitRoot, "alice", "notes.git")}
}

// remoteWorkflow returns the parsed compaction workflow on the remote, and
// whether there is one.
func remoteWorkflow(t *testing.T, r *fixture.Remote) (workflow.Spec, bool) {
	t.Helper()
	if exec.Command("git", "--git-dir="+r.Dir, "rev-parse", "--verify", "--quiet", "refs/heads/main").Run() != nil {
		return workflow.Spec{}, false // empty repo
	}
	raw, ok := r.Files(t)[workflow.Path]
	if !ok {
		return workflow.Spec{}, false
	}
	spec, err := workflow.Parse([]byte(raw))
	if err != nil {
		t.Fatal(err)
	}
	return spec, true
}

func TestInitInstallsThePinnedWorkflowAndCompactDispatchesIt(t *testing.T) {
	w := newGitHubWorld(t, goodToken+"\n")
	cli.UseReleases(t, fakeReleases(t, "1.2.3"), "1.2.3")

	code, out, errOut := w.m.run(t, "init", "alice/notes", w.m.folder)
	if code != 0 {
		t.Fatalf("init: exit %d: %s", code, errOut)
	}
	if !strings.Contains(out, workflow.Path) || !strings.Contains(out, "1.2.3") {
		t.Errorf("init output does not report the workflow:\n%s", out)
	}
	spec, ok := remoteWorkflow(t, w.remote())
	if !ok {
		t.Fatal("init did not install the workflow")
	}
	if spec.Pin.Version != "1.2.3" || spec.Pin.SHA256 != releaseSum("1.2.3") ||
		!strings.HasSuffix(spec.Pin.URL, "/v1.2.3/gcd_1.2.3_linux_amd64.tar.gz") {
		t.Errorf("workflow pin = %+v, want release 1.2.3's linux/amd64 archive and checksum", spec.Pin)
	}
	if spec.Schedule != config.Default().CompactSchedule {
		t.Errorf("workflow schedule = %q, want the repo config's %q", spec.Schedule, config.Default().CompactSchedule)
	}

	code, out, errOut = w.m.run(t, "compact")
	if code != 0 {
		t.Fatalf("compact: exit %d: %s", code, errOut)
	}
	if !strings.Contains(out, "/alice/notes/actions/runs/1") {
		t.Errorf("compact does not link the run page:\n%s", out)
	}
	want := authtest.Dispatch{Repo: "alice/notes", Workflow: workflow.FileName, Ref: "main"}
	if got := w.gh.Dispatches(); len(got) != 1 || got[0] != want {
		t.Errorf("dispatches = %+v, want %+v", got, want)
	}

	if code, _, errOut := w.m.run(t, "sync"); code != 0 {
		t.Fatalf("sync: exit %d: %s", code, errOut)
	}
	if _, err := os.Stat(filepath.Join(w.m.folder, ".github")); !os.IsNotExist(err) {
		t.Errorf(".github appeared in the synced folder (err %v)", err)
	}
}

// Installing the workflow is init's probe for the Workflows permission: a
// token without it binds nothing.
func TestInitWithoutWorkflowsPermissionBindsNothing(t *testing.T) {
	w := newGitHubWorld(t, noWorkflowsToken+"\n")
	cli.UseReleases(t, fakeReleases(t, "1.2.3"), "1.2.3")

	code, _, errOut := w.m.run(t, "init", "alice/notes", w.m.folder)
	if code == 0 {
		t.Fatal("init succeeded")
	}
	// Reported like the permissions Check finds missing.
	for _, s := range []string{"missing permissions on alice/notes: " + auth.PermWorkflows, "personal-access-tokens/new?"} {
		if !strings.Contains(errOut, s) {
			t.Errorf("stderr does not mention %q:\n%s", s, errOut)
		}
	}
	if reg := registryFile(t, w.m); strings.Contains(reg, "alice/notes") {
		t.Errorf("disk was registered:\n%s", reg)
	}
	if w.keyring.Len() != 0 {
		t.Error("token was saved in the keychain")
	}
	if _, ok := remoteWorkflow(t, w.remote()); ok {
		t.Error("workflow was installed")
	}
}

// A refused workflow push lists every missing permission at once, so the
// token is fixed in one go.
func TestInitWithoutWorkflowsOrActionsListsBoth(t *testing.T) {
	w := newGitHubWorld(t, bareToken+"\n")
	cli.UseReleases(t, fakeReleases(t, "1.2.3"), "1.2.3")

	code, _, errOut := w.m.run(t, "init", "alice/notes", w.m.folder)
	if code == 0 {
		t.Fatal("init succeeded")
	}
	if want := "missing permissions on alice/notes: " + auth.PermWorkflows + ", " + auth.PermActions; !strings.Contains(errOut, want) {
		t.Errorf("stderr does not mention %q:\n%s", want, errOut)
	}
}

// Actions write is only needed to start compaction on GitHub (`gcd
// compact`): init binds the disk and warns, and `gcd compact` says what is
// missing.
func TestInitWithoutActionsPermissionWarnsAndCompactExplains(t *testing.T) {
	w := newGitHubWorld(t, noActionsToken+"\n")
	cli.UseReleases(t, fakeReleases(t, "1.2.3"), "1.2.3")

	code, _, errOut := w.m.run(t, "init", "alice/notes", w.m.folder)
	if code != 0 {
		t.Fatalf("init: exit %d: %s", code, errOut)
	}
	for _, s := range []string{"warning", auth.PermActions, "gcd compact"} {
		if !strings.Contains(errOut, s) {
			t.Errorf("init stderr does not mention %q:\n%s", s, errOut)
		}
	}
	if !strings.Contains(registryFile(t, w.m), "alice/notes") {
		t.Error("disk was not registered")
	}
	if _, ok := remoteWorkflow(t, w.remote()); !ok {
		t.Error("workflow was not installed")
	}

	code, _, errOut = w.m.run(t, "compact")
	if code == 0 {
		t.Fatal("compact succeeded without Actions permission")
	}
	for _, s := range []string{"missing permissions on alice/notes: " + auth.PermActions, "gcd compact --local"} {
		if !strings.Contains(errOut, s) {
			t.Errorf("compact stderr does not mention %q:\n%s", s, errOut)
		}
	}
	if len(w.gh.Dispatches()) != 0 {
		t.Error("a workflow run started")
	}
}

func TestDevBuildInitSkipsTheWorkflowAndUpgradeNeedsAVersion(t *testing.T) {
	w := newGitHubWorld(t, goodToken+"\n")
	cli.UseReleases(t, fakeReleases(t, "1.2.3"), "dev")

	code, out, errOut := w.m.run(t, "init", "alice/notes", w.m.folder)
	if code != 0 {
		t.Fatalf("init: exit %d: %s", code, errOut)
	}
	if !strings.Contains(out+errOut, "gcd workflow upgrade --version") {
		t.Errorf("init does not say how to install the workflow:\n%s%s", out, errOut)
	}
	if _, ok := remoteWorkflow(t, w.remote()); ok {
		t.Error("a dev build installed the workflow")
	}

	code, _, errOut = w.m.run(t, "workflow", "upgrade")
	if code == 0 || !strings.Contains(errOut, "--version") {
		t.Errorf("upgrade on a dev build: exit %d, stderr %q; want a refusal suggesting --version", code, errOut)
	}
	code, _, errOut = w.m.run(t, "workflow", "upgrade", "--version", "9.9.9")
	if code == 0 || !strings.Contains(errOut, "9.9.9") {
		t.Errorf("upgrade to a missing release: exit %d, stderr %q", code, errOut)
	}
	if _, ok := remoteWorkflow(t, w.remote()); ok {
		t.Error("a failed upgrade installed the workflow")
	}

	code, out, errOut = w.m.run(t, "workflow", "upgrade", "--version", "v1.2.3")
	if code != 0 {
		t.Fatalf("upgrade --version v1.2.3: exit %d: %s", code, errOut)
	}
	if spec, ok := remoteWorkflow(t, w.remote()); !ok || spec.Pin.Version != "1.2.3" || spec.Pin.SHA256 != releaseSum("1.2.3") {
		t.Errorf("workflow = %+v, %v; want pinned to 1.2.3", spec, ok)
	}
	if !strings.Contains(out, "1.2.3") {
		t.Errorf("upgrade output = %q, want the pinned version", out)
	}
}

// Real-GitHub testing uses pre-release tags such as v0.1.0-rc1.
func TestPreReleaseBuildsPinTheirRelease(t *testing.T) {
	w := newGitHubWorld(t, goodToken+"\n")
	rel := fakeReleases(t, "0.1.0-rc1", "0.1.0-rc2")
	cli.UseReleases(t, rel, "0.1.0-rc1")

	if code, _, errOut := w.m.run(t, "init", "alice/notes", w.m.folder); code != 0 {
		t.Fatalf("init: exit %d: %s", code, errOut)
	}
	if spec, ok := remoteWorkflow(t, w.remote()); !ok || spec.Pin.Version != "0.1.0-rc1" ||
		!strings.HasSuffix(spec.Pin.URL, "/v0.1.0-rc1/gcd_0.1.0-rc1_linux_amd64.tar.gz") {
		t.Fatalf("workflow = %+v, %v; want pinned to 0.1.0-rc1", spec, ok)
	}

	code, out, errOut := w.m.run(t, "workflow", "upgrade", "--version", "0.1.0-rc2")
	if code != 0 {
		t.Fatalf("upgrade --version 0.1.0-rc2: exit %d: %s", code, errOut)
	}
	if spec, _ := remoteWorkflow(t, w.remote()); spec.Pin.Version != "0.1.0-rc2" || spec.Pin.SHA256 != releaseSum("0.1.0-rc2") {
		t.Errorf("workflow pin = %+v, want 0.1.0-rc2", spec.Pin)
	}
	if !strings.Contains(out, "0.1.0-rc1 -> 0.1.0-rc2") {
		t.Errorf("upgrade output = %q, want both versions", out)
	}
}

func TestWorkflowUpgradePinsTheRunningVersion(t *testing.T) {
	w := newGitHubWorld(t, goodToken+"\n")
	rel := fakeReleases(t, "1.2.3", "1.3.0")
	cli.UseReleases(t, rel, "1.2.3")
	if code, _, errOut := w.m.run(t, "init", "alice/notes", w.m.folder); code != 0 {
		t.Fatalf("init: exit %d: %s", code, errOut)
	}

	cli.UseReleases(t, rel, "1.3.0")
	code, out, errOut := w.m.run(t, "workflow", "upgrade")
	if code != 0 {
		t.Fatalf("upgrade: exit %d: %s", code, errOut)
	}
	if !strings.Contains(out, "1.2.3") || !strings.Contains(out, "1.3.0") {
		t.Errorf("upgrade output = %q, want old and new version", out)
	}
	if spec, _ := remoteWorkflow(t, w.remote()); spec.Pin.Version != "1.3.0" || spec.Pin.SHA256 != releaseSum("1.3.0") {
		t.Errorf("workflow pin = %+v, want 1.3.0", spec.Pin)
	}
	commits := len(w.remote().Log(t))
	if code, out, _ := w.m.run(t, "workflow", "upgrade"); code != 0 || !strings.Contains(out, "already") {
		t.Errorf("second upgrade: exit %d, stdout %q; want already up to date", code, out)
	}
	if n := len(w.remote().Log(t)); n != commits {
		t.Errorf("second upgrade made %d commits", n-commits)
	}

	if code, _, _ := w.m.run(t, "workflow"); code != 2 {
		t.Errorf("gcd workflow: exit %d, want usage (2)", code)
	}
}

func TestCompactWithoutTheWorkflowSaysHowToGetIt(t *testing.T) {
	w := newGitHubWorld(t, goodToken+"\n")
	cli.UseReleases(t, fakeReleases(t), "dev")
	if code, _, errOut := w.m.run(t, "init", "alice/notes", w.m.folder); code != 0 {
		t.Fatalf("init: exit %d: %s", code, errOut)
	}
	code, _, errOut := w.m.run(t, "compact")
	if code == 0 {
		t.Fatal("compact succeeded without a workflow")
	}
	for _, s := range []string{"gcd workflow upgrade", "gcd compact --local"} {
		if !strings.Contains(errOut, s) {
			t.Errorf("stderr does not mention %q:\n%s", s, errOut)
		}
	}
}

// The Actions token cannot have the Workflows permission. When GitHub
// refuses the rewritten history for touching the workflow file, the job log
// says how to fix it.
func TestCompactRunInActionsExplainsAWorkflowsRefusal(t *testing.T) {
	fake := &authtest.GitHub{Tokens: map[string]authtest.Grant{
		"actions-token": {Repos: []string{"alice/notes"}, ContentsWrite: true},
	}}
	remote := &fixture.Remote{Dir: fake.AddRepo(t, "alice/notes", true)}
	now := time.Now()
	remote.CommitAt(t, fixture.CommitSpec{Time: fixture.Ago(now, 90), Files: map[string]string{workflow.Path: "on: push\n", "a.txt": "1"}})
	remote.CommitAt(t, fixture.CommitSpec{Time: fixture.Ago(now, 60), Files: map[string]string{"a.txt": "2"}})
	remote.CommitAt(t, fixture.CommitSpec{Time: fixture.Ago(now, 1), Files: map[string]string{"a.txt": "3"}})
	head := remote.Head(t)
	gh := fake.Start(t)
	cli.UseFakeGitHub(t, gh, &authtest.Keyring{}, strings.NewReader(""))
	t.Setenv("GITHUB_ACTIONS", "true")
	t.Setenv("GITHUB_SERVER_URL", gh.WebURL)
	t.Setenv("GITHUB_REPOSITORY", "alice/notes")
	t.Setenv(auth.TokenEnv, "actions-token")
	m := machine{home: filepath.Join(t.TempDir(), "runner")}

	code, _, errOut := m.run(t, "compact", "--run")
	if code == 0 {
		t.Fatal("compact --run succeeded")
	}
	for _, s := range []string{"repository secret " + workflow.SecretName, auth.PermWorkflows, "gcd compact --local"} {
		if !strings.Contains(errOut, s) {
			t.Errorf("stderr does not mention %q:\n%s", s, errOut)
		}
	}
	if remote.Head(t) != head {
		t.Error("history was rewritten")
	}
}
