package cli_test

import (
	"bytes"
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/Hanson-Tsai/gcd/internal/cli"
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
	notesRemote.AssertFiles(t, map[string]string{"sub/n.txt": "note"})
	docsRemote.AssertFiles(t, map[string]string{})

	// Outside every disk: ambiguous without --disk, fine with it.
	t.Chdir(outside)
	msg := m.gcdFails(t, "sync")
	for _, want := range []string{"--disk", "notes", notes, "docs", docs} {
		if !strings.Contains(msg, want) {
			t.Errorf("ambiguous sync error %q does not mention %q", msg, want)
		}
	}
	m.gcd(t, "sync", "--disk", "docs")
	docsRemote.AssertFiles(t, map[string]string{"d.txt": "doc"})
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
