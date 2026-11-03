package cli

import (
	"flag"
	"fmt"
	"path/filepath"
	"strings"
)

// parseInterspersed parses fs from args, allowing flags after positional
// arguments (`gcd restore a.txt --at X`), and returns the positionals. A
// literal "--" ends flag parsing.
func parseInterspersed(fs *flag.FlagSet, args []string) ([]string, error) {
	var pos []string
	for {
		if err := fs.Parse(args); err != nil {
			return nil, err
		}
		rest := fs.Args()
		if len(rest) == 0 {
			return pos, nil
		}
		// flag stops at the first non-flag or after "--"; tell them apart.
		if len(args) > len(rest) && args[len(args)-len(rest)-1] == "--" {
			return append(pos, rest...), nil
		}
		pos = append(pos, rest[0])
		args = rest[1:]
	}
}

// folderPath turns a user-given path into a slash-separated path relative to
// the disk folder. The path is taken relative to the current directory; a
// relative path that falls outside the folder (e.g. when running from
// elsewhere) is taken relative to the folder instead.
func folderPath(folder, arg string) (string, error) {
	abs, err := filepath.Abs(arg)
	if err != nil {
		return "", err
	}
	rel, err := filepath.Rel(folder, abs)
	if err != nil || outside(rel) {
		// The cwd may reach the folder through a symlink (or a short name on
		// Windows): compare resolved paths.
		if rf, err1 := filepath.EvalSymlinks(folder); err1 == nil {
			if r, err2 := filepath.Rel(rf, resolveExisting(abs)); err2 == nil {
				rel, err = r, nil
			}
		}
	}
	if err != nil || outside(rel) {
		if filepath.IsAbs(arg) {
			return "", fmt.Errorf("%s is outside the disk folder %s", arg, folder)
		}
		rel = arg
	}
	if rel == "." {
		return "", fmt.Errorf("%s is the disk folder itself; give a path inside it", arg)
	}
	return filepath.ToSlash(filepath.Clean(rel)), nil
}

func outside(rel string) bool {
	return rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) || filepath.IsAbs(rel)
}

// resolveExisting resolves symlinks in the longest existing prefix of abs
// (the file itself may not exist, e.g. when restoring a deleted file).
func resolveExisting(abs string) string {
	var tail []string
	for p := abs; ; p = filepath.Dir(p) {
		if r, err := filepath.EvalSymlinks(p); err == nil {
			return filepath.Join(append([]string{r}, tail...)...)
		}
		if filepath.Dir(p) == p {
			return abs
		}
		tail = append([]string{filepath.Base(p)}, tail...)
	}
}
