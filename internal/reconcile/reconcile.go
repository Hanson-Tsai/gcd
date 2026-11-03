// Package reconcile holds the three-way comparison rules. It is pure: given
// the base, local and remote file lists (path -> content hash), it returns the
// actions that bring both sides to the same state. It is tested through the
// disk package (Seam A), not directly.
package reconcile

import "sort"

// Files maps a slash-separated path to a content hash. A missing path means
// the file does not exist on that side.
type Files map[string]string

// Op is what to do with one path.
type Op int

const (
	// Upload: the local version wins; put Hash at Path in the new remote tree.
	Upload Op = iota + 1
	// DeleteRemote: the file was deleted locally; drop Path from the remote tree.
	DeleteRemote
	// Download: the remote version wins; write Hash to Path in the folder.
	Download
	// DeleteLocal: the file was deleted remotely; remove Path from the folder.
	DeleteLocal
	// Conflict: both sides changed Path to different content. The remote
	// version (Hash) stays at Path; the local version moves to Copy, a
	// conflict copy, and both are uploaded.
	Conflict
)

func (o Op) String() string {
	switch o {
	case Upload:
		return "upload"
	case DeleteRemote:
		return "delete-remote"
	case Download:
		return "download"
	case DeleteLocal:
		return "delete-local"
	case Conflict:
		return "conflict"
	}
	return "unknown"
}

// Action is one step of a plan.
type Action struct {
	Op   Op
	Path string
	Hash string // target content at Path for Upload/Download/Conflict; empty for deletes
	// Local and Remote are the hashes on each side ("" = absent); set for
	// every action so callers can classify adds vs modifications.
	Local, Remote string
	// Copy is the conflict-copy path for a Conflict. Reconcile leaves it
	// empty; the caller names copies (see apply.CopyName).
	Copy string
}

// Reconcile compares base, local and remote and returns actions sorted by path.
// Paths where local and remote already agree produce no action (this covers
// both sides making the same change and both sides deleting). A change on one
// side wins; an edit wins over a delete; different edits are a Conflict. On
// the first sync base is empty, so differing files on both sides conflict.
func Reconcile(base, local, remote Files) []Action {
	paths := map[string]struct{}{}
	for _, m := range []Files{base, local, remote} {
		for p := range m {
			paths[p] = struct{}{}
		}
	}
	var actions []Action
	for p := range paths {
		b, l, r := base[p], local[p], remote[p]
		a := Action{Path: p, Local: l, Remote: r}
		switch {
		case l == r:
			continue // in agreement (including both deleted)
		case l == b: // only the remote side changed
			if r == "" {
				a.Op = DeleteLocal
			} else {
				a.Op, a.Hash = Download, r
			}
		case r == b: // only the local side changed
			if l == "" {
				a.Op = DeleteRemote
			} else {
				a.Op, a.Hash = Upload, l
			}
		case l == "": // deleted locally, modified remotely: keep the edit
			a.Op, a.Hash = Download, r
		case r == "": // deleted remotely, modified locally: keep the edit
			a.Op, a.Hash = Upload, l
		default:
			a.Op, a.Hash = Conflict, r
		}
		actions = append(actions, a)
	}
	sort.Slice(actions, func(i, j int) bool { return actions[i].Path < actions[j].Path })
	return actions
}
