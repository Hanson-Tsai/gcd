package disk_test

import (
	"context"
	"errors"
	"reflect"
	"testing"
	"time"

	"github.com/Hanson-Tsai/gcd/internal/disk"
	"github.com/Hanson-Tsai/gcd/internal/fixture"
)

func logOf(t *testing.T, d *fixture.Device, path string) []disk.LogEntry {
	t.Helper()
	log, err := d.Disk.Log(context.Background(), path)
	if err != nil {
		t.Fatalf("log %q on %s: %v", path, d.Host, err)
	}
	return log
}

func TestLogListsWholeDiskHistoryNewestFirst(t *testing.T) {
	remote, devs := fixture.NewDevices(t, 2)
	a, b := devs[0], devs[1]

	a.Write(t, "a.txt", "1")
	a.Write(t, "notes/b.md", "2")
	a.Sync(t)
	b.Sync(t)
	b.Write(t, "a.txt", "1 changed")
	b.Remove(t, "notes/b.md")
	b.Sync(t)

	// a has not synced since b's commit: Log still shows the remote history.
	log := logOf(t, a, "")
	commits := remote.Log(t)
	if len(log) != 2 {
		t.Fatalf("got %d log entries, want 2: %+v", len(log), log)
	}
	if log[0].Commit != commits[0].Hash || log[1].Commit != commits[1].Hash {
		t.Errorf("log commits = %s, %s; want remote's %s, %s", log[0].Commit, log[1].Commit, commits[0].Hash, commits[1].Hash)
	}
	if log[0].Host != "host2" || log[1].Host != "host1" {
		t.Errorf("hosts = %q, %q; want host2, host1", log[0].Host, log[1].Host)
	}
	if log[0].Time.IsZero() || log[0].Time.Before(log[1].Time) {
		t.Errorf("times = %v, %v; want non-zero, newest first", log[0].Time, log[1].Time)
	}
	wantNewest := []disk.Change{{Path: "a.txt", Kind: disk.Modified}, {Path: "notes/b.md", Kind: disk.Deleted}}
	if !reflect.DeepEqual(log[0].Changes, wantNewest) {
		t.Errorf("newest changes = %+v, want %+v", log[0].Changes, wantNewest)
	}
	wantOldest := []disk.Change{{Path: "a.txt", Kind: disk.Added}, {Path: "notes/b.md", Kind: disk.Added}}
	if !reflect.DeepEqual(log[1].Changes, wantOldest) {
		t.Errorf("oldest changes = %+v, want %+v", log[1].Changes, wantOldest)
	}
}

func TestLogPathShowsOnlyCommitsTouchingIt(t *testing.T) {
	_, devs := fixture.NewDevices(t, 1)
	a := devs[0]

	a.Write(t, "keep.txt", "1")
	a.Write(t, "gone [1].txt", "x") // glob characters are matched literally
	a.Write(t, "gone 1.txt", "y")
	a.Sync(t)
	a.Write(t, "keep.txt", "2")
	a.Write(t, "gone 1.txt", "y2")
	a.Sync(t)
	a.Remove(t, "gone [1].txt")
	a.Sync(t)

	log := logOf(t, a, "gone [1].txt")
	if len(log) != 2 {
		t.Fatalf("got %d entries, want 2 (add, delete): %+v", len(log), log)
	}
	if want := []disk.Change{{Path: "gone [1].txt", Kind: disk.Deleted}}; !reflect.DeepEqual(log[0].Changes, want) {
		t.Errorf("newest changes = %+v, want %+v", log[0].Changes, want)
	}
	if want := []disk.Change{{Path: "gone [1].txt", Kind: disk.Added}}; !reflect.DeepEqual(log[1].Changes, want) {
		t.Errorf("oldest changes = %+v, want %+v", log[1].Changes, want)
	}

	if log := logOf(t, a, "never-existed.txt"); len(log) != 0 {
		t.Errorf("log of unknown path = %+v, want none", log)
	}
}

func TestLogDirectoryPath(t *testing.T) {
	_, devs := fixture.NewDevices(t, 1)
	a := devs[0]
	a.Write(t, "notes/a.md", "a")
	a.Write(t, "other.txt", "o")
	a.Sync(t)
	a.Write(t, "other.txt", "o2")
	a.Sync(t)
	a.Write(t, "notes/sub/b.md", "b")
	a.Sync(t)

	log := logOf(t, a, "notes")
	if len(log) != 2 {
		t.Fatalf("got %d entries, want 2: %+v", len(log), log)
	}
	if want := []disk.Change{{Path: "notes/sub/b.md", Kind: disk.Added}}; !reflect.DeepEqual(log[0].Changes, want) {
		t.Errorf("newest changes = %+v, want %+v", log[0].Changes, want)
	}
}

