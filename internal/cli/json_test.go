package cli_test

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/Hanson-Tsai/gcd/internal/cli"
	"github.com/Hanson-Tsai/gcd/internal/config"
	"github.com/Hanson-Tsai/gcd/internal/fixture"
	"github.com/Hanson-Tsai/gcd/internal/workflow"
)

// The JSON shapes below mirror docs/json.md, decoded the way an agent would.

type jsonChange struct {
	Path string `json:"path"`
	Kind string `json:"kind"`
}

type jsonResult struct {
	Disk       string       `json:"disk"`
	Uploaded   []jsonChange `json:"uploaded"`
	Downloaded []jsonChange `json:"downloaded"`
	Conflicts  []struct {
		Path string `json:"path"`
		Copy string `json:"copy"`
	} `json:"conflicts"`
	Skipped  []string `json:"skipped"`
	Rejected []struct {
		Path   string `json:"path"`
		Reason string `json:"reason"`
		Detail string `json:"detail"`
		Remote bool   `json:"remote"`
	} `json:"rejected"`
	NeedsConfirm    bool    `json:"needs_confirm"`
	PendingDeletes  int     `json:"pending_deletes"`
	Commit          *string `json:"commit"`
	WorkflowVersion *string `json:"workflow_version"`
}

type jsonError struct {
	Error *struct {
		Kind           string `json:"kind"`
		Message        string `json:"message"`
		PendingDeletes *int   `json:"pending_deletes"`
	} `json:"error"`
}

// gcdJSON runs gcd, checks that stdout is exactly one JSON object line and
// stderr is empty, decodes stdout into v and returns the exit code.
func (m machine) gcdJSON(t *testing.T, v any, args ...string) int {
	t.Helper()
	code, out, errOut := m.run(t, args...)
	if errOut != "" {
		t.Errorf("gcd %v: stderr = %q, want nothing with --json", args, errOut)
	}
	if strings.Count(out, "\n") != 1 || !strings.HasSuffix(out, "\n") {
		t.Fatalf("gcd %v: stdout = %q, want one line of JSON", args, out)
	}
	dec := json.NewDecoder(strings.NewReader(out))
	if err := dec.Decode(v); err != nil {
		t.Fatalf("gcd %v: stdout %q is not JSON: %v", args, out, err)
	}
	return code
}

// gcdJSONError runs a --json command that must fail with exit 1 and returns
// its error object.
func (m machine) gcdJSONError(t *testing.T, args ...string) jsonError {
	t.Helper()
	var e jsonError
	if code := m.gcdJSON(t, &e, args...); code != 1 {
		t.Errorf("gcd %v: exit %d, want 1", args, code)
	}
	if e.Error == nil {
		t.Fatalf("gcd %v: no error object", args)
	}
	return e
}

func TestStatusJSONListsEveryKindOfChange(t *testing.T) {
	remote := fixture.NewRemote(t)
	a, b := newMachine(t, "a"), newMachine(t, "b")
	a.gcd(t, "init", remote.URL, a.folder)
	b.gcd(t, "init", remote.URL, b.folder)
	writeFile(t, filepath.Join(a.folder, "both.md"), "base")
	writeFile(t, filepath.Join(a.folder, "gone.md"), "x")
	a.gcd(t, "sync")
	b.gcd(t, "sync")

	writeFile(t, filepath.Join(a.folder, "both.md"), "from a")
	writeFile(t, filepath.Join(b.folder, "both.md"), "from b")
	writeFile(t, filepath.Join(b.folder, "theirs.md"), "t")
	b.gcd(t, "sync")
	writeFile(t, filepath.Join(a.folder, "mine.md"), "m")
	if err := os.Remove(filepath.Join(a.folder, "gone.md")); err != nil {
		t.Fatal(err)
	}
	remote.Commit(t, map[string]string{"a:b.txt": "x"}, "another git client")
	head := remote.Head(t)

	var got jsonResult
	if code := a.gcdJSON(t, &got, "status", "--json"); code != 0 {
		t.Fatalf("status --json: exit %d", code)
	}

	if got.Disk != "a-folder" {
		t.Errorf("disk = %q, want a-folder", got.Disk)
	}
	for _, want := range []jsonChange{{"mine.md", "added"}, {"gone.md", "deleted"}} {
		if !hasChange(got.Uploaded, want) {
			t.Errorf("uploaded = %v, want %v in it", got.Uploaded, want)
		}
	}
	for _, want := range []jsonChange{{"theirs.md", "added"}, {"both.md", "modified"}} {
		if !hasChange(got.Downloaded, want) {
			t.Errorf("downloaded = %v, want %v in it", got.Downloaded, want)
		}
	}
	if len(got.Conflicts) != 1 || got.Conflicts[0].Path != "both.md" || !strings.Contains(got.Conflicts[0].Copy, "conflict") {
		t.Errorf("conflicts = %+v, want both.md with a conflict copy", got.Conflicts)
	}
	if len(got.Rejected) != 1 || got.Rejected[0].Path != "a:b.txt" || got.Rejected[0].Reason != "bad_char" || !got.Rejected[0].Remote {
		t.Errorf("rejected = %+v, want a:b.txt (bad_char, remote)", got.Rejected)
	}
	if got.Skipped == nil || len(got.Skipped) != 0 {
		t.Errorf("skipped = %#v, want an empty array", got.Skipped)
	}
	if got.NeedsConfirm || got.PendingDeletes != 0 {
		t.Errorf("needs_confirm = %v, pending_deletes = %d; want false, 0", got.NeedsConfirm, got.PendingDeletes)
	}
	if got.Commit != nil {
		t.Errorf("status has a commit field: %q", *got.Commit)
	}
	if got.WorkflowVersion == nil || *got.WorkflowVersion != "" {
		t.Errorf("workflow_version = %v, want \"\" (no workflow in the repo)", got.WorkflowVersion)
	}
	if remote.Head(t) != head {
		t.Error("status --json pushed to the remote")
	}
}

