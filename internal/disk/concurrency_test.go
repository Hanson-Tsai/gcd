package disk_test

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"testing"

	"github.com/Hanson-Tsai/gcd/internal/disk"
	"github.com/Hanson-Tsai/gcd/internal/fixture"
)

func TestRejectedPushIsRetriedAndDevicesConverge(t *testing.T) {
	remote, devs := fixture.NewDevices(t, 2)
	a, b := devs[0], devs[1]

	a.Write(t, "from-a.txt", "a")
	b.Write(t, "from-b.txt", "b")
	interleaved := false
	disk.SetBeforePush(a.Disk, func() {
		if !interleaved {
			interleaved = true
			b.Sync(t) // b pushes after a fetched: a's first push is rejected
		}
	})
	res := a.Sync(t)
	b.Sync(t)

	want := map[string]string{"from-a.txt": "a", "from-b.txt": "b"}
	remote.AssertFiles(t, want)
	a.AssertFiles(t, want)
	b.AssertFiles(t, want)
	if len(res.Downloaded) != 1 || res.Downloaded[0].Path != "from-b.txt" {
		t.Errorf("a downloaded %+v, want from-b.txt", res.Downloaded)
	}
	log := remote.Log(t)
	if len(log) != 2 {
		t.Fatalf("got %d commits, want 2 (one per device)", len(log))
	}
	if res.Commit != log[0].Hash || res.BaseTree != log[0].Tree {
		t.Errorf("a result commit/base = %s/%s, want remote head %s/%s", res.Commit, res.BaseTree, log[0].Hash, log[0].Tree)
	}
}

func TestConcurrentSyncsFromSeveralDevicesConverge(t *testing.T) {
	// With n devices syncing once each at the same time, a device loses at
	// most n-1 races, which is within the retry limit.
	const n = 3
	remote, devs := fixture.NewDevices(t, n)
	want := map[string]string{}
	for _, d := range devs {
		d.Write(t, d.Host+".txt", d.Host)
		want[d.Host+".txt"] = d.Host
	}

	var wg sync.WaitGroup
	errs := make([]error, n)
	for i, d := range devs {
		wg.Add(1)
		go func() {
			defer wg.Done()
			_, errs[i] = d.Disk.Sync(context.Background(), disk.SyncOptions{})
		}()
	}
	wg.Wait()
	for i, err := range errs {
		if err != nil {
			t.Fatalf("concurrent sync on %s: %v", devs[i].Host, err)
		}
	}

	for _, d := range devs {
		d.Sync(t)
	}
	remote.AssertFiles(t, want)
	for _, d := range devs {
		d.AssertFiles(t, want)
	}
	if got := len(remote.Log(t)); got != n {
		t.Errorf("got %d commits, want %d (one per device)", got, n)
	}
}

func TestPushRetriesAreBoundedAndLeaveTheFolderAlone(t *testing.T) {
	remote, devs := fixture.NewDevices(t, 2)
	a, b := devs[0], devs[1]

	a.Write(t, "from-a.txt", "a")
	n := 0
	disk.SetBeforePush(a.Disk, func() {
		n++ // b pushes before every one of a's pushes
		b.Write(t, fmt.Sprintf("b%d.txt", n), "b")
		b.Sync(t)
	})
	_, err := a.Disk.Sync(context.Background(), disk.SyncOptions{})
	if !errors.Is(err, disk.ErrRemoteChanging) {
		t.Fatalf("sync err = %v, want ErrRemoteChanging", err)
	}
	if n < 2 || n > 10 {
		t.Errorf("a attempted %d pushes, want a small retry limit > 1", n)
	}
	a.AssertFiles(t, map[string]string{"from-a.txt": "a"})
	if _, ok := remote.Files(t)["from-a.txt"]; ok {
		t.Error("from-a.txt reached the remote although every push was rejected")
	}

	disk.SetBeforePush(a.Disk, nil)
	a.Sync(t)
	b.Sync(t)
	want := remote.Files(t)
	if want["from-a.txt"] != "a" || len(want) != n+1 {
		t.Fatalf("remote after retry = %q, want from-a.txt plus %d files from b", want, n)
	}
	a.AssertFiles(t, want)
	b.AssertFiles(t, want)
}
