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

// checkMassDelete counts the files the plan deletes on either side of s and
// fails with ErrMassDelete when that is more than cfg.MassDeleteCount files,
// or more than minPercentDeletes files that are also more than
// cfg.MassDeletePercent % of the base (last synced) files, or when the plan
// deletes every synced file one side still has (a folder emptied by mistake
// or a drive that is not mounted), however few that is.
func checkMassDelete(plan []reconcile.Action, s sides, cfg config.Config) (int, error) {
	var remoteDeletes, localDeletes int
	for _, a := range plan {
		switch a.Op {
		case reconcile.DeleteRemote:
			remoteDeletes++
		case reconcile.DeleteLocal:
			localDeletes++
		}
	}
	n, baseFiles := remoteDeletes+localDeletes, len(s.base)
	emptied := remoteDeletes > 0 && remoteDeletes == syncedOn(s.base, s.remote) ||
		localDeletes > 0 && localDeletes == syncedOn(s.base, s.local)
	overPercent := n > minPercentDeletes && n*100 > cfg.MassDeletePercent*baseFiles
	if n > cfg.MassDeleteCount || overPercent || emptied {
		return n, fmt.Errorf("%w: %d of %d files (repo limits: %d%% or %d files, or every synced file on one side); nothing was changed",
			ErrMassDelete, n, baseFiles, cfg.MassDeletePercent, cfg.MassDeleteCount)
	}
	return n, nil
}

// syncedOn counts the base paths that side still has.
func syncedOn(base, side reconcile.Files) int {
	n := 0
	for p := range base {
		if _, ok := side[p]; ok {
			n++
		}
	}
	return n
}
