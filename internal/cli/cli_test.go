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
