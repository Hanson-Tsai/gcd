package watch_test

import (
	"context"
	"errors"
	"fmt"
	"os"
	"slices"
	"sync"
	"testing"
	"time"

	"github.com/Hanson-Tsai/gcd/internal/config"
	"github.com/Hanson-Tsai/gcd/internal/disk"
	"github.com/Hanson-Tsai/gcd/internal/fixture"
	"github.com/Hanson-Tsai/gcd/internal/watch"
)

// Short timings keep the smoke tests fast; production reads them from the
// repo config (10 s debounce, 60 s poll).
const (
	debounce = 100 * time.Millisecond
	poll     = 200 * time.Millisecond
	deadline = 15 * time.Second
)

// session is one running watch.Run plus the events it reported.
type session struct {
	cancel context.CancelFunc
	done   chan error

	stopped bool

	mu     sync.Mutex
	events []watch.Event
}

func start(t *testing.T, targets ...watch.Target) *session {
	t.Helper()
	return startWith(t, watch.Options{Debounce: debounce, Poll: poll}, targets...)
}

// startWith runs watch.Run with opts (Report is set here) and waits until
// every target finished its first sync attempt.
func startWith(t *testing.T, opts watch.Options, targets ...watch.Target) *session {
	t.Helper()
	ctx, cancel := context.WithCancel(context.Background())
	s := &session{cancel: cancel, done: make(chan error, 1)}
	opts.Report = func(e watch.Event) {
		s.mu.Lock()
		s.events = append(s.events, e)
		s.mu.Unlock()
	}
	go func() { s.done <- watch.Run(ctx, targets, opts) }()
	t.Cleanup(func() { s.stop(t) })
	for _, tg := range targets {
		eventually(t, tg.Name+"'s first sync", func() bool { return s.count(tg.Name, watch.Synced, watch.Failed) > 0 })
	}
	return s
}

// stop cancels the watch (like Ctrl+C) and waits for Run to return. It may
// be called more than once.
func (s *session) stop(t *testing.T) {
	t.Helper()
	s.cancel()
	if s.stopped {
		return
	}
	select {
	case err := <-s.done:
		s.stopped = true
		if err != nil {
			t.Errorf("watch.Run returned %v after cancel, want nil", err)
		}
	case <-time.After(deadline):
		t.Fatal("watch.Run did not return after cancel")
	}
}

// eventually polls cond until it holds or the deadline passes.
func eventually(t *testing.T, what string, cond func() bool) {
	t.Helper()
	end := time.Now().Add(deadline)
	for !cond() {
		if time.Now().After(end) {
			t.Fatalf("timed out waiting for %s", what)
		}
		time.Sleep(20 * time.Millisecond)
	}
}

// count returns how many events of the given kinds disk name reported.
func (s *session) count(name string, kinds ...watch.Kind) int {
	s.mu.Lock()
	defer s.mu.Unlock()
	n := 0
	for _, e := range s.events {
		if e.Disk == name && slices.Contains(kinds, e.Kind) {
			n++
		}
	}
	return n
}

// find returns the first event of kind for disk name.
func (s *session) find(name string, kind watch.Kind) (watch.Event, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	for _, e := range s.events {
		if e.Disk == name && e.Kind == kind {
			return e, true
		}
	}
	return watch.Event{}, false
}

func (s *session) dump() string {
	s.mu.Lock()
	defer s.mu.Unlock()
	out := ""
	for _, e := range s.events {
		out += fmt.Sprintf("  %s %v err=%v\n", e.Disk, e.Kind, e.Err)
	}
	return out
}

func remoteHas(t *testing.T, r *fixture.Remote, path, content string) func() bool {
	return func() bool { return r.Files(t)[path] == content }
}

func TestLocalChangesAreUploadedAfterTheDebounce(t *testing.T) {
	remote, devs := fixture.NewDevices(t, 2)
	a, b := devs[0], devs[1]
	start(t, watch.Target{Name: "a", Disk: a.Disk})

	a.Write(t, "notes/today.md", "first")
	eventually(t, "the remote to get notes/today.md", remoteHas(t, remote, "notes/today.md", "first"))
	a.Write(t, "notes/today.md", "second")
	eventually(t, "the remote to get the edit", remoteHas(t, remote, "notes/today.md", "second"))

	b.Sync(t)
	b.AssertFiles(t, map[string]string{"notes/today.md": "second"})
}

func TestTimingComesFromTheRepoConfig(t *testing.T) {
	remote, devs := fixture.NewDevices(t, 1)
	a := devs[0]
	remote.Commit(t, map[string]string{config.Path: "debounce_seconds = 1\npoll_seconds = 1\n"}, "fast config")
	startWith(t, watch.Options{}, watch.Target{Name: "a", Disk: a.Disk})

	began := time.Now()
	a.Write(t, "x.md", "x")
	eventually(t, "the remote to get x.md", remoteHas(t, remote, "x.md", "x"))
	// The default debounce is 10 s; the repo config's 1 s must be in effect.
	if took := time.Since(began); took > 6*time.Second {
		t.Errorf("upload took %v with debounce_seconds = 1, want well under the 10 s default", took)
	}
}

func TestRemoteChangesAreDownloadedByPolling(t *testing.T) {
	_, devs := fixture.NewDevices(t, 2)
	a, b := devs[0], devs[1]
	start(t, watch.Target{Name: "a", Disk: a.Disk})

	b.Write(t, "from-b.txt", "hello")
	b.Sync(t)
	eventually(t, "a to download from-b.txt", func() bool { return a.Files(t)["from-b.txt"] == "hello" })

	b.Remove(t, "from-b.txt")
	b.Sync(t)
	eventually(t, "a to delete from-b.txt", func() bool { _, ok := a.Files(t)["from-b.txt"]; return !ok })
}

