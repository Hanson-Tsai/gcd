package gitstore_test

import (
	"context"
	"os/exec"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/Hanson-Tsai/gcd/internal/fixture"
	"github.com/Hanson-Tsai/gcd/internal/gitstore"
)

func openStore(t *testing.T) (*gitstore.Store, string) {
	t.Helper()
	ctx := context.Background()
	remote := fixture.NewRemote(t)
	s, err := gitstore.Open(ctx, filepath.Join(t.TempDir(), "git"), remote.URL)
	if err != nil {
		t.Fatal(err)
	}
	blob, err := s.WriteBlob(ctx, []byte("x"))
	if err != nil {
		t.Fatal(err)
	}
	return s, blob
}

// A path git can never hold in a tree must fail the build: a tree missing an
// entry would delete that file from the remote.
func TestWriteTreeFailsRatherThanDropAPath(t *testing.T) {
	ctx := context.Background()
	s, blob := openStore(t)
	for _, bad := range []string{".git/x", "a/.GIT/x", "a/../b", "a//b", "/a", "a/", "f/x"} {
		entries := map[string]gitstore.Entry{
			"ok.md": {Mode: gitstore.RegularMode, Hash: blob},
			"f":     {Mode: gitstore.RegularMode, Hash: blob},
			bad:     {Mode: gitstore.RegularMode, Hash: blob},
		}
		if tree, err := s.WriteTree(ctx, entries); err == nil {
			got, _ := s.ReadTree(ctx, tree)
			t.Errorf("WriteTree with %q succeeded: %v", bad, got)
		}
	}
}

// Trees are encoded by gcd, not git, so they must be exactly what git
// writes: entries sorted with directories as if named "dir/", valid to
// git fsck (GitHub refuses pushes that fail it), and read back unchanged.
func TestWriteTreeMatchesGit(t *testing.T) {
	ctx := context.Background()
	s, blob := openStore(t)
	entries := map[string]gitstore.Entry{}
	for _, p := range []string{
		"a", "a.b", "a-b", "b/c", "b.c", "b-c", "b0", "b/d/e", "B/x",
		"a:b.txt", "CON.md", "trail.", "dir?/inner.md", "sp ace/é.md",
	} {
		entries[p] = gitstore.Entry{Mode: gitstore.RegularMode, Hash: blob}
	}
	entries["run.sh"] = gitstore.Entry{Mode: "100755", Hash: blob}
	entries["link"] = gitstore.Entry{Mode: "120000", Hash: blob}

	tree, err := s.WriteTree(ctx, entries)
	if err != nil {
		t.Fatal(err)
	}
	got, err := s.ReadTree(ctx, tree)
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(got, entries) {
		t.Errorf("read back:\n got  %v\n want %v", got, entries)
	}
	if out, err := exec.Command("git", "--git-dir="+s.GitDir, "fsck", "--strict", "--no-dangling").CombinedOutput(); err != nil || strings.Contains(string(out), "warning") {
		t.Errorf("git fsck: %v: %s", err, out)
	}
	empty, err := s.WriteTree(ctx, nil)
	if err != nil || empty != "4b825dc642cb6eb9a060e54bf8d69288fbee4904" {
		t.Errorf("empty tree = %q, %v", empty, err)
	}
}
