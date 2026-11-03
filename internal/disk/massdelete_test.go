package disk_test

import (
	"context"
	"errors"
	"fmt"
	"testing"

	"github.com/Hanson-Tsai/gcd/internal/disk"
	"github.com/Hanson-Tsai/gcd/internal/fixture"
)

// files returns n files named f0.md .. f<n-1>.md.
func files(n int) map[string]string {
	m := make(map[string]string, n)
	for i := range n {
		m[fmt.Sprintf("f%d.md", i)] = fmt.Sprint(i)
	}
	return m
}

func writeAll(t *testing.T, d *fixture.Device, m map[string]string) {
	t.Helper()
	for p, c := range m {
		d.Write(t, p, c)
	}
}

func TestEmptiedFolderNeedsConfirmationAndLeavesTheRemoteAlone(t *testing.T) {
	remote, devs := fixture.NewDevices(t, 1)
	a := devs[0]
	all := files(10)
	writeAll(t, a, all)
	a.Sync(t)
	before := remote.Head(t)
	for p := range all {
		a.Remove(t, p)
	}

	res, err := a.Disk.Sync(context.Background(), disk.SyncOptions{})

	if !errors.Is(err, disk.ErrMassDelete) {
		t.Fatalf("sync error = %v, want ErrMassDelete", err)
	}
	if !res.NeedsConfirm || res.PendingDeletes != 10 {
		t.Errorf("NeedsConfirm, PendingDeletes = %v, %d; want true, 10", res.NeedsConfirm, res.PendingDeletes)
	}
	if got := remote.Head(t); got != before {
		t.Errorf("remote moved from %s to %s", before, got)
	}
	remote.AssertFiles(t, all)
	a.AssertFiles(t, map[string]string{})
}

func TestYesConfirmsAMassDelete(t *testing.T) {
	remote, devs := fixture.NewDevices(t, 1)
	a := devs[0]
	all := files(10)
	writeAll(t, a, all)
	a.Sync(t)
	for p := range all {
		a.Remove(t, p)
	}

	res, err := a.Disk.Sync(context.Background(), disk.SyncOptions{Yes: true})

	if err != nil {
		t.Fatalf("sync --yes: %v", err)
	}
	if res.NeedsConfirm || len(res.Uploaded) != 10 {
		t.Errorf("NeedsConfirm = %v, %d uploaded; want false, 10", res.NeedsConfirm, len(res.Uploaded))
	}
	remote.AssertFiles(t, map[string]string{})
}

func TestMassDeleteFromTheRemoteLeavesTheFolderAlone(t *testing.T) {
	remote, devs := fixture.NewDevices(t, 2)
	a, b := devs[0], devs[1]
	all := files(10)
	writeAll(t, a, all)
	a.Sync(t)
	b.Sync(t)
	for p := range all {
		a.Remove(t, p)
	}
	if _, err := a.Disk.Sync(context.Background(), disk.SyncOptions{Yes: true}); err != nil {
		t.Fatal(err)
	}
	b.Write(t, "new.md", "new")
	before := remote.Head(t)

	res, err := b.Disk.Sync(context.Background(), disk.SyncOptions{})

	if !errors.Is(err, disk.ErrMassDelete) || res.PendingDeletes != 10 {
		t.Fatalf("sync = %d pending, %v; want 10, ErrMassDelete", res.PendingDeletes, err)
	}
	if got := remote.Head(t); got != before {
		t.Errorf("remote moved from %s to %s", before, got)
	}
	all["new.md"] = "new"
	b.AssertFiles(t, all)
}

func TestMassDeleteThresholdsComeFromTheRepoConfig(t *testing.T) {
	for name, tc := range map[string]struct {
		config  string
		deletes int
		confirm bool
	}{
		"over the count":      {"mass_delete_count = 5\nmass_delete_percent = 100\n", 6, true},
		"at the count":        {"mass_delete_count = 5\nmass_delete_percent = 100\n", 5, false},
		"over the percentage": {"mass_delete_count = 1000\nmass_delete_percent = 50\n", 11, true},
		"at the percentage":   {"mass_delete_count = 1000\nmass_delete_percent = 50\n", 10, false},
		"default percentage":  {"", 6, true}, // 6 of 20 = 30% > 20%
	} {
		t.Run(name, func(t *testing.T) {
			remote, devs := fixture.NewDevices(t, 1)
			a := devs[0]
			remote.Commit(t, map[string]string{".gcd/config.toml": tc.config}, "config")
			writeAll(t, a, files(20))
			a.Sync(t)
			for i := range tc.deletes {
				a.Remove(t, fmt.Sprintf("f%d.md", i))
			}

			res, err := a.Disk.Sync(context.Background(), disk.SyncOptions{})

			if got := errors.Is(err, disk.ErrMassDelete); got != tc.confirm || res.NeedsConfirm != tc.confirm {
				t.Fatalf("sync error = %v, NeedsConfirm = %v; want confirmation %v", err, res.NeedsConfirm, tc.confirm)
			}
			if !tc.confirm && err != nil {
				t.Fatal(err)
			}
		})
	}
}

func TestDeletingAFewFilesFromASmallFolderNeedsNoConfirmation(t *testing.T) {
	remote, devs := fixture.NewDevices(t, 1)
	a := devs[0]
	writeAll(t, a, files(3))
	a.Sync(t)
	a.Remove(t, "f0.md")
	a.Remove(t, "f1.md")

	a.Sync(t) // 2 of 3 files: over 20%, but too few files to be a mass delete

	remote.AssertFiles(t, map[string]string{"f2.md": "2"})
}

func TestEmptyingASmallFolderNeedsConfirmation(t *testing.T) {
	remote, devs := fixture.NewDevices(t, 1)
	a := devs[0]
	all := files(3)
	writeAll(t, a, all)
	a.Sync(t)
	before := remote.Head(t)
	for p := range all {
		a.Remove(t, p) // the folder was emptied, or the drive is not mounted
	}

	res, err := a.Disk.Sync(context.Background(), disk.SyncOptions{})

	if !errors.Is(err, disk.ErrMassDelete) || !res.NeedsConfirm || res.PendingDeletes != 3 {
		t.Fatalf("sync = NeedsConfirm %v, %d pending, %v; want true, 3, ErrMassDelete",
			res.NeedsConfirm, res.PendingDeletes, err)
	}
	if got := remote.Head(t); got != before {
		t.Errorf("remote moved from %s to %s", before, got)
	}
	remote.AssertFiles(t, all)
}

func TestEmptyingASmallRemoteNeedsConfirmation(t *testing.T) {
	remote, devs := fixture.NewDevices(t, 2)
	a, b := devs[0], devs[1]
	all := files(2)
	writeAll(t, a, all)
	a.Sync(t)
	b.Sync(t)
	for p := range all {
		a.Remove(t, p)
	}
	if _, err := a.Disk.Sync(context.Background(), disk.SyncOptions{Yes: true}); err != nil {
		t.Fatal(err)
	}

	_, err := b.Disk.Sync(context.Background(), disk.SyncOptions{})

	if !errors.Is(err, disk.ErrMassDelete) {
		t.Fatalf("sync error = %v, want ErrMassDelete", err)
	}
	b.AssertFiles(t, all)
	remote.AssertFiles(t, map[string]string{})
}