func hasChange(changes []jsonChange, want jsonChange) bool {
	for _, c := range changes {
		if c == want {
			return true
		}
	}
	return false
}

func TestStatusJSONEmptyListsAreArrays(t *testing.T) {
	remote := fixture.NewRemote(t)
	a := newMachine(t, "a")
	a.gcd(t, "init", remote.URL, a.folder)

	_, out, _ := a.run(t, "status", "--json")
	for _, field := range []string{`"uploaded":[]`, `"downloaded":[]`, `"conflicts":[]`, `"skipped":[]`, `"rejected":[]`} {
		if !strings.Contains(out, field) {
			t.Errorf("status --json = %s, want %s", out, field)
		}
	}
}

func TestStatusJSONReportsThePinnedWorkflowVersion(t *testing.T) {
	remote := fixture.NewRemote(t)
	a := newMachine(t, "a")
	a.gcd(t, "init", remote.URL, a.folder)
	raw, err := workflow.Render(workflow.Spec{
		Pin: workflow.Pin{
			Version: "0.1.0-rc1",
			SHA256:  strings.Repeat("ab", 32),
			URL:     "https://github.com/Hanson-Tsai/gcd/releases/download/v0.1.0-rc1/" + workflow.AssetName("0.1.0-rc1"),
		},
		Schedule: "0 3 * * 0",
	})
	if err != nil {
		t.Fatal(err)
	}
	remote.Commit(t, map[string]string{workflow.Path: string(raw)}, "install workflow")
	head := remote.Head(t)

	var got jsonResult
	a.gcdJSON(t, &got, "status", "--json")

	if got.WorkflowVersion == nil || *got.WorkflowVersion != "0.1.0-rc1" {
		t.Errorf("workflow_version = %v, want 0.1.0-rc1", got.WorkflowVersion)
	}
	if remote.Head(t) != head {
		t.Error("status --json pushed to the remote")
	}
}

func TestVersionJSON(t *testing.T) {
	m := newMachine(t, "a")
	for _, tc := range []struct {
		version string
		dev     bool
	}{
		{"0.1.0-rc2", false},
		{"1.2.3", false},
		{"dev", true},
		{"0.0.0-20261103080000-abcdef123456+dirty", true},
	} {
		cli.UseReleases(t, workflow.Releases{}, tc.version)
		var got struct {
			Version string `json:"version"`
			OS      string `json:"os"`
			Arch    string `json:"arch"`
			Dev     *bool  `json:"dev"`
		}
		if code := m.gcdJSON(t, &got, "version", "--json"); code != 0 {
			t.Fatalf("version --json: exit %d", code)
		}
		if got.Version != tc.version || got.OS != runtime.GOOS || got.Arch != runtime.GOARCH || got.Dev == nil || *got.Dev != tc.dev {
			t.Errorf("version --json for %s = %+v (dev %v), want dev %v", tc.version, got, got.Dev, tc.dev)
		}
	}
}

