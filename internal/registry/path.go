package registry

import (
	"os"
	"path/filepath"
	"runtime"
	"strings"
)

// foldCase reports whether paths on this OS's default file systems compare
// case-insensitively. It is only the fallback for paths that do not exist;
// existing paths are compared by file identity, which also gets
// case-sensitive volumes on macOS and case-insensitive ones on Linux right.
var foldCase = runtime.GOOS == "windows" || runtime.GOOS == "darwin"

// contains reports whether path is folder or lies somewhere below it.
//
// Each ancestor of path is compared with folder by file identity
// (os.SameFile), so different letter case on a case-insensitive file
// system, symlinked parents (macOS /tmp -> /private/tmp) and Windows short
// names all match. When folder does not exist (moved or deleted), cleaned
// paths are compared as text instead.
func contains(folder, path string) bool {
	folder = filepath.Clean(folder)
	fi, err := os.Stat(folder)
	if err != nil {
		fi = nil
	}
	for p := filepath.Clean(path); ; {
		if samePathAs(folder, fi, p) {
			return true
		}
		parent := filepath.Dir(p)
		if parent == p {
			return false
		}
		p = parent
	}
}

// samePathAs reports whether p names folder, whose Stat result is fi (nil
// when folder does not exist).
func samePathAs(folder string, fi os.FileInfo, p string) bool {
	if p == folder {
		return true
	}
	if fi != nil {
		if pi, err := os.Stat(p); err == nil {
			return os.SameFile(fi, pi)
		}
	}
	return foldCase && strings.EqualFold(p, folder)
}
