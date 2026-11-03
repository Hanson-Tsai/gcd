package disk

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
)

// lockFile lives in the state dir. It is held for the whole of a Sync.
const lockFile = "sync.lock"

// ErrAlreadySyncing is returned when another gcd process (or another Disk
// value in this process) is syncing the same Disk right now.
var ErrAlreadySyncing = errors.New("already syncing in another gcd process")

// lock takes the per-Disk sync lock without waiting. It is an OS advisory
// lock on <StateDir>/sync.lock (flock on Unix, LockFileEx on Windows), so the
// OS drops it whenever the holder exits, however it exits: a crashed or killed
// gcd leaves at most an unlocked file behind, never a stale lock.
//
// The file is never deleted. Removing it on unlock would let one process lock
// the old file while another creates and locks a new one.
func (d *Disk) lock() (unlock func(), err error) {
	f, err := os.OpenFile(filepath.Join(d.stateDir, lockFile), os.O_RDWR|os.O_CREATE, 0o644)
	if err != nil {
		return nil, fmt.Errorf("open sync lock: %w", err)
	}
	ok, err := tryLock(f)
	if err != nil || !ok {
		f.Close()
		if err != nil {
			return nil, fmt.Errorf("take sync lock: %w", err)
		}
		return nil, fmt.Errorf("%w: %s", ErrAlreadySyncing, d.folder)
	}
	return func() {
		_ = unlockFile(f)
		_ = f.Close()
	}, nil
}