func TestSyncJSON(t *testing.T) {
	remote := fixture.NewRemote(t)
	a := newMachine(t, "a")
	a.gcd(t, "init", remote.URL, a.folder)
	writeFile(t, filepath.Join(a.folder, "new.md"), "n")

	var got jsonResult
	if code := a.gcdJSON(t, &got, "sync", "--json"); code != 0 {
		t.Fatalf("sync --json: exit %d", code)
	}
	if !hasChange(got.Uploaded, jsonChange{"new.md", "added"}) {
		t.Errorf("uploaded = %v, want new.md added", got.Uploaded)
	}
	if got.Commit == nil || *got.Commit != remote.Head(t) {
		t.Errorf("commit = %v, want the remote head %s", got.Commit, remote.Head(t))
	}
	if got.WorkflowVersion != nil {
		t.Errorf("sync has a workflow_version field: %q", *got.WorkflowVersion)
	}

	got = jsonResult{}
	a.gcdJSON(t, &got, "sync", "--json")
	if got.Commit == nil || *got.Commit != "" {
		t.Errorf("commit of a sync with nothing to push = %v, want \"\"", got.Commit)
	}
}

func TestSyncJSONNeedsConfirmCarriesThePendingDeletes(t *testing.T) {
	remote := fixture.NewRemote(t)
	a := newMachine(t, "a")
	a.gcd(t, "init", remote.URL, a.folder)
	for i := range 10 {
		writeFile(t, filepath.Join(a.folder, fmt.Sprintf("f%d.md", i)), "x")
	}
	a.gcd(t, "sync")
	for i := range 10 {
		if err := os.Remove(filepath.Join(a.folder, fmt.Sprintf("f%d.md", i))); err != nil {
			t.Fatal(err)
		}
	}

	var st jsonResult
	a.gcdJSON(t, &st, "status", "--json")
	if !st.NeedsConfirm || st.PendingDeletes != 10 {
		t.Errorf("status: needs_confirm = %v, pending_deletes = %d; want true, 10", st.NeedsConfirm, st.PendingDeletes)
	}

	e := a.gcdJSONError(t, "sync", "--json")
	if e.Error.Kind != "needs_confirm" || e.Error.PendingDeletes == nil || *e.Error.PendingDeletes != 10 {
		t.Errorf("error = %+v, want needs_confirm with pending_deletes 10", e.Error)
	}
	if !strings.Contains(e.Error.Message, "gcd sync --yes") {
		t.Errorf("message %q does not say how to confirm", e.Error.Message)
	}
	if n := len(remote.Files(t)); n < 10 {
		t.Fatalf("remote has %d files after the refused sync, want all 10 kept", n)
	}
}

func TestJSONErrorNoDisk(t *testing.T) {
	remote := fixture.NewRemote(t)
	a := newMachine(t, "a")

	if e := a.gcdJSONError(t, "status", "--json"); e.Error.Kind != "no_disk" {
		t.Errorf("status with no disk registered: kind %q, want no_disk", e.Error.Kind)
	}
	a.gcd(t, "init", remote.URL, a.folder)
	e := a.gcdJSONError(t, "sync", "--json", "--disk", "nope")
	if e.Error.Kind != "no_disk" || !strings.Contains(e.Error.Message, "a-folder") {
		t.Errorf("sync --disk nope: %+v, want no_disk listing a-folder", e.Error)
	}
}

func TestJSONErrorFormatTooNew(t *testing.T) {
	remote := fixture.NewRemote(t)
	a := newMachine(t, "a")
	a.gcd(t, "init", remote.URL, a.folder)
	remote.Commit(t, map[string]string{config.Path: "format = 99\n"}, "future")

	for _, cmd := range []string{"status", "sync"} {
		e := a.gcdJSONError(t, cmd, "--json")
		if e.Error.Kind != "format_too_new" || !strings.Contains(e.Error.Message, "upgrade gcd") {
			t.Errorf("%s: %+v, want format_too_new asking to upgrade gcd", cmd, e.Error)
		}
	}
}

func TestJSONErrorAuthOnARevokedToken(t *testing.T) {
	w := newGitHubWorld(t, goodToken+"\n")
	if code, _, errOut := w.m.run(t, "init", "alice/notes", w.m.folder); code != 0 {
		t.Fatalf("init: exit %d: %s", code, errOut)
	}
	w.gh.Revoke(goodToken)

	e := w.m.gcdJSONError(t, "sync", "--json")
	if e.Error.Kind != "auth" || !strings.Contains(e.Error.Message, "gcd login alice/notes") {
		t.Errorf("error = %+v, want auth with the login hint", e.Error)
	}
	if strings.Contains(e.Error.Message, goodToken) {
		t.Errorf("message leaks the token: %q", e.Error.Message)
	}
}

