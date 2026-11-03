package disk_test

import (
	"os"
	"testing"

	"github.com/Hanson-Tsai/gcd/internal/disk"
	"github.com/Hanson-Tsai/gcd/internal/fixture"
)

// Safe writes while files are being edited (ticket #8). Each test edits the
// folder after the scan through disk.SetAfterScan.

// editAfterScan makes d's next sync run edit once, right after its scan.
func editAfterScan(d *fixture.Device, edit func()) {
	disk.SetAfterScan(d.Disk, func() {
		disk.SetAfterScan(d.Disk, nil)
		edit()
	})
}

func TestFileEditedAfterScanIsUploadedNextRound(t *testing.T) {
	remote, devs := fixture.NewDevices(t, 1)
	a := devs[0]
	a.Write(t, "doc.md", "v1")
	a.Write(t, "other.md", "o1")
	a.Sync(t)

	a.Write(t, "doc.md", "v2 half")
	a.Write(t, "other.md", "o2")
	editAfterScan(a, func() { a.Write(t, "doc.md", "v2 half written, now complete") })
	res := a.Sync(t)

	if len(res.Uploaded) != 1 || res.Uploaded[0].Path != "other.md" {
		t.Errorf("uploaded = %+v, want only other.md", res.Uploaded)
	}
	if len(res.Skipped) != 1 || res.Skipped[0] != "doc.md" {
		t.Errorf("skipped = %v, want [doc.md]", res.Skipped)
	}
	remote.AssertFiles(t, map[string]string{"doc.md": "v1", "other.md": "o2"})

	a.Sync(t)
	want := map[string]string{"doc.md": "v2 half written, now complete", "other.md": "o2"}
	remote.AssertFiles(t, want)
	a.AssertFiles(t, want)
}

func TestFileRewrittenWithSameSizeAndMtimeAfterScanIsNotUploaded(t *testing.T) {
	remote, devs := fixture.NewDevices(t, 1)
	a := devs[0]
	a.Write(t, "doc.md", "v1")
	a.Sync(t)

	a.Write(t, "doc.md", "v2")
	scanned, err := os.Stat(a.Path("doc.md"))
	if err != nil {
		t.Fatal(err)
	}
	editAfterScan(a, func() {
		a.Write(t, "doc.md", "v3")
		if err := os.Chtimes(a.Path("doc.md"), scanned.ModTime(), scanned.ModTime()); err != nil {
			t.Fatal(err)
		}
	})
	res := a.Sync(t)

	if len(res.Uploaded) != 0 || res.Commit != "" {
		t.Errorf("result = %+v, want nothing uploaded", res)
	}
	remote.AssertFiles(t, map[string]string{"doc.md": "v1"})
}

func TestNewFileDeletedAfterScanIsNotUploaded(t *testing.T) {
	remote, devs := fixture.NewDevices(t, 1)
	a := devs[0]
	a.Write(t, "keep.md", "k")
	a.Write(t, "scratch.md", "s")

	editAfterScan(a, func() { a.Remove(t, "scratch.md") })
	a.Sync(t)

	remote.AssertFiles(t, map[string]string{"keep.md": "k"})
	a.Sync(t)
	remote.AssertFiles(t, map[string]string{"keep.md": "k"})
}

func TestDownloadKeepsLocalEditMadeAfterScanAsConflictCopy(t *testing.T) {
	remote, a, b := synced(t, map[string]string{"doc.md": "v1"})
	a.Write(t, "doc.md", "v2 by a")
	a.Sync(t)

	editAfterScan(b, func() { b.Write(t, "doc.md", "v2 by b, typed during sync") })
	res, dates := syncConflicts(t, b)

	assertConflicts(t, res, dates, map[string]string{"doc.md": "doc (conflict from host2 <date>).md"})
	want := map[string]string{"doc.md": "v2 by a", res.Conflicts[0].Copy: "v2 by b, typed during sync"}
	b.AssertFiles(t, want)
	converge(t, remote, a, b, want)
}

func TestDownloadKeepsFileCreatedAfterScanAsConflictCopy(t *testing.T) {
	remote, a, b := synced(t, map[string]string{"old.md": "o"})
	a.Write(t, "new.md", "from a")
	a.Sync(t)

	editAfterScan(b, func() { b.Write(t, "new.md", "from b") })
	res, dates := syncConflicts(t, b)

	assertConflicts(t, res, dates, map[string]string{"new.md": "new (conflict from host2 <date>).md"})
	converge(t, remote, a, b, map[string]string{
		"old.md": "o", "new.md": "from a", res.Conflicts[0].Copy: "from b",
	})
}

func TestRemoteDeleteKeepsLocalEditMadeAfterScan(t *testing.T) {
	remote, a, b := synced(t, map[string]string{"doc.md": "v1", "other.md": "o"})
	a.Remove(t, "doc.md")
	a.Sync(t)

	editAfterScan(b, func() { b.Write(t, "doc.md", "v2 by b") })
	res := b.Sync(t)

	if len(res.Skipped) != 1 || res.Skipped[0] != "doc.md" {
		t.Errorf("skipped = %v, want [doc.md]", res.Skipped)
	}
	if len(res.Downloaded) != 0 {
		t.Errorf("downloaded = %+v, want none", res.Downloaded)
	}
	converge(t, remote, a, b, map[string]string{"doc.md": "v2 by b", "other.md": "o"})
}

func TestConflictedFileEditedAfterScanWaitsForNextRound(t *testing.T) {
	remote, a, b := synced(t, map[string]string{"doc.md": "v1"})
	a.Write(t, "doc.md", "v2 by a")
	a.Sync(t)
	b.Write(t, "doc.md", "v2 by b")

	editAfterScan(b, func() { b.Write(t, "doc.md", "v3 by b") })
	res := b.Sync(t)

	if len(res.Conflicts) != 0 || len(res.Skipped) != 1 {
		t.Fatalf("result = %+v, want doc.md skipped and no conflict yet", res)
	}
	b.AssertFiles(t, map[string]string{"doc.md": "v3 by b"})

	res, dates := syncConflicts(t, b)
	assertConflicts(t, res, dates, map[string]string{"doc.md": "doc (conflict from host2 <date>).md"})
	converge(t, remote, a, b, map[string]string{"doc.md": "v2 by a", res.Conflicts[0].Copy: "v3 by b"})
}

func TestLeftoverTempFilesAreRemovedAndNeverUploaded(t *testing.T) {
	remote, devs := fixture.NewDevices(t, 1)
	a := devs[0]
	a.Write(t, "doc.md", "v1")
	a.Write(t, "sub/.gcd-tmp-123", "half a download")
	a.Write(t, ".gcd-tmp-456", "half a download")

	a.Sync(t)

	a.AssertFiles(t, map[string]string{"doc.md": "v1"})
	remote.AssertFiles(t, map[string]string{"doc.md": "v1"})
}

func TestFileRecreatedAfterScanIsNotDeletedRemotely(t *testing.T) {
	remote, devs := fixture.NewDevices(t, 1)
	a := devs[0]
	a.Write(t, "doc.md", "v1")
	a.Sync(t)

	a.Remove(t, "doc.md")
	editAfterScan(a, func() { a.Write(t, "doc.md", "v2 saved by editor") })
	res := a.Sync(t)

	if len(res.Skipped) != 1 || res.Commit != "" {
		t.Errorf("result = %+v, want doc.md skipped and no commit", res)
	}
	remote.AssertFiles(t, map[string]string{"doc.md": "v1"})
	a.Sync(t)
	remote.AssertFiles(t, map[string]string{"doc.md": "v2 saved by editor"})
}