func restore(t *testing.T, d *fixture.Device, path string, opts disk.RestoreOptions) disk.RestoreResult {
	t.Helper()
	res, err := d.Disk.Restore(context.Background(), path, opts)
	if err != nil {
		t.Fatalf("restore %q on %s: %v", path, d.Host, err)
	}
	return res
}

func TestRestoreDeletedFileAtCommitUploadsOnNextSync(t *testing.T) {
	remote, devs := fixture.NewDevices(t, 2)
	a, b := devs[0], devs[1]

	a.Write(t, "notes/plan.md", "v1")
	a.Sync(t)
	a.Write(t, "notes/plan.md", "v2")
	a.Sync(t)
	v2 := remote.Head(t)
	a.Remove(t, "notes/plan.md")
	a.SyncYes(t)
	b.SyncYes(t)
	b.AssertFiles(t, map[string]string{})

	// b restores the deleted file from the commit that held v2.
	res := restore(t, b, "notes/plan.md", disk.RestoreOptions{At: disk.At{Commit: v2[:7]}})
	if res.Commit != v2 {
		t.Errorf("restored from %s, want %s", res.Commit, v2)
	}
	b.AssertFiles(t, map[string]string{"notes/plan.md": "v2"})

	up := b.Sync(t)
	if want := []disk.Change{{Path: "notes/plan.md", Kind: disk.Added}}; !reflect.DeepEqual(up.Uploaded, want) {
		t.Errorf("sync after restore uploaded %+v, want %+v", up.Uploaded, want)
	}
	a.Sync(t)
	a.AssertFiles(t, map[string]string{"notes/plan.md": "v2"})
	remote.AssertFiles(t, map[string]string{"notes/plan.md": "v2"})
}

func TestRestoreWithoutAtTakesNewestVersionBeforeDeletion(t *testing.T) {
	_, devs := fixture.NewDevices(t, 1)
	a := devs[0]
	a.Write(t, "a.txt", "v1")
	a.Sync(t)
	a.Write(t, "a.txt", "v2")
	a.Sync(t)
	a.Remove(t, "a.txt")
	a.Write(t, "other.txt", "o")
	a.SyncYes(t)
	a.Write(t, "other.txt", "o2")
	a.Sync(t)

	restore(t, a, "a.txt", disk.RestoreOptions{})
	a.AssertFiles(t, map[string]string{"a.txt": "v2", "other.txt": "o2"})
}

// nextSecond waits until the wall clock enters a new second, so the next
// commit's (second-resolution) time is later than every earlier one.
func nextSecond() {
	now := time.Now()
	time.Sleep(now.Truncate(time.Second).Add(time.Second).Sub(now))
}

func TestRestoreAtTime(t *testing.T) {
	_, devs := fixture.NewDevices(t, 1)
	a := devs[0]
	a.Write(t, "a.txt", "v1")
	a.Sync(t)
	nextSecond()
	a.Write(t, "a.txt", "v2")
	a.Sync(t)
	nextSecond()
	a.Remove(t, "a.txt")
	a.SyncYes(t)

	log := logOf(t, a, "")
	t1, t2, t3 := log[2].Time, log[1].Time, log[0].Time

	restore(t, a, "a.txt", disk.RestoreOptions{At: disk.At{Time: t1}})
	a.AssertFiles(t, map[string]string{"a.txt": "v1"})

	// Between v2 and the deletion: v2.
	restore(t, a, "a.txt", disk.RestoreOptions{At: disk.At{Time: t2.Add(500 * time.Millisecond)}, Force: true})
	a.AssertFiles(t, map[string]string{"a.txt": "v2"})

	a.Remove(t, "a.txt")
	_, err := a.Disk.Restore(context.Background(), "a.txt", disk.RestoreOptions{At: disk.At{Time: t3}})
	if !errors.Is(err, disk.ErrPathNotFound) {
		t.Errorf("restore after deletion: err = %v, want ErrPathNotFound", err)
	}
	_, err = a.Disk.Restore(context.Background(), "a.txt", disk.RestoreOptions{At: disk.At{Time: t1.Add(-time.Second)}})
	if !errors.Is(err, disk.ErrBeforeRetention) {
		t.Errorf("restore before oldest commit: err = %v, want ErrBeforeRetention", err)
	}
	a.AssertFiles(t, map[string]string{})
}

