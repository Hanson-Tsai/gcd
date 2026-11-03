package disk_test

import (
	"strings"
	"testing"
	"time"

	"github.com/Hanson-Tsai/gcd/internal/disk"
	"github.com/Hanson-Tsai/gcd/internal/fixture"
)

// Three-way reconcile scenarios (ticket #4), each played out on two devices
// sharing one remote. Every test ends by checking that both folders and the
// remote hold exactly the same files.

// synced returns two devices that both hold files, already synced.
func synced(t *testing.T, files map[string]string) (*fixture.Remote, *fixture.Device, *fixture.Device) {
	t.Helper()
	remote, devs := fixture.NewDevices(t, 2)
	a, b := devs[0], devs[1]
	for p, c := range files {
		a.Write(t, p, c)
	}
	a.Sync(t)
	b.Sync(t)
	b.AssertFiles(t, files)
	return remote, a, b
}

// converge syncs a then b then a again and asserts every side holds want.
func converge(t *testing.T, remote *fixture.Remote, a, b *fixture.Device, want map[string]string) {
	t.Helper()
	a.Sync(t)
	b.Sync(t)
	a.Sync(t)
	a.AssertFiles(t, want)
	b.AssertFiles(t, want)
	remote.AssertFiles(t, want)
}

func TestDeleteOnOneSideDeletesOnTheOther(t *testing.T) {
	remote, a, b := synced(t, map[string]string{"gone.txt": "x", "kept.txt": "y"})

	a.Remove(t, "gone.txt")
	a.Sync(t)
	b.Sync(t)

	want := map[string]string{"kept.txt": "y"}
	b.AssertFiles(t, want)
	converge(t, remote, a, b, want)
}

func TestDeleteOnBothSidesDeletes(t *testing.T) {
	remote, a, b := synced(t, map[string]string{"gone.txt": "x", "kept.txt": "y"})

	a.Remove(t, "gone.txt")
	b.Remove(t, "gone.txt")
	a.Sync(t)
	res := b.Sync(t)

	if len(res.Conflicts) != 0 {
		t.Errorf("conflicts = %+v, want none", res.Conflicts)
	}
	converge(t, remote, a, b, map[string]string{"kept.txt": "y"})
}

func TestSameChangeOnBothSidesIsNotAConflict(t *testing.T) {
	remote, a, b := synced(t, map[string]string{"doc.md": "v1"})

	a.Write(t, "doc.md", "v2")
	b.Write(t, "doc.md", "v2")
	a.Write(t, "new.md", "same")
	b.Write(t, "new.md", "same")
	a.Sync(t)
	res := b.Sync(t)

	if len(res.Conflicts) != 0 || res.Commit != "" {
		t.Errorf("second device result = %+v, want no conflicts and no commit", res)
	}
	converge(t, remote, a, b, map[string]string{"doc.md": "v2", "new.md": "same"})
}

func TestLocalEditBeatsRemoteDelete(t *testing.T) {
	remote, a, b := synced(t, map[string]string{"doc.md": "v1"})

	a.Remove(t, "doc.md")
	a.SyncYes(t)
	b.Write(t, "doc.md", "v2 by b")
	b.Sync(t)

	converge(t, remote, a, b, map[string]string{"doc.md": "v2 by b"})
}

func TestRemoteEditBeatsLocalDelete(t *testing.T) {
	remote, a, b := synced(t, map[string]string{"doc.md": "v1"})

	a.Write(t, "doc.md", "v2 by a")
	a.Sync(t)
	b.Remove(t, "doc.md")
	b.Sync(t)

	b.AssertFiles(t, map[string]string{"doc.md": "v2 by a"})
	converge(t, remote, a, b, map[string]string{"doc.md": "v2 by a"})
}

// syncConflicts runs a sync on d and returns its result plus the dates (as
// "YYYY-MM-DD") that a conflict copy made during it may carry.
func syncConflicts(t *testing.T, d *fixture.Device) (disk.Result, []string) {
	t.Helper()
	before := time.Now().Format("2006-01-02")
	res := d.Sync(t)
	return res, []string{before, time.Now().Format("2006-01-02")}
}

// assertConflicts checks res reported exactly the conflicts want, mapping each
// conflicted path to its expected copy name with "<date>" standing for the
// sync date.
func assertConflicts(t *testing.T, res disk.Result, dates []string, want map[string]string) {
	t.Helper()
	if len(res.Conflicts) != len(want) {
		t.Fatalf("conflicts = %+v, want %d", res.Conflicts, len(want))
	}
	for _, c := range res.Conflicts {
		pattern, ok := want[c.Path]
		if !ok {
			t.Fatalf("unexpected conflict %+v", c)
		}
		if c.Copy != strings.ReplaceAll(pattern, "<date>", dates[0]) &&
			c.Copy != strings.ReplaceAll(pattern, "<date>", dates[1]) {
			t.Errorf("conflict copy of %s = %q, want %q", c.Path, c.Copy, pattern)
		}
	}
}

