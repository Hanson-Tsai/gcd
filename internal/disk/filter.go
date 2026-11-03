package disk

import (
	"sort"

	"github.com/Hanson-Tsai/gcd/internal/config"
	"github.com/Hanson-Tsai/gcd/internal/gitstore"
	"github.com/Hanson-Tsai/gcd/internal/reconcile"
	"github.com/Hanson-Tsai/gcd/internal/scan"
)

// symlinkMode is the tree mode of a symlink, which only other git clients
// can put in the repo.
const symlinkMode = "120000"

// sides is the reconcile input after the file rules have been applied.
type sides struct {
	base, local, remote reconcile.Files
	// excluded are base and remote paths the rules leave alone. Like managed
	// paths they are never downloaded, uploaded or deleted. They also keep
	// their old base entry, so that when a rule stops applying (an ignore
	// rule is removed, a collision is renamed away) the next sync sees what
	// really changed on each side in the meantime.
	excluded []string
	rejected []scan.Rejection // sorted by path
}

// applyRules filters base, local and remote through the file rules: the
// scan's ignore rules, name policy, size limit and regular-files-only rule
// (scan.Result.Exclude), case collisions across local and remote together
// (scan.Collisions), and managed paths. A path one side cannot have is left
// out on every side, so a rejected file is never mistaken for a deletion and
// a file another client pushed with a bad name never reaches the folder.
func applyRules(local scan.Result, base, remote map[string]gitstore.Entry) sides {
	s := sides{local: localHashes(local), base: reconcile.Files{}, remote: reconcile.Files{}}
	s.rejected = append(s.rejected, local.Rejected...)
	excluded := map[string]bool{}
	keep := func(p string, e gitstore.Entry, report bool) bool {
		if config.IsManaged(p) {
			return false
		}
		if e.Mode == symlinkMode {
			if report {
				s.rejected = append(s.rejected, scan.Rejection{Path: p, Reason: scan.NotRegular,
					Detail: "symlinks are not synced", Remote: true})
			}
			excluded[p] = true
			return false
		}
		rej, out := local.Exclude(p)
		if rej != nil && report {
			s.rejected = append(s.rejected, *rej)
		}
		if out {
			excluded[p] = true
		}
		return !out
	}
	for p, e := range remote {
		if keep(p, e, true) {
			s.remote[p] = e.Hash
		}
	}
	for p, e := range base {
		if keep(p, e, false) {
			s.base[p] = e.Hash
		}
	}

	var paths []string
	for p := range s.local {
		paths = append(paths, p)
	}
	for p := range s.remote {
		if _, ok := s.local[p]; !ok {
			paths = append(paths, p)
		}
	}
	for p, detail := range scan.Collisions(paths) {
		_, inLocal := s.local[p]
		s.rejected = append(s.rejected, scan.Rejection{Path: p, Reason: scan.CaseCollision,
			Detail: detail, Remote: !inLocal})
		delete(s.local, p)
		delete(s.remote, p)
		delete(s.base, p)
		excluded[p] = true
	}

	for p := range excluded {
		s.excluded = append(s.excluded, p)
	}
	sort.Strings(s.excluded)
	sort.Slice(s.rejected, func(i, j int) bool { return s.rejected[i].Path < s.rejected[j].Path })
	return s
}
