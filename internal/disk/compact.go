package disk

import (
	"context"
	"path/filepath"

	"github.com/Hanson-Tsai/gcd/internal/compact"
)

// GitDir is the bare git-dir inside a Disk's state dir.
func GitDir(stateDir string) string { return filepath.Join(stateDir, "git") }

// Compact compacts the remote's history from this machine (gcd compact
// --local; see compact.Run) in the Disk's own git-dir, so only new objects
// are downloaded. It holds the sync lock meanwhile, so it never shares the
// git-dir with a Sync or Restore: ErrAlreadySyncing when one runs.
func (d *Disk) Compact(ctx context.Context) (compact.Result, error) {
	unlock, err := d.lock()
	if err != nil {
		return compact.Result{}, err
	}
	defer unlock()
	return compact.Run(ctx, compact.Options{GitDir: GitDir(d.stateDir), Remote: d.remote, Token: d.token})
}
