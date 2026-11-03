package disk

import (
	"errors"
	"fmt"

	"github.com/Hanson-Tsai/gcd/internal/config"
	"github.com/Hanson-Tsai/gcd/internal/reconcile"
)

// ErrMassDelete is matched (errors.Is) by the error Sync returns when the
// sync would delete more files than the repo config allows without
// confirmation. Nothing was pushed and the folder is unchanged; Sync with
// SyncOptions.Yes runs it anyway.
var ErrMassDelete = errors.New("sync would delete many files")

// minPercentDeletes is the most deletions the percentage threshold lets
// through regardless of folder size, so deleting a file or two from a small
// folder does not need confirmation.
const minPercentDeletes = 5

// checkMassDelete counts the files plan deletes on either side and fails with
// ErrMassDelete when that is more than cfg.MassDeleteCount files, or more than
// minPercentDeletes files that are also more than cfg.MassDeletePercent % of
// the base (last synced) files.
func checkMassDelete(plan []reconcile.Action, baseFiles int, cfg config.Config) (int, error) {
	n := 0
	for _, a := range plan {
		if a.Op == reconcile.DeleteRemote || a.Op == reconcile.DeleteLocal {
			n++
		}
	}
	overPercent := n > minPercentDeletes && n*100 > cfg.MassDeletePercent*baseFiles
	if n > cfg.MassDeleteCount || overPercent {
		return n, fmt.Errorf("%w: %d of %d files (repo limits: %d%% or %d files); nothing was changed",
			ErrMassDelete, n, baseFiles, cfg.MassDeletePercent, cfg.MassDeleteCount)
	}
	return n, nil
}
