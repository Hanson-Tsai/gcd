package disk_test

import (
	"context"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/Hanson-Tsai/gcd/internal/config"
	"github.com/Hanson-Tsai/gcd/internal/disk"
	"github.com/Hanson-Tsai/gcd/internal/fixture"
	"github.com/Hanson-Tsai/gcd/internal/scan"
)

func status(t *testing.T, d *fixture.Device) disk.Result {
	t.Helper()
	res, err := d.Disk.Status(context.Background())
	if err != nil {
		t.Fatalf("status on %s: %v", d.Host, err)
	}
	return res
}

// snapshot is everything Status must leave alone: the folder, the remote
// head, the base (state file) and the remote-tracking ref Sync publishes.
type snapshot struct {
	folder   map[string]string
	remote   string
	state    string
	tracking string
}

func takeSnapshot(t *testing.T, remote *fixture.Remote, d *fixture.Device) snapshot {
	t.Helper()
	st, err := os.ReadFile(filepath.Join(d.StateDir, "state.json"))
	if err != nil && !os.IsNotExist(err) {
		t.Fatal(err)
	}
	out, _ := exec.Command("git", "--git-dir="+filepath.Join(d.StateDir, "git"),
		"rev-parse", "--verify", "--quiet", "refs/remotes/origin/main").Output()
	return snapshot{folder: d.Files(t), remote: remote.Head(t), state: string(st),
		tracking: strings.TrimSpace(string(out))}
}

func assertUnchanged(t *testing.T, remote *fixture.Remote, d *fixture.Device, before snapshot) {
	t.Helper()
	if after := takeSnapshot(t, remote, d); !reflect.DeepEqual(after, before) {
		t.Fatalf("status changed state:\n before %+v\n after  %+v", before, after)
	}
}

func assertChanges(t *testing.T, what string, got []disk.Change, want map[string]disk.ChangeKind) {
	t.Helper()
	m := map[string]disk.ChangeKind{}
	for _, c := range got {
		m[c.Path] = c.Kind
	}
	if !reflect.DeepEqual(m, want) {
		t.Errorf("%s = %v, want %v", what, m, want)
	}
}

func TestStatusListsPendingUploadsWithoutSyncing(t *testing.T) {
	remote, devs := fixture.NewDevices(t, 1)
	a := devs[0]
	writeAll(t, a, map[string]string{"keep.md": "k", "edit.md": "1", "gone.md": "g"})
	a.Sync(t)
	a.Write(t, "new.md", "n")
	a.Write(t, "edit.md", "2")
	a.Remove(t, "gone.md")
	before := takeSnapshot(t, remote, a)

	res := status(t, a)

	assertChanges(t, "Uploaded", res.Uploaded, map[string]disk.ChangeKind{
		"new.md": disk.Added, "edit.md": disk.Modified, "gone.md": disk.Deleted})
	assertChanges(t, "Downloaded", res.Downloaded, map[string]disk.ChangeKind{})
	assertUnchanged(t, remote, a, before)
}

func TestStatusFetchesPendingDownloadsAndTheNextSyncStillDownloadsThem(t *testing.T) {
	remote, devs := fixture.NewDevices(t, 2)
	a, b := devs[0], devs[1]
	writeAll(t, a, map[string]string{"edit.md": "1", "gone.md": "g"})
	a.Sync(t)
	b.Sync(t)
	a.Write(t, "new.md", "n")
	a.Write(t, "edit.md", "2")
	a.Remove(t, "gone.md")
	a.Sync(t)
	before := takeSnapshot(t, remote, b)

	res := status(t, b)

	assertChanges(t, "Downloaded", res.Downloaded, map[string]disk.ChangeKind{
		"new.md": disk.Added, "edit.md": disk.Modified, "gone.md": disk.Deleted})
	assertChanges(t, "Uploaded", res.Uploaded, map[string]disk.ChangeKind{})
	assertUnchanged(t, remote, b, before)

	b.Sync(t)
	b.AssertFiles(t, map[string]string{"new.md": "n", "edit.md": "2"})
}

