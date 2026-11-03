package cli_test

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/Hanson-Tsai/gcd/internal/fixture"
)

func TestSyncAsksForYesBeforeAMassDelete(t *testing.T) {
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

	msg := a.gcdFails(t, "sync")
	for _, want := range []string{"10", "gcd sync --yes"} {
		if !strings.Contains(msg, want) {
			t.Errorf("mass-delete error %q does not mention %q", msg, want)
		}
	}
	if n := len(remote.Files(t)); n < 10 {
		t.Fatalf("remote has %d files after the refused sync, want all 10 kept", n)
	}

	if out := a.gcd(t, "sync", "--yes"); !strings.Contains(out, "- f0.md") {
		t.Errorf("sync --yes output = %q, want it to list the deletions", out)
	}
}
