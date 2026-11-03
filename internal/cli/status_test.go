package cli_test

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/Hanson-Tsai/gcd/internal/fixture"
)

func TestStatusSmoke(t *testing.T) {
	remote := fixture.NewRemote(t)
	root := t.TempDir()
	a := machine{home: filepath.Join(root, "a-home"), folder: filepath.Join(root, "a-folder")}
	a.gcd(t, "init", remote.URL, a.folder)
	if out := a.gcd(t, "status"); !strings.Contains(out, "up to date") {
		t.Errorf("status on a fresh disk = %q, want \"up to date\"", out)
	}

	writeFile(t, filepath.Join(a.folder, "mine.md"), "m")
	remote.Commit(t, map[string]string{"theirs.md": "t", "a:b.txt": "x"}, "another client")
	head := remote.Head(t)

	out := a.gcd(t, "status")

	for _, want := range []string{
		"up    + mine.md",
		"down  + theirs.md",
		"not synced a:b.txt (on the remote)",
		"1 to upload, 1 to download",
	} {
		if !strings.Contains(out, want) {
			t.Errorf("status output = %q, want a line with %q", out, want)
		}
	}
	if remote.Head(t) != head {
		t.Error("status pushed to the remote")
	}
	if _, err := os.Stat(filepath.Join(a.folder, "theirs.md")); !os.IsNotExist(err) {
		t.Errorf("status downloaded theirs.md: %v", err)
	}
}

func TestStatusSaysWhenSyncNeedsYes(t *testing.T) {
	remote := fixture.NewRemote(t)
	root := t.TempDir()
	a := machine{home: filepath.Join(root, "a-home"), folder: filepath.Join(root, "a-folder")}
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

	out := a.gcd(t, "status")

	if !strings.Contains(out, "gcd sync --yes") {
		t.Errorf("status output = %q, want it to say the sync needs `gcd sync --yes`", out)
	}
}