func TestDifferentChangesOnBothSidesKeepBothVersions(t *testing.T) {
	remote, a, b := synced(t, map[string]string{"notes/plan.md": "v1"})

	a.Write(t, "notes/plan.md", "v2 by a")
	b.Write(t, "notes/plan.md", "v2 by b")
	a.Sync(t)
	res, dates := syncConflicts(t, b)

	assertConflicts(t, res, dates, map[string]string{
		"notes/plan.md": "notes/plan (conflict from host2 <date>).md",
	})
	copy := res.Conflicts[0].Copy
	want := map[string]string{
		"notes/plan.md": "v2 by a",
		copy:            "v2 by b",
	}
	b.AssertFiles(t, want)
	remote.AssertFiles(t, want)
	if got, wantMsg := remote.Log(t)[0].Subject, "sync from host2: +1 ~0 -0"; got != wantMsg {
		t.Errorf("conflict commit subject = %q, want %q", got, wantMsg)
	}
	converge(t, remote, a, b, want)
}

func TestFirstSyncWithFilesOnBothSides(t *testing.T) {
	remote, devs := fixture.NewDevices(t, 2)
	a, b := devs[0], devs[1]
	a.Write(t, "same.txt", "identical")
	a.Write(t, "diff.txt", "from a")
	a.Write(t, "only-a.txt", "a")
	a.Sync(t)
	b.Write(t, "same.txt", "identical")
	b.Write(t, "diff.txt", "from b")
	b.Write(t, "only-b.txt", "b")

	res, dates := syncConflicts(t, b)

	assertConflicts(t, res, dates, map[string]string{
		"diff.txt": "diff (conflict from host2 <date>).txt",
	})
	want := map[string]string{
		"same.txt":            "identical",
		"diff.txt":            "from a",
		res.Conflicts[0].Copy: "from b",
		"only-a.txt":          "a",
		"only-b.txt":          "b",
	}
	converge(t, remote, a, b, want)
}

func TestConflictCopyNamesAvoidExistingFiles(t *testing.T) {
	remote, a, b := synced(t, map[string]string{"Makefile": "v1", ".bashrc": "v1", "R.md": "v1"})
	today := time.Now().Format("2006-01-02")
	// A file already holds the name the first copy of R.md would get (in
	// another case), on the remote side only.
	a.Write(t, "r (conflict from host2 "+today+").md", "taken")
	for _, p := range []string{"Makefile", ".bashrc", "R.md"} {
		a.Write(t, p, "v2 by a")
		b.Write(t, p, "v2 by b")
	}
	a.Sync(t)
	res, dates := syncConflicts(t, b)
	if dates[0] != today {
		t.Skip("crossed midnight")
	}

	assertConflicts(t, res, dates, map[string]string{
		"Makefile": "Makefile (conflict from host2 <date>)",
		".bashrc":  ".bashrc (conflict from host2 <date>)",
		"R.md":     "R (conflict from host2 <date> 2).md",
	})
	want := map[string]string{
		"Makefile": "v2 by a",
		".bashrc":  "v2 by a",
		"R.md":     "v2 by a",
		"r (conflict from host2 " + today + ").md": "taken",
	}
	for _, c := range res.Conflicts {
		want[c.Copy] = "v2 by b"
	}
	converge(t, remote, a, b, want)
}

func TestRepeatedConflictsOnTheSameDayGetNumberedCopies(t *testing.T) {
	remote, a, b := synced(t, map[string]string{"x.txt": "v1"})

	a.Write(t, "x.txt", "v2 by a")
	b.Write(t, "x.txt", "v2 by b")
	a.Sync(t)
	res1, dates1 := syncConflicts(t, b)
	a.Sync(t)
	a.Write(t, "x.txt", "v3 by a")
	b.Write(t, "x.txt", "v3 by b")
	a.Sync(t)
	res2, dates2 := syncConflicts(t, b)
	if dates1[0] != dates2[1] {
		t.Skip("crossed midnight")
	}

	assertConflicts(t, res1, dates1, map[string]string{"x.txt": "x (conflict from host2 <date>).txt"})
	assertConflicts(t, res2, dates2, map[string]string{"x.txt": "x (conflict from host2 <date> 2).txt"})
	converge(t, remote, a, b, map[string]string{
		"x.txt":                "v3 by a",
		res1.Conflicts[0].Copy: "v2 by b",
		res2.Conflicts[0].Copy: "v3 by b",
	})
}
