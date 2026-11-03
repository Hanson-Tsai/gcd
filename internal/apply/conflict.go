package apply

import (
	"fmt"
	"os"
	"path"
	"path/filepath"
	"strings"
	"time"
)

// CopyName returns the conflict-copy path for rel (slash-separated):
// "<stem> (conflict from <host> <YYYY-MM-DD>)<ext>" in the same directory,
// keeping the extension. A dotfile with no other extension (".bashrc") keeps
// its whole name as the stem. If the name is taken (taken is called with the
// candidate path), " 2", " 3", ... is added inside the parentheses.
func CopyName(rel, host string, now time.Time, taken func(string) bool) string {
	dir, name := path.Split(rel)
	ext := path.Ext(name)
	stem := strings.TrimSuffix(name, ext)
	if stem == "" {
		stem, ext = name, ""
	}
	label := fmt.Sprintf("conflict from %s %s", host, now.Format("2006-01-02"))
	for n := 1; ; n++ {
		suffix := ""
		if n > 1 {
			suffix = fmt.Sprintf(" %d", n)
		}
		candidate := fmt.Sprintf("%s%s (%s%s)%s", dir, stem, label, suffix, ext)
		if !taken(candidate) {
			return candidate
		}
	}
}

// keepAsCopy moves the local file at rel to the conflict copy path copy. It
// never overwrites an existing file at copy. When rel no longer exists (the
// user deleted it after the scan) there is nothing to keep: it reports
// moved = false and no error.
func keepAsCopy(root, rel, copy string) (moved bool, err error) {
	from := filepath.Join(root, filepath.FromSlash(rel))
	to := filepath.Join(root, filepath.FromSlash(copy))
	if _, err := os.Lstat(from); os.IsNotExist(err) {
		return false, nil
	}
	if _, err := os.Lstat(to); err == nil {
		return false, fmt.Errorf("conflict copy %s already exists", copy)
	} else if !os.IsNotExist(err) {
		return false, err
	}
	if err := os.MkdirAll(filepath.Dir(to), 0o755); err != nil {
		return false, err
	}
	if err := rename(from, to); err != nil {
		return false, err
	}
	return true, nil
}
