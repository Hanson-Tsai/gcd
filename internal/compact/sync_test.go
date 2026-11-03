package compact_test

import (
	"context"
	"path/filepath"
	"testing"
	"time"

	"github.com/Hanson-Tsai/gcd/internal/compact"
	"github.com/Hanson-Tsai/gcd/internal/fixture"
)

// Seam A after Seam B: devices keep syncing across a compaction, including
// changes made before it that were not synced yet, without false conflicts.
func TestDevicesSyncNormallyAfterCompaction(t *testing.T) {
	remote, devs := fixture.NewDevices(t, 2)
	a, b := devs[0], devs[1]
	wall := time.Now()
	remote.CommitAt(t, fixture.CommitSpec{Time: fixture.Ago(wall, 90), Files: map[string]string{"a.txt": "a1", "b.txt": "b1"}})
	remote.CommitAt(t, fixture.CommitSpec{Time: fixture.Ago(wall, 60), Files: map[string]string{"a.txt": "a2", "gone.txt": "x"}})
	remote.CommitAt(t, fixture.CommitSpec{Time: fixture.Ago(wall, 45), Delete: []string{"gone.txt"}})
	a.Sync(t)
	b.Sync(t)
	a.Write(t, "x.txt", "from a")
	a.Sync(t)
	b.Write(t, "b.txt", "b2 from b") // not synced before the compaction
	a.Write(t, "y.txt", "also from a")

	// Compact through device a's own git-dir, as gcd compact --local does.
	res, err := compact.Run(context.Background(), compact.Options{
		GitDir: filepath.Join(a.StateDir, "git"), Remote: remote.URL, RetentionDays: 30,
	})
	if err != nil || !res.Compacted || res.Squashed != 3 || res.Kept != 1 {
		t.Fatalf("compact = %+v, %v; want 3 squashed, 1 kept", res, err)
	}

	for _, d := range []*fixture.Device{b, a, b} {
		if r := d.Sync(t); len(r.Conflicts) != 0 {
			t.Fatalf("sync on %s after compaction: conflicts %+v", d.Host, r.Conflicts)
		}
	}
	want := map[string]string{"a.txt": "a2", "b.txt": "b2 from b", "x.txt": "from a", "y.txt": "also from a"}
	a.AssertFiles(t, want)
	b.AssertFiles(t, want)
	remote.AssertFiles(t, want)
	log := remote.History(t)
	if root := log[len(log)-1]; root.Hash != res.NewRoot || len(log) != 4 {
		t.Errorf("history = %d commits ending in %s, want 4 on the snapshot %s", len(log), root.Hash, res.NewRoot)
	}
}
