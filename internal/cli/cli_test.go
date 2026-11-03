package cli_test

import (
	"bytes"
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/Hanson-Tsai/gcd/internal/cli"
	"github.com/Hanson-Tsai/gcd/internal/config"
	"github.com/Hanson-Tsai/gcd/internal/fixture"
	"github.com/Hanson-Tsai/gcd/internal/registry"
)

// machine is one simulated computer: its own gcd home (registry + state).
type machine struct {
	home, folder string
}

func (m machine) gcd(t *testing.T, args ...string) string {
	t.Helper()
	t.Setenv(registry.HomeEnv, m.home)
	var out, errOut bytes.Buffer
	if code := cli.Run(context.Background(), args, &out, &errOut); code != 0 {
		t.Fatalf("gcd %v: exit %d: %s", args, code, errOut.String())
	}
	return out.String()
}

func TestInitAndSyncSmoke(t *testing.T) {
	remote := fixture.NewRemote(t)
	root := t.TempDir()
	a := machine{home: filepath.Join(root, "a-home"), folder: filepath.Join(root, "a-folder")}
	b := machine{home: filepath.Join(root, "b-home"), folder: filepath.Join(root, "b-folder")}

	a.gcd(t, "init", remote.URL, a.folder)
	b.gcd(t, "init", remote.URL, b.folder)

	if err := os.WriteFile(filepath.Join(a.folder, "hello.txt"), []byte("hi"), 0o644); err != nil {
		t.Fatal(err)
	}
	if out := a.gcd(t, "sync"); !strings.Contains(out, "+ hello.txt") {
		t.Errorf("sync output on a = %q, want it to list the upload", out)
	}
	b.gcd(t, "sync")

	got, err := os.ReadFile(filepath.Join(b.folder, "hello.txt"))
	if err != nil || string(got) != "hi" {
		t.Fatalf("b/hello.txt = %q, %v; want \"hi\"", got, err)
	}
	if _, err := os.Stat(filepath.Join(a.folder, ".git")); !os.IsNotExist(err) {
		t.Errorf("init left .git in the synced folder: %v", err)
	}
}

func TestSyncListsRejectedFilesWithReasons(t *testing.T) {
	remote := fixture.NewRemote(t)
	root := t.TempDir()
	a := machine{home: filepath.Join(root, "a-home"), folder: filepath.Join(root, "a-folder")}
	a.gcd(t, "init", remote.URL, a.folder)
	remote.Commit(t, map[string]string{"a:b.txt": "x", "ok.txt": "ok"}, "another git client")

	out := a.gcd(t, "sync")

	want := `not synced a:b.txt (on the remote): name contains ':', which Windows does not allow`
	if !strings.Contains(out, want) {
		t.Errorf("sync output = %q, want a line %q", out, want)
	}
}

// gcdFails runs gcd expecting a non-zero exit and returns its stderr.
func (m machine) gcdFails(t *testing.T, args ...string) string {
	t.Helper()
	t.Setenv(registry.HomeEnv, m.home)
	var out, errOut bytes.Buffer
	if code := cli.Run(context.Background(), args, &out, &errOut); code == 0 {
		t.Fatalf("gcd %v: exit 0, want failure; stdout: %s", args, out.String())
	}
	return errOut.String()
}

func writeFile(t *testing.T, path, content string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
}

func TestTwoNamedDisksOnOneMachine(t *testing.T) {
	notesRemote, docsRemote := fixture.NewRemote(t), fixture.NewRemote(t)
	root := t.TempDir()
	m := machine{home: filepath.Join(root, "home")}
	notes, docs := filepath.Join(root, "my-notes"), filepath.Join(root, "my-docs")
	outside := filepath.Join(root, "elsewhere")
	writeFile(t, filepath.Join(outside, "x"), "")

	m.gcd(t, "init", "--disk", "notes", notesRemote.URL, notes)
	m.gcd(t, "init", "--disk", "docs", docsRemote.URL, docs)
	if msg := m.gcdFails(t, "init", "--disk", "Notes", docsRemote.URL, filepath.Join(root, "third")); !strings.Contains(msg, `"notes"`) {
		t.Errorf("init with a taken name: %q", msg)
	}
	if msg := m.gcdFails(t, "init", "--disk", "inner", docsRemote.URL, filepath.Join(notes, "inner")); !strings.Contains(msg, `"notes"`) {
		t.Errorf("init inside another disk: %q", msg)
	}
	if _, err := os.Stat(filepath.Join(notes, "inner")); !os.IsNotExist(err) {
		t.Errorf("refused init still created its folder inside disk notes: %v", err)
	}

	writeFile(t, filepath.Join(notes, "sub", "n.txt"), "note")
	writeFile(t, filepath.Join(docs, "d.txt"), "doc")

	// Inside a subfolder of notes: no flag needed.
	t.Chdir(filepath.Join(notes, "sub"))
	m.gcd(t, "sync")
	notesRemote.AssertFiles(t, map[string]string{"sub/n.txt": "note", config.Path: defaultConfig})
	docsRemote.AssertFiles(t, map[string]string{config.Path: defaultConfig})

	// Outside every disk: ambiguous without --disk, fine with it.
	t.Chdir(outside)
	msg := m.gcdFails(t, "sync")
	for _, want := range []string{"--disk", "notes", notes, "docs", docs} {
		if !strings.Contains(msg, want) {
			t.Errorf("ambiguous sync error %q does not mention %q", msg, want)
		}
	}
	m.gcd(t, "sync", "--disk", "docs")
	docsRemote.AssertFiles(t, map[string]string{"d.txt": "doc", config.Path: defaultConfig})

	// log and restore take --disk too; restore paths are folder-relative here.
	if out := m.gcd(t, "log", "--disk", "docs"); !strings.Contains(out, "+1 ~0 -0") {
		t.Errorf("gcd log --disk docs = %q, want the docs sync", out)
	}
	if err := os.Remove(filepath.Join(docs, "d.txt")); err != nil {
		t.Fatal(err)
	}
	m.gcd(t, "restore", "--disk", "docs", "d.txt")
	if got, err := os.ReadFile(filepath.Join(docs, "d.txt")); err != nil || string(got) != "doc" {
		t.Errorf("restore --disk docs d.txt: got %q, %v; want doc", got, err)
	}
}

