//go:build !windows

package disk

import (
	"errors"
	"os"
	"syscall"
)

// tryLock takes an exclusive flock on f without blocking. flock locks belong
// to the open file, so two opens in one process also exclude each other.
// os.OpenFile sets O_CLOEXEC, so git child processes never inherit the lock.
func tryLock(f *os.File) (bool, error) {
	err := syscall.Flock(int(f.Fd()), syscall.LOCK_EX|syscall.LOCK_NB)
	if errors.Is(err, syscall.EWOULDBLOCK) {
		return false, nil
	}
	return err == nil, err
}

func unlockFile(f *os.File) error {
	return syscall.Flock(int(f.Fd()), syscall.LOCK_UN)
}