func TestRestoreRejectsUnknownCommitAndPath(t *testing.T) {
	_, devs := fixture.NewDevices(t, 1)
	a := devs[0]
	a.Write(t, "a.txt", "v1")
	a.Write(t, "dir/b.txt", "b")
	a.Sync(t)
	ctx := context.Background()

	if _, err := a.Disk.Restore(ctx, "a.txt", disk.RestoreOptions{At: disk.At{Commit: "deadbeef"}}); !errors.Is(err, disk.ErrUnknownCommit) {
		t.Errorf("unknown commit: err = %v, want ErrUnknownCommit", err)
	}
	if _, err := a.Disk.Restore(ctx, "nope.txt", disk.RestoreOptions{}); !errors.Is(err, disk.ErrPathNotFound) {
		t.Errorf("unknown path: err = %v, want ErrPathNotFound", err)
	}
	for _, p := range []string{"", "../x", "/etc/passwd", "dir"} {
		if _, err := a.Disk.Restore(ctx, p, disk.RestoreOptions{}); err == nil {
			t.Errorf("restore %q succeeded, want an error", p)
		}
	}
}

func TestRestoreRefusesToOverwriteDifferentLocalFile(t *testing.T) {
	remote, devs := fixture.NewDevices(t, 1)
	a := devs[0]
	a.Write(t, "a.txt", "v1")
	a.Sync(t)
	v1 := remote.Head(t)
	a.Write(t, "a.txt", "v2")
	a.Sync(t)
	ctx := context.Background()

	if _, err := a.Disk.Restore(ctx, "a.txt", disk.RestoreOptions{At: disk.At{Commit: v1}}); !errors.Is(err, disk.ErrExists) {
		t.Fatalf("err = %v, want ErrExists", err)
	}
	a.AssertFiles(t, map[string]string{"a.txt": "v2"})

	res := restore(t, a, "a.txt", disk.RestoreOptions{At: disk.At{Commit: v1}, Force: true})
	if res.Unchanged {
		t.Error("forced restore reported Unchanged")
	}
	a.AssertFiles(t, map[string]string{"a.txt": "v1"})

	// Same content already present: nothing to do, no --force needed.
	if res := restore(t, a, "a.txt", disk.RestoreOptions{At: disk.At{Commit: v1}}); !res.Unchanged {
		t.Error("restoring identical content did not report Unchanged")
	}

	up := a.Sync(t)
	if want := []disk.Change{{Path: "a.txt", Kind: disk.Modified}}; !reflect.DeepEqual(up.Uploaded, want) {
		t.Errorf("sync after restore uploaded %+v, want %+v", up.Uploaded, want)
	}
	remote.AssertFiles(t, map[string]string{"a.txt": "v1"})
}

func TestParseAt(t *testing.T) {
	loc := time.FixedZone("X", 8*3600)
	cases := []struct {
		in   string
		want disk.At
	}{
		{"2026-11-03T10:20:30Z", disk.At{Time: time.Date(2026, 11, 3, 10, 20, 30, 0, time.UTC)}},
		{"2026-11-03 10:20", disk.At{Time: time.Date(2026, 11, 3, 10, 20, 0, 0, loc)}},
		{"2026-11-03 10:20:30", disk.At{Time: time.Date(2026, 11, 3, 10, 20, 30, 0, loc)}},
		{"2026-11-03", disk.At{Time: time.Date(2026, 11, 3, 0, 0, 0, 0, loc)}},
		{"a1b2c3d", disk.At{Commit: "a1b2c3d"}},
	}
	for _, c := range cases {
		got, err := disk.ParseAt(c.in, loc)
		if err != nil || !got.Time.Equal(c.want.Time) || got.Commit != c.want.Commit {
			t.Errorf("ParseAt(%q) = %+v, %v; want %+v", c.in, got, err, c.want)
		}
	}
	for _, bad := range []string{"", "yesterday", "abc", "2026-13-01"} {
		if _, err := disk.ParseAt(bad, loc); err == nil {
			t.Errorf("ParseAt(%q) succeeded, want error", bad)
		}
	}
}

func TestLogEmptyRemoteHasNoEntries(t *testing.T) {
	_, devs := fixture.NewDevices(t, 1)
	if log := logOf(t, devs[0], ""); len(log) != 0 {
		t.Errorf("got %+v, want no entries", log)
	}
}
