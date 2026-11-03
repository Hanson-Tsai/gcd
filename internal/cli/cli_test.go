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

// gcdFails runs gcd, expects a non-zero exit and returns stderr.
func (m machine) gcdFails(t *testing.T, args ...string) string {
	t.Helper()
	t.Setenv(registry.HomeEnv, m.home)
	var out, errOut bytes.Buffer
	if code := cli.Run(context.Background(), args, &out, &errOut); code == 0 {
		t.Fatalf("gcd %v: exit 0, want failure; stdout: %s", args, out.String())
	}
	return errOut.String()
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

func TestUnknownCommandFails(t *testing.T) {
	var out, errOut bytes.Buffer
	if code := cli.Run(context.Background(), []string{"bogus"}, &out, &errOut); code == 0 {
		t.Error("unknown command exited 0")
	}
}