func TestDiskFlagOnlyOnDiskCommands(t *testing.T) {
	var out, errOut bytes.Buffer
	if code := cli.Run(context.Background(), []string{"version", "--disk", "notes"}, &out, &errOut); code == 0 {
		t.Error("gcd version --disk: exit 0, want an unknown-flag error")
	}
}

func TestInitNamesDiskAfterFolderByDefault(t *testing.T) {
	remote := fixture.NewRemote(t)
	root := t.TempDir()
	m := machine{home: filepath.Join(root, "home")}

	if out := m.gcd(t, "init", remote.URL, filepath.Join(root, "Photos")); !strings.Contains(out, `"Photos"`) {
		t.Errorf("init output %q does not name the disk Photos", out)
	}
	if msg := m.gcdFails(t, "init", remote.URL, filepath.Join(root, "my photos")); !strings.Contains(msg, "--disk") {
		t.Errorf("init with an unusable folder name: %q, want a hint to pass --disk", msg)
	}
}

func TestLogAndRestoreSmoke(t *testing.T) {
	remote := fixture.NewRemote(t)
	root := t.TempDir()
	a := machine{home: filepath.Join(root, "a-home"), folder: filepath.Join(root, "a-folder")}
	a.gcd(t, "init", remote.URL, a.folder)
	file := filepath.Join(a.folder, "notes", "plan.md")
	if err := os.MkdirAll(filepath.Dir(file), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(file, []byte("v1"), 0o644); err != nil {
		t.Fatal(err)
	}
	a.gcd(t, "sync")
	if err := os.Remove(file); err != nil {
		t.Fatal(err)
	}
	a.gcd(t, "sync")

	t.Chdir(a.folder)
	if out := a.gcd(t, "log"); !strings.Contains(out, "+0 ~0 -1") || !strings.Contains(out, "+1 ~0 -0") {
		t.Errorf("gcd log = %q, want one line per sync with its change summary", out)
	}
	added := remote.Log(t)[1].Hash
	if out := a.gcd(t, "log", "notes/plan.md"); !strings.Contains(out, added[:7]) || !strings.Contains(out, "- notes/plan.md") {
		t.Errorf("gcd log notes/plan.md = %q, want commit %s and the deletion", out, added[:7])
	}

	if msg := a.gcdFails(t, "restore", "notes/plan.md", "--at", "2000-01-01"); !strings.Contains(msg, "oldest version") {
		t.Errorf("restore before history: stderr = %q, want retention error", msg)
	}

	// Flags may follow the path, and the path is relative to the cwd.
	if err := os.MkdirAll(filepath.Dir(file), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Chdir(filepath.Dir(file))
	a.gcd(t, "restore", "plan.md", "--at", added[:7])
	if got, err := os.ReadFile(file); err != nil || string(got) != "v1" {
		t.Fatalf("restored file = %q, %v; want v1", got, err)
	}

	// A different local file is not overwritten without --force.
	if err := os.WriteFile(file, []byte("mine"), 0o644); err != nil {
		t.Fatal(err)
	}
	if msg := a.gcdFails(t, "restore", "plan.md"); !strings.Contains(msg, "--force") {
		t.Errorf("restore over existing file: stderr = %q, want a hint about --force", msg)
	}
	a.gcd(t, "restore", "--force", "plan.md")
	if got, _ := os.ReadFile(file); string(got) != "v1" {
		t.Fatalf("forced restore left %q, want v1", got)
	}

	if out := a.gcd(t, "sync"); !strings.Contains(out, "+ notes/plan.md") {
		t.Errorf("sync after restore = %q, want upload of notes/plan.md", out)
	}
}

func TestVersionCommand(t *testing.T) {
	var out, errOut bytes.Buffer
	if code := cli.Run(context.Background(), []string{"version"}, &out, &errOut); code != 0 {
		t.Fatalf("gcd version: exit %d: %s", code, errOut.String())
	}
	if !strings.HasPrefix(out.String(), "gcd version ") {
		t.Errorf("gcd version printed %q, want it to start with \"gcd version \"", out.String())
	}
}

func TestUnknownCommandFails(t *testing.T) {
	var out, errOut bytes.Buffer
	if code := cli.Run(context.Background(), []string{"bogus"}, &out, &errOut); code == 0 {
		t.Error("unknown command exited 0")
	}
}