func TestABurstOfSavesBecomesOneCommit(t *testing.T) {
	remote, devs := fixture.NewDevices(t, 1)
	a := devs[0]
	startWith(t, watch.Options{Debounce: 700 * time.Millisecond, Poll: poll}, watch.Target{Name: "a", Disk: a.Disk})
	commits := len(remote.Log(t))

	for i := range 5 {
		a.Write(t, "draft.md", fmt.Sprintf("version %d", i))
		time.Sleep(20 * time.Millisecond)
	}
	eventually(t, "the remote to get the last version", remoteHas(t, remote, "draft.md", "version 4"))
	time.Sleep(time.Second) // room for a (wrong) second sync

	if got := len(remote.Log(t)) - commits; got != 1 {
		t.Errorf("a burst of saves made %d commits, want 1", got)
	}
}

func TestAMassDeleteIsSkippedWithAWarning(t *testing.T) {
	remote, devs := fixture.NewDevices(t, 1)
	a := devs[0]
	for i := range 10 {
		a.Write(t, fmt.Sprintf("f%d.md", i), "x")
	}
	a.Sync(t)
	s := start(t, watch.Target{Name: "a", Disk: a.Disk})

	for i := range 10 {
		a.Remove(t, fmt.Sprintf("f%d.md", i))
	}
	eventually(t, "a needs-confirm event", func() bool { return s.count("a", watch.NeedsConfirm) > 0 })
	e, _ := s.find("a", watch.NeedsConfirm)
	if !errors.Is(e.Err, disk.ErrMassDelete) || e.Result.PendingDeletes != 10 {
		t.Errorf("needs-confirm event: err %v, %d pending deletes; want ErrMassDelete and 10", e.Err, e.Result.PendingDeletes)
	}
	time.Sleep(4 * poll) // later polls must not delete either
	if n := len(remote.Files(t)); n < 10 {
		t.Fatalf("remote has %d files after the refused sync, want all 10 kept", n)
	}
	if n := s.count("a", watch.NeedsConfirm); n != 1 {
		t.Errorf("warned %d times about the same mass delete, want once", n)
	}
}

func TestAFailingDiskDoesNotStopTheOthers(t *testing.T) {
	broken, devs := fixture.NewDevices(t, 1)
	bad := devs[0]
	good := fixture.NewDevice(t, fixture.NewRemote(t), "host1")
	if err := os.RemoveAll(broken.Dir); err != nil {
		t.Fatal(err)
	}
	s := start(t, watch.Target{Name: "bad", Disk: bad.Disk}, watch.Target{Name: "good", Disk: good.Disk})

	bad.Write(t, "lost.txt", "x")
	good.Write(t, "kept.txt", "y")
	eventually(t, "good to sync kept.txt", func() bool {
		s.mu.Lock()
		defer s.mu.Unlock()
		for _, e := range s.events {
			if e.Disk == "good" && e.Kind == watch.Synced && len(e.Result.Uploaded) > 0 {
				return true
			}
		}
		return false
	})
	if s.count("bad", watch.Failed) == 0 {
		t.Errorf("no failure reported for the disk whose remote is gone; events:\n%s", s.dump())
	}
}

func TestWatchAndManualSyncTakeTurns(t *testing.T) {
	remote, devs := fixture.NewDevices(t, 1)
	a := devs[0]
	// A second Disk value on the same folder and state dir stands in for a
	// `gcd sync` run in another terminal.
	other, err := disk.Open(disk.Options{Folder: a.Folder, Remote: remote.URL, StateDir: a.StateDir, Host: "host1"})
	if err != nil {
		t.Fatal(err)
	}
	s := start(t, watch.Target{Name: "a", Disk: a.Disk})

	busy := 0
	for i := range 10 {
		a.Write(t, fmt.Sprintf("n%d.md", i), "x")
		if _, err := other.Sync(context.Background(), disk.SyncOptions{}); errors.Is(err, disk.ErrAlreadySyncing) {
			busy++
		} else if err != nil {
			t.Fatalf("manual sync: %v", err)
		}
		time.Sleep(debounce)
	}
	eventually(t, "all files on the remote", func() bool { return len(remote.Files(t)) >= 10 })
	if n := s.count("a", watch.Failed); n != 0 {
		t.Errorf("watch reported %d failures next to manual syncs; events:\n%s", n, s.dump())
	}
	t.Logf("manual syncs that found watch syncing: %d", busy)
}

func TestStoppingFinishesTheRunningSync(t *testing.T) {
	_, devs := fixture.NewDevices(t, 2)
	a, b := devs[0], devs[1]
	s := start(t, watch.Target{Name: "a", Disk: a.Disk})

	for i := range 20 {
		a.Write(t, fmt.Sprintf("dir%d/f.md", i), "x")
	}
	time.Sleep(debounce + 50*time.Millisecond) // the debounced sync is about to run or running
	s.stop(t)

	// Run returned, so no sync is in flight any more: a manual sync does not
	// find the Disk busy, and both devices end up with every file.
	a.Sync(t)
	b.Sync(t)
	if got := len(b.Files(t)); got != 20 {
		t.Errorf("b has %d files after stop and a manual sync, want 20", got)
	}
}
