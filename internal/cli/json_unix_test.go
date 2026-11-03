//go:build !windows

package cli_test

import (
	"os"
	"path/filepath"
	"syscall"
	"testing"

	"github.com/Hanson-Tsai/gcd/internal/fixture"
)

// holdSyncLock takes the Disk's sync lock like another gcd process would.
func holdSyncLock(t *testing.T, m machine, disk string) {
	t.Helper()
	f, err := os.OpenFile(filepath.Join(m.home, "disks", disk, "sync.lock"), os.O_RDWR|os.O_CREATE, 0o644)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { f.Close() })
	if err := syscall.Flock(int(f.Fd()), syscall.LOCK_EX|syscall.LOCK_NB); err != nil {
		t.Fatal(err)
	}
}

func TestJSONErrorBusyWhileAnotherGcdSyncs(t *testing.T) {
	remote := fixture.NewRemote(t)
	a := newMachine(t, "a")
	a.gcd(t, "init", remote.URL, a.folder)
	writeFile(t, filepath.Join(a.folder, "x.md"), "x")
	holdSyncLock(t, a, "a-folder")

	for _, args := range [][]string{{"sync", "--json"}, {"restore", "x.md", "--json"}} {
		if e := a.gcdJSONError(t, args...); e.Error.Kind != "busy" {
			t.Errorf("gcd %v while locked: %+v, want busy", args, e.Error)
		}
	}
}
