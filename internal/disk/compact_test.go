package disk_test

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/Hanson-Tsai/gcd/internal/disk"
	"github.com/Hanson-Tsai/gcd/internal/fixture"
)

func TestCompactWaitsForNoSync(t *testing.T) {
	remote, devs := fixture.NewDevices(t, 1)
	a := devs[0]
	a.Write(t, "a.txt", "v1")
	var compactErr error
	disk.SetBeforePush(a.Disk, func() {
		_, compactErr = a.Disk.Compact(context.Background())
	})

	a.Sync(t)

	if !errors.Is(compactErr, disk.ErrAlreadySyncing) {
		t.Fatalf("compact during a sync = %v, want ErrAlreadySyncing", compactErr)
	}
	remote.AssertFiles(t, map[string]string{"a.txt": "v1"})
}

func TestCompactSquashesOldHistory(t *testing.T) {
	remote, devs := fixture.NewDevices(t, 1)
	a := devs[0]
	now := time.Now()
	remote.CommitAt(t, fixture.CommitSpec{Time: fixture.Ago(now, 90), Files: map[string]string{"a.txt": "1"}})
	remote.CommitAt(t, fixture.CommitSpec{Time: fixture.Ago(now, 60), Files: map[string]string{"a.txt": "2"}})
	remote.CommitAt(t, fixture.CommitSpec{Time: fixture.Ago(now, 1), Files: map[string]string{"a.txt": "3"}})

	res, err := a.Disk.Compact(context.Background())

	if err != nil || !res.Compacted || res.Squashed != 2 {
		t.Fatalf("compact = %+v, %v; want 2 commits squashed", res, err)
	}
	if n := len(remote.Log(t)); n != 2 {
		t.Errorf("remote has %d commits, want snapshot + 1 kept", n)
	}
	a.Sync(t)
	a.AssertFiles(t, map[string]string{"a.txt": "3"})
}