func TestStatusDoesNotHideARemoteUpdateFromWatch(t *testing.T) {
	_, devs := fixture.NewDevices(t, 2)
	a, b := devs[0], devs[1]
	a.Write(t, "a.md", "1")
	a.Sync(t)
	b.Sync(t)
	a.Write(t, "a.md", "2")
	a.Sync(t)

	status(t, b)

	changed, err := b.Disk.RemoteChanged(context.Background())
	if err != nil || !changed {
		t.Fatalf("RemoteChanged after status = %v, %v; want true so watch still syncs", changed, err)
	}
}

func TestStatusShowsConflictsWithTheCopySyncWouldMake(t *testing.T) {
	remote, devs := fixture.NewDevices(t, 2)
	a, b := devs[0], devs[1]
	a.Write(t, "plan.md", "v1")
	a.Sync(t)
	b.Sync(t)
	a.Write(t, "plan.md", "from a")
	a.Sync(t)
	b.Write(t, "plan.md", "from b")
	before := takeSnapshot(t, remote, b)

	res := status(t, b)

	assertUnchanged(t, remote, b, before)
	if len(res.Conflicts) != 1 || res.Conflicts[0].Path != "plan.md" {
		t.Fatalf("Conflicts = %v, want plan.md", res.Conflicts)
	}
	synced := b.Sync(t)
	if !reflect.DeepEqual(synced.Conflicts, res.Conflicts) {
		t.Errorf("sync made conflicts %v, status predicted %v", synced.Conflicts, res.Conflicts)
	}
}

func TestStatusShowsRejectedFilesAndWhy(t *testing.T) {
	remote, devs := fixture.NewDevices(t, 1)
	a := devs[0]
	remote.Commit(t, map[string]string{
		".gcd/config.toml": "format = 1\nmax_file_mb = 1\n",
		"Notes.md":         "remote",
		"notes.md":         "remote too",
	}, "other client")
	a.Write(t, "big.bin", strings.Repeat("x", 1<<20+1))
	a.Write(t, "ok.md", "fine")
	before := takeSnapshot(t, remote, a)

	res := status(t, a)

	assertUnchanged(t, remote, a, before)
	assertRejected(t, res, map[string]rejected{
		"big.bin":  {scan.TooLarge, false},
		"Notes.md": {scan.CaseCollision, true},
		"notes.md": {scan.CaseCollision, true},
	})
	assertChanges(t, "Uploaded", res.Uploaded, map[string]disk.ChangeKind{"ok.md": disk.Added})
}

func TestStatusShowsWhenTheNextSyncNeedsYes(t *testing.T) {
	remote, devs := fixture.NewDevices(t, 1)
	a := devs[0]
	all := files(10)
	writeAll(t, a, all)
	a.Sync(t)
	for p := range all {
		a.Remove(t, p)
	}
	before := takeSnapshot(t, remote, a)

	res := status(t, a)

	assertUnchanged(t, remote, a, before)
	if !res.NeedsConfirm || res.PendingDeletes != 10 {
		t.Errorf("NeedsConfirm, PendingDeletes = %v, %d; want true, 10", res.NeedsConfirm, res.PendingDeletes)
	}
	if len(res.Uploaded) != 10 {
		t.Errorf("%d pending uploads, want the 10 deletions listed", len(res.Uploaded))
	}
}

func TestStatusRefusesARepoWithANewerFormat(t *testing.T) {
	remote, devs := fixture.NewDevices(t, 1)
	a := devs[0]
	remote.Commit(t, map[string]string{".gcd/config.toml": "format = 2\n"}, "upgrade repo")

	res, err := a.Disk.Status(context.Background())

	if !errors.Is(err, config.ErrFormatTooNew) || !res.FormatTooNew {
		t.Fatalf("status = %v, FormatTooNew %v; want ErrFormatTooNew", err, res.FormatTooNew)
	}
}

func TestStatusRunsWhileASyncHoldsTheLock(t *testing.T) {
	remote, devs := fixture.NewDevices(t, 1)
	a := devs[0]
	a.Write(t, "a.md", "a")
	var res disk.Result
	var err error
	disk.SetBeforePush(a.Disk, func() {
		res, err = reopen(t, remote, a).Status(context.Background())
	})

	a.Sync(t)

	if err != nil {
		t.Fatalf("status during sync: %v", err)
	}
	assertChanges(t, "Uploaded", res.Uploaded, map[string]disk.ChangeKind{"a.md": disk.Added})
}