func TestJSONUsageErrorsStayText(t *testing.T) {
	a := newMachine(t, "a")
	code, out, errOut := a.run(t, "status", "--json", "extra")
	if code != 2 || out != "" || !strings.Contains(errOut, "usage: gcd status") {
		t.Errorf("status --json extra: exit %d, stdout %q, stderr %q; want exit 2 with the usage on stderr", code, out, errOut)
	}
}

func TestLogJSON(t *testing.T) {
	remote := fixture.NewRemote(t)
	a := newMachine(t, "a")
	a.gcd(t, "init", remote.URL, a.folder)
	writeFile(t, filepath.Join(a.folder, "notes", "a.md"), "1")
	a.gcd(t, "sync")
	writeFile(t, filepath.Join(a.folder, "other.md"), "o")
	a.gcd(t, "sync")
	writeFile(t, filepath.Join(a.folder, "notes", "a.md"), "2")
	a.gcd(t, "sync")

	type logJSON struct {
		Disk    string  `json:"disk"`
		Path    *string `json:"path"`
		Entries []struct {
			Commit  string       `json:"commit"`
			Time    string       `json:"time"`
			Host    string       `json:"host"`
			Changes []jsonChange `json:"changes"`
		} `json:"entries"`
	}
	var all logJSON
	if code := a.gcdJSON(t, &all, "log", "--json"); code != 0 {
		t.Fatalf("log --json: exit %d", code)
	}
	if all.Disk != "a-folder" || all.Path == nil || *all.Path != "" || len(all.Entries) != 3 {
		t.Fatalf("log --json = %+v, want 3 entries for the whole disk", all)
	}
	newest := all.Entries[0]
	if newest.Commit != remote.Head(t) || newest.Host == "" || !strings.HasSuffix(newest.Time, "Z") {
		t.Errorf("newest entry = %+v, want the head commit, a host and a UTC time", newest)
	}
	if len(newest.Changes) != 1 || newest.Changes[0] != (jsonChange{"notes/a.md", "modified"}) {
		t.Errorf("newest changes = %v, want notes/a.md modified", newest.Changes)
	}

	var one logJSON
	t.Chdir(filepath.Join(a.folder, "notes"))
	a.gcdJSON(t, &one, "log", "a.md", "--json")
	if one.Path == nil || *one.Path != "notes/a.md" || len(one.Entries) != 2 {
		t.Fatalf("log a.md --json = %+v, want path notes/a.md with 2 entries", one)
	}
	if one.Entries[1].Changes[0] != (jsonChange{"notes/a.md", "added"}) {
		t.Errorf("oldest change = %v, want notes/a.md added", one.Entries[1].Changes)
	}
}

func TestRestoreJSON(t *testing.T) {
	remote := fixture.NewRemote(t)
	a := newMachine(t, "a")
	a.gcd(t, "init", remote.URL, a.folder)
	file := filepath.Join(a.folder, "doc.md")
	writeFile(t, file, "v1")
	writeFile(t, filepath.Join(a.folder, "keep.md"), "k") // deleting doc.md is not deleting everything
	a.gcd(t, "sync")
	first := remote.Head(t)
	if err := os.Remove(file); err != nil {
		t.Fatal(err)
	}
	a.gcd(t, "sync")

	type restoreJSON struct {
		Path      string `json:"path"`
		Commit    string `json:"commit"`
		Time      string `json:"time"`
		Unchanged *bool  `json:"unchanged"`
	}
	var got restoreJSON
	if code := a.gcdJSON(t, &got, "restore", filepath.Join(a.folder, "doc.md"), "--json"); code != 0 {
		t.Fatalf("restore --json: exit %d", code)
	}
	if got.Path != "doc.md" || got.Commit != first || !strings.HasSuffix(got.Time, "Z") || got.Unchanged == nil || *got.Unchanged {
		t.Errorf("restore --json = %+v, want doc.md from %s, changed", got, first)
	}
	if b, err := os.ReadFile(file); err != nil || string(b) != "v1" {
		t.Errorf("doc.md = %q, %v; want v1", b, err)
	}

	got = restoreJSON{}
	a.gcdJSON(t, &got, "restore", file, "--json")
	if got.Unchanged == nil || !*got.Unchanged {
		t.Errorf("restoring the same version again: %+v, want unchanged", got)
	}

	writeFile(t, file, "edited")
	e := a.gcdJSONError(t, "restore", file, "--json")
	if e.Error.Kind != "exists" || !strings.Contains(e.Error.Message, "--force") {
		t.Errorf("restore over a different file: %+v, want exists mentioning --force", e.Error)
	}
	if b, _ := os.ReadFile(file); string(b) != "edited" {
		t.Errorf("refused restore changed doc.md to %q", b)
	}
}
