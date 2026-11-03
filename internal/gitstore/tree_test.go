package gitstore_test

import (
	"context"
	"path/filepath"
	"testing"

	"github.com/Hanson-Tsai/gcd/internal/fixture"
	"github.com/Hanson-Tsai/gcd/internal/gitstore"
)

// git's update-index --index-info skips a path it will not put in an index
// and still exits 0. A tree missing an entry would delete that file from the
// remote, so WriteTree must fail instead.
func TestWriteTreeFailsRatherThanDropAPath(t *testing.T) {
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
	entries := map[string]gitstore.Entry{
		"ok.md":   {Mode: gitstore.RegularMode, Hash: blob},
		".git/x":  {Mode: gitstore.RegularMode, Hash: blob}, // refused by every git
		"a/../b":  {Mode: gitstore.RegularMode, Hash: blob},
		"keep.md": {Mode: gitstore.RegularMode, Hash: blob},
	}
	if tree, err := s.WriteTree(ctx, entries); err == nil {
		got, _ := s.ReadTree(ctx, tree)
		t.Fatalf("WriteTree succeeded with %d of %d entries: %v", len(got), len(entries), got)
	}
}
